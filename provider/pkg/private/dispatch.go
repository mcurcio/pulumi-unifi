package private

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	pbempty "github.com/golang/protobuf/ptypes/empty"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/logging"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/binding/official"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/controller"
	core "github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/binding"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
)

// Config keys and environment fallbacks the dispatcher reads (design §7).
const (
	keyBaseURL = "networkBaseUrl"
	envBaseURL = "UNIFI_NETWORK_BASE_URL"
)

var configEnv = map[string]string{
	"apiKey":        "UNIFI_APIKEY",
	"apiHost":       "UNIFI_API_HOST",
	"siteId":        "UNIFI_SITEID",
	"allowInsecure": "UNIFI_ALLOW_INSECURE",
	keyBaseURL:      envBaseURL,
}

// Dispatcher wraps the framework's ResourceProviderServer (design §2):
// unifi:network:* goes to the private engine, everything else to the
// framework, unchanged.
type Dispatcher struct {
	pulumirpc.ResourceProviderServer // the framework (official resources)

	name    string
	schema  []byte
	surface *engine.Surface
	client  *binding.Client
	engine  *engine.Engine
	// ctl is the controller access shared by the private binding and the
	// official singletons' binding.
	ctl   *controller.Client
	guard *versionGuard

	// ordering serves the official singletons (surface.Singletons) through
	// the official binding; its binding is built in Configure.
	ordering *core.Ordering

	// officialErr is set when the official side was not configured (no
	// apiKey while private kinds are served); official calls return it.
	officialErr error
}

// NewDispatcher wraps fw. mergedSchema is returned from GetSchema; surface is
// private.json. With no private kinds the dispatcher only swaps the schema
// bytes (equal to the golden at PRIVATE=0) and forwards everything.
func NewDispatcher(fw pulumirpc.ResourceProviderServer, name string, mergedSchema, surface []byte) (*Dispatcher, error) {
	s, err := engine.ParseSurface(surface)
	if err != nil {
		return nil, err
	}
	c := &binding.Client{Site: "default"}
	d := &Dispatcher{
		ResourceProviderServer: fw, name: name, schema: mergedSchema, surface: s, client: c,
	}
	d.guard = &versionGuard{Binding: c, want: s.Source.NetworkVersion, read: c.NetworkVersion}
	d.engine = &engine.Engine{Surface: s, Binding: d.guard}
	d.configureClients(controller.Config{}, "default")
	d.ordering = &core.Ordering{
		Binding: official.New(d.ctl),
		Site:    func() string { return d.client.Site },
		Warn:    func(msg string) { logging.Warningf("%s", msg) },
	}
	return d, nil
}

// configureClients builds the one controller client every engine-served kind
// shares (TLS policy, X-API-Key, 429 retry; docs/design/private-binding.md
// §S2.2 L1, §S2.6) and points the private binding at its Network root: the
// authenticated edge https://<apiHost>/proxy/network by default, or the
// networkBaseUrl override.
func (d *Dispatcher) configureClients(cfg controller.Config, site string) {
	d.ctl = controller.New(cfg)
	root, err := d.ctl.NetworkRoot()
	if err != nil {
		root = "" // the binding reports what to set on the first call
	}
	d.client.BaseURL = root
	d.client.Site = site
	d.client.Ctl = d.ctl
	d.client.Sites = &controller.SiteResolver{C: d.ctl}
}

// singleton returns the official singleton the URN's type names, or nil.
func (d *Dispatcher) singleton(urn string) *core.Singleton {
	return d.surface.FindSingleton(string(resource.URN(urn).Type()))
}

// Surface returns the served private kinds.
func (d *Dispatcher) Surface() *engine.Surface { return d.surface }

// Client returns the private HTTP client (tests).
func (d *Dispatcher) Client() *binding.Client { return d.client }

func (d *Dispatcher) private(urn string) *engine.Kind {
	t := string(resource.URN(urn).Type())
	if !strings.HasPrefix(t, engine.TokenPrefix) {
		return nil
	}
	return d.surface.Find(t)
}

func (d *Dispatcher) isPrivateToken(urn string) bool {
	return strings.HasPrefix(string(resource.URN(urn).Type()), engine.TokenPrefix)
}

// GetSchema returns the merged schema.
func (d *Dispatcher) GetSchema(context.Context, *pulumirpc.GetSchemaRequest) (*pulumirpc.GetSchemaResponse, error) {
	return &pulumirpc.GetSchemaResponse{Schema: string(d.schema)}, nil
}

// ---------------------------------------------------------------- config

func stripStruct(s *structpb.Struct) (*structpb.Struct, *structpb.Value) {
	if s == nil {
		return nil, nil
	}
	v, ok := s.GetFields()[keyBaseURL]
	if !ok {
		return s, nil
	}
	cp := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	for k, e := range s.GetFields() {
		if k != keyBaseURL {
			cp.Fields[k] = e
		}
	}
	return cp, v
}

// CheckConfig forwards the official keys and keeps networkBaseUrl.
func (d *Dispatcher) CheckConfig(ctx context.Context, req *pulumirpc.CheckRequest) (*pulumirpc.CheckResponse, error) {
	news, base := stripStruct(req.GetNews())
	olds, _ := stripStruct(req.GetOlds())
	fwd := &pulumirpc.CheckRequest{Urn: req.GetUrn(), Olds: olds, News: news, RandomSeed: req.GetRandomSeed(), Name: req.GetName(), Type: req.GetType()}
	resp, err := d.ResourceProviderServer.CheckConfig(ctx, fwd)
	if err != nil || base == nil {
		return resp, err
	}
	inputs := &structpb.Struct{Fields: map[string]*structpb.Value{keyBaseURL: base}}
	for k, v := range resp.GetInputs().GetFields() {
		inputs.Fields[k] = v
	}
	resp.Inputs = inputs
	if s := base.GetStringValue(); s != "" && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		resp.Failures = append(resp.Failures, &pulumirpc.CheckFailure{Property: keyBaseURL,
			Reason: fmt.Sprintf("networkBaseUrl %q must be an http:// or https:// URL", s)})
	}
	return resp, nil
}

// Configure configures the private client, then the framework with
// networkBaseUrl removed.
func (d *Dispatcher) Configure(ctx context.Context, req *pulumirpc.ConfigureRequest) (*pulumirpc.ConfigureResponse, error) {
	vars := req.GetVariables()
	get := func(key string) string {
		if v := vars[d.name+":config:"+key]; v != "" {
			return v
		}
		if v := req.GetArgs().GetFields()[key]; v != nil && v.GetStringValue() != "" {
			return v.GetStringValue()
		}
		return os.Getenv(configEnv[key])
	}
	apiKey := get("apiKey")
	site := get("siteId")
	if site == "" {
		site = "default"
	}
	insecure, _ := strconv.ParseBool(get("allowInsecure"))
	d.configureClients(controller.Config{APIHost: get("apiHost"), APIKey: apiKey, AllowInsecure: insecure,
		NetworkBaseURL: get(keyBaseURL)}, site)
	d.ordering.Binding = official.New(d.ctl)
	d.guard.mu.Lock()
	d.guard.done, d.guard.result = false, nil
	d.guard.mu.Unlock()
	if len(d.surface.Kinds) > 0 {
		logging.V(3).Infof("private API: %d kinds, built for Network %s; base %q, site %q, api key set: %v",
			len(d.surface.Kinds), d.surface.Source.NetworkVersion, d.client.BaseURL, site, apiKey != "")
	}

	fv := make(map[string]string, len(vars))
	for k, v := range vars {
		if k != d.name+":config:"+keyBaseURL {
			fv[k] = v
		}
	}
	args, _ := stripStruct(req.GetArgs())
	fwd := &pulumirpc.ConfigureRequest{Variables: fv, Args: args, AcceptSecrets: req.GetAcceptSecrets(),
		AcceptResources: req.GetAcceptResources(), SendsOldInputs: req.GetSendsOldInputs(),
		SendsOldInputsToDelete: req.GetSendsOldInputsToDelete()}
	if apiKey == "" && len(d.surface.Kinds) > 0 {
		// Private kinds work without a key against the Network app directly
		// (design §7); the official resources need one.
		d.officialErr = errors.New("a UniFi API key is required for the official resources (set unifi:apiKey or the UNIFI_APIKEY env var)")
		return &pulumirpc.ConfigureResponse{AcceptSecrets: true}, nil
	}
	d.officialErr = nil
	return d.ResourceProviderServer.Configure(ctx, fwd)
}

// ---------------------------------------------------------------- resources

var marshalOpts = plugin.MarshalOptions{KeepUnknowns: true, KeepSecrets: true, KeepOutputValues: true}

func unmarshal(s *structpb.Struct) (resource.PropertyMap, error) {
	if s == nil {
		return nil, nil
	}
	return plugin.UnmarshalProperties(s, marshalOpts)
}

func marshal(pm resource.PropertyMap) (*structpb.Struct, error) {
	return plugin.MarshalProperties(pm, marshalOpts)
}

func (d *Dispatcher) unknownKind(urn string) error {
	return fmt.Errorf("%s is not served by this build of the provider (private kinds: %v); build with PRIVATE=1",
		resource.URN(urn).Type(), d.surface.Tokens())
}

// Check validates a private resource's inputs.
func (d *Dispatcher) Check(ctx context.Context, req *pulumirpc.CheckRequest) (*pulumirpc.CheckResponse, error) {
	if sg := d.singleton(req.GetUrn()); sg != nil {
		return d.checkSingleton(sg, req)
	}
	if !d.isPrivateToken(req.GetUrn()) {
		if d.officialErr != nil {
			return nil, d.officialErr
		}
		return d.ResourceProviderServer.Check(ctx, req)
	}
	k := d.private(req.GetUrn())
	if k == nil {
		return nil, d.unknownKind(req.GetUrn())
	}
	olds, err := unmarshal(req.GetOlds())
	if err != nil {
		return nil, err
	}
	news, err := unmarshal(req.GetNews())
	if err != nil {
		return nil, err
	}
	resp := &pulumirpc.CheckResponse{Inputs: req.GetNews()}
	for _, f := range d.engine.Check(k, olds, news) {
		resp.Failures = append(resp.Failures, &pulumirpc.CheckFailure{Property: f.Property, Reason: f.Reason})
	}
	return resp, nil
}

// Diff compares managed paths.
func (d *Dispatcher) Diff(ctx context.Context, req *pulumirpc.DiffRequest) (*pulumirpc.DiffResponse, error) {
	if sg := d.singleton(req.GetUrn()); sg != nil {
		return d.diffSingleton(sg, req)
	}
	if !d.isPrivateToken(req.GetUrn()) {
		if d.officialErr != nil {
			return nil, d.officialErr
		}
		return d.ResourceProviderServer.Diff(ctx, req)
	}
	k := d.private(req.GetUrn())
	if k == nil {
		return nil, d.unknownKind(req.GetUrn())
	}
	oldS := req.GetOldInputs()
	var outs resource.PropertyMap
	if oldS == nil {
		oldS = req.GetOlds()
	} else if req.GetOlds() != nil {
		o, err := unmarshal(req.GetOlds())
		if err != nil {
			return nil, err
		}
		outs = o
	}
	olds, err := unmarshal(oldS)
	if err != nil {
		return nil, err
	}
	news, err := unmarshal(req.GetNews())
	if err != nil {
		return nil, err
	}
	r, err := d.engine.DiffLive(k, olds, news, outs)
	if err != nil {
		return nil, err
	}
	resp := &pulumirpc.DiffResponse{Changes: pulumirpc.DiffResponse_DIFF_NONE, HasDetailedDiff: true,
		DetailedDiff: map[string]*pulumirpc.PropertyDiff{}, Replaces: r.Replaces, Diffs: r.Changed}
	if len(r.Changed) > 0 {
		resp.Changes = pulumirpc.DiffResponse_DIFF_SOME
	}
	for path, k := range r.Detailed {
		kind := map[string]pulumirpc.PropertyDiff_Kind{"add": pulumirpc.PropertyDiff_ADD, "update": pulumirpc.PropertyDiff_UPDATE,
			"delete": pulumirpc.PropertyDiff_DELETE}[k]
		top, _, _ := strings.Cut(path, ".")
		for _, rp := range r.Replaces {
			if rp == top {
				kind += pulumirpc.PropertyDiff_ADD_REPLACE - pulumirpc.PropertyDiff_ADD
			}
		}
		resp.DetailedDiff[path] = &pulumirpc.PropertyDiff{Kind: kind, InputDiff: true}
	}
	return resp, nil
}

// Create creates a private object.
func (d *Dispatcher) Create(ctx context.Context, req *pulumirpc.CreateRequest) (*pulumirpc.CreateResponse, error) {
	if sg := d.singleton(req.GetUrn()); sg != nil {
		return d.createSingleton(ctx, sg, req)
	}
	if !d.isPrivateToken(req.GetUrn()) {
		if d.officialErr != nil {
			return nil, d.officialErr
		}
		return d.ResourceProviderServer.Create(ctx, req)
	}
	k := d.private(req.GetUrn())
	if k == nil {
		return nil, d.unknownKind(req.GetUrn())
	}
	if req.GetPreview() {
		return &pulumirpc.CreateResponse{Properties: req.GetProperties()}, nil
	}
	in, err := unmarshal(req.GetProperties())
	if err != nil {
		return nil, err
	}
	id, out, err := d.engine.Create(ctx, k, in)
	if err != nil {
		return nil, err
	}
	props, err := marshal(out)
	if err != nil {
		return nil, err
	}
	return &pulumirpc.CreateResponse{Id: id, Properties: props}, nil
}

// Read refreshes or imports a private object.
func (d *Dispatcher) Read(ctx context.Context, req *pulumirpc.ReadRequest) (*pulumirpc.ReadResponse, error) {
	if sg := d.singleton(req.GetUrn()); sg != nil {
		return d.readSingleton(ctx, sg, req)
	}
	if !d.isPrivateToken(req.GetUrn()) {
		if d.officialErr != nil {
			return nil, d.officialErr
		}
		return d.ResourceProviderServer.Read(ctx, req)
	}
	k := d.private(req.GetUrn())
	if k == nil {
		return nil, d.unknownKind(req.GetUrn())
	}
	in, err := unmarshal(req.GetInputs())
	if err != nil {
		return nil, err
	}
	importing := len(req.GetInputs().GetFields()) == 0 && len(req.GetProperties().GetFields()) == 0
	r, err := d.engine.Read(ctx, k, req.GetId(), in, importing)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return &pulumirpc.ReadResponse{}, nil // gone
	}
	props, err := marshal(r.Outputs)
	if err != nil {
		return nil, err
	}
	inputs, err := marshal(r.Inputs)
	if err != nil {
		return nil, err
	}
	return &pulumirpc.ReadResponse{Id: r.ID, Properties: props, Inputs: inputs}, nil
}

// Update merges the managed paths onto a fresh read and PUTs it.
func (d *Dispatcher) Update(ctx context.Context, req *pulumirpc.UpdateRequest) (*pulumirpc.UpdateResponse, error) {
	if sg := d.singleton(req.GetUrn()); sg != nil {
		return d.updateSingleton(ctx, sg, req)
	}
	if !d.isPrivateToken(req.GetUrn()) {
		if d.officialErr != nil {
			return nil, d.officialErr
		}
		return d.ResourceProviderServer.Update(ctx, req)
	}
	k := d.private(req.GetUrn())
	if k == nil {
		return nil, d.unknownKind(req.GetUrn())
	}
	if req.GetPreview() {
		return &pulumirpc.UpdateResponse{Properties: req.GetNews()}, nil
	}
	oldS := req.GetOldInputs()
	if oldS == nil {
		oldS = req.GetOlds()
	}
	olds, err := unmarshal(oldS)
	if err != nil {
		return nil, err
	}
	news, err := unmarshal(req.GetNews())
	if err != nil {
		return nil, err
	}
	out, err := d.engine.Update(ctx, k, req.GetId(), olds, news)
	if err != nil {
		return nil, err
	}
	props, err := marshal(out)
	if err != nil {
		return nil, err
	}
	return &pulumirpc.UpdateResponse{Properties: props}, nil
}

// Delete deletes a private object.
func (d *Dispatcher) Delete(ctx context.Context, req *pulumirpc.DeleteRequest) (*pbempty.Empty, error) {
	if sg := d.singleton(req.GetUrn()); sg != nil {
		d.ordering.Delete(sg, req.GetId())
		return &pbempty.Empty{}, nil
	}
	if !d.isPrivateToken(req.GetUrn()) {
		if d.officialErr != nil {
			return nil, d.officialErr
		}
		return d.ResourceProviderServer.Delete(ctx, req)
	}
	k := d.private(req.GetUrn())
	if k == nil {
		return nil, d.unknownKind(req.GetUrn())
	}
	if err := d.engine.Delete(ctx, k, req.GetId()); err != nil {
		return nil, err
	}
	return &pbempty.Empty{}, nil
}

// Invoke serves the official functions (no private functions in slice 1).
func (d *Dispatcher) Invoke(ctx context.Context, req *pulumirpc.InvokeRequest) (*pulumirpc.InvokeResponse, error) {
	if d.officialErr != nil {
		return nil, d.officialErr
	}
	return d.ResourceProviderServer.Invoke(ctx, req)
}
