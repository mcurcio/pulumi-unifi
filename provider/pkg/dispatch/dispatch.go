// Package dispatch is the routing layer (docs/design/private-binding.md §S2.2,
// L5): it wraps the framework's ResourceProviderServer, sends every token the
// surface (surface.json) lists to the one engine, refuses an unknown private
// token, and forwards everything else to the framework unchanged. It
// translates gRPC to the engine's lifecycle and back, builds the bindings from
// the provider configuration (one controller client: TLS policy, X-API-Key,
// 429 retry), and puts the version guard in front of the private ones. It
// holds no lifecycle and no wire format.
package dispatch

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

	"github.com/mcurcio/pulumi-unifi/provider/pkg/binding/classic"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/binding/official"
	v2 "github.com/mcurcio/pulumi-unifi/provider/pkg/binding/v2"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/controller"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
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

// Dispatcher wraps the framework's ResourceProviderServer (design §2,
// §S2.2): a token the surface lists goes to the engine, any other
// unifi:network:* token is refused, everything else goes to the framework,
// unchanged.
type Dispatcher struct {
	pulumirpc.ResourceProviderServer // the framework (official resources)

	name    string
	schema  []byte
	surface *engine.Surface
	engine  *engine.Engine
	// site is the configured siteId (a name or the official UUID).
	site string
	// v2 and classic are the private bindings, behind guard; kept for tests.
	v2      *v2.Binding
	classic *classic.Binding
	guard   *versionGuard

	// officialErr is set when the official side was not configured (no
	// apiKey while private kinds are served); framework calls return it.
	officialErr error
}

// NewDispatcher wraps fw. mergedSchema is returned from GetSchema; surface is
// surface.json. With no engine-served kinds the dispatcher only swaps the
// schema bytes (equal to the golden at PRIVATE=0) and forwards everything.
func NewDispatcher(fw pulumirpc.ResourceProviderServer, name string, mergedSchema, surface []byte) (*Dispatcher, error) {
	s, err := engine.ParseSurface(surface)
	if err != nil {
		return nil, err
	}
	d := &Dispatcher{ResourceProviderServer: fw, name: name, schema: mergedSchema, surface: s}
	d.engine = &engine.Engine{Surface: s,
		Site: func() string { return d.site },
		// A singleton's release (§S2.5, §S2.8); to the plugin log
		// (private-binding.md §S2.14 row 7).
		Warn: func(msg string) { logging.Warningf("%s", msg) }}
	d.configureBindings(controller.Config{}, "default")
	return d, nil
}

// configureBindings builds the one controller client every engine-served kind
// shares (TLS policy, X-API-Key, 429 retry; docs/design/private-binding.md
// §S2.2 L1, §S2.6) and a binding per API over it: official on the
// Integration API root, v2 and classic on the Network root (the authenticated
// edge https://<apiHost>/proxy/network by default, or the networkBaseUrl
// override), the private two behind one version guard. A new Configure
// re-arms the guard.
func (d *Dispatcher) configureBindings(cfg controller.Config, site string) {
	ctl := controller.New(cfg)
	root, err := ctl.NetworkRoot()
	if err != nil {
		root = "" // the binding reports what to set on the first call
	}
	sites := &controller.SiteResolver{C: ctl}
	d.site = site
	d.v2 = &v2.Binding{BaseURL: root, Site: site, Ctl: ctl, Sites: sites}
	d.classic = &classic.Binding{BaseURL: root, Site: site, Ctl: ctl, Sites: sites}
	d.guard = &versionGuard{want: d.surface.Source.NetworkVersion, read: d.classic.NetworkVersion}
	d.engine.Bindings = map[string]engine.Binding{
		engine.APIOfficial: official.New(ctl),
		engine.APIV2:       d.guard.wrap(d.v2),
		engine.APIClassic:  d.guard.wrap(d.classic),
	}
}

// Surface returns the engine-served kinds.
func (d *Dispatcher) Surface() *engine.Surface { return d.surface }

// served returns the surface kind the URN's type names, or nil.
func (d *Dispatcher) served(urn string) *engine.Kind {
	return d.surface.Find(string(resource.URN(urn).Type()))
}

func (d *Dispatcher) isPrivateToken(urn string) bool {
	return strings.HasPrefix(string(resource.URN(urn).Type()), engine.PrivateTokenPrefix)
}

// route returns the engine kind the URN's type names; nil with a nil error
// means the framework serves it.
func (d *Dispatcher) route(urn string) (*engine.Kind, error) {
	if k := d.served(urn); k != nil {
		return k, nil
	}
	if d.isPrivateToken(urn) {
		return nil, d.unknownKind(urn)
	}
	return nil, d.officialErr
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

// Configure configures the bindings, then the framework with networkBaseUrl
// removed.
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
	d.configureBindings(controller.Config{APIHost: get("apiHost"), APIKey: apiKey, AllowInsecure: insecure,
		NetworkBaseURL: get(keyBaseURL)}, site)
	private := len(d.surface.Private())
	if private > 0 {
		logging.V(3).Infof("private API: %d kinds, built for Network %s; base %q, site %q, api key set: %v",
			private, d.surface.Source.NetworkVersion, d.v2.BaseURL, site, apiKey != "")
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
	if apiKey == "" && private > 0 {
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
		resource.URN(urn).Type(), d.surface.PrivateTokens())
}

// Check validates an engine-served resource's inputs and fills the defaults
// the engine owns.
func (d *Dispatcher) Check(ctx context.Context, req *pulumirpc.CheckRequest) (*pulumirpc.CheckResponse, error) {
	k, err := d.route(req.GetUrn())
	if err != nil {
		return nil, err
	}
	if k == nil {
		return d.ResourceProviderServer.Check(ctx, req)
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
	if filled, changed := d.engine.Defaults(k, news); changed {
		if news = filled; news == nil {
			news = resource.PropertyMap{}
		}
		if resp.Inputs, err = marshal(news); err != nil {
			return nil, err
		}
	}
	for _, f := range d.engine.Check(k, olds, news) {
		resp.Failures = append(resp.Failures, &pulumirpc.CheckFailure{Property: f.Property, Reason: f.Reason})
	}
	return resp, nil
}

// Diff compares managed paths.
func (d *Dispatcher) Diff(ctx context.Context, req *pulumirpc.DiffRequest) (*pulumirpc.DiffResponse, error) {
	k, err := d.route(req.GetUrn())
	if err != nil {
		return nil, err
	}
	if k == nil {
		return d.ResourceProviderServer.Diff(ctx, req)
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

// Create creates (a singleton: adopts) an engine-served object.
func (d *Dispatcher) Create(ctx context.Context, req *pulumirpc.CreateRequest) (*pulumirpc.CreateResponse, error) {
	k, err := d.route(req.GetUrn())
	if err != nil {
		return nil, err
	}
	if k == nil {
		return d.ResourceProviderServer.Create(ctx, req)
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

// Read refreshes or imports an engine-served object.
func (d *Dispatcher) Read(ctx context.Context, req *pulumirpc.ReadRequest) (*pulumirpc.ReadResponse, error) {
	k, err := d.route(req.GetUrn())
	if err != nil {
		return nil, err
	}
	if k == nil {
		return d.ResourceProviderServer.Read(ctx, req)
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

// Update writes an engine-served object (the managed paths merged onto a fresh read).
func (d *Dispatcher) Update(ctx context.Context, req *pulumirpc.UpdateRequest) (*pulumirpc.UpdateResponse, error) {
	k, err := d.route(req.GetUrn())
	if err != nil {
		return nil, err
	}
	if k == nil {
		return d.ResourceProviderServer.Update(ctx, req)
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

// Delete deletes (a singleton: releases) an engine-served object.
func (d *Dispatcher) Delete(ctx context.Context, req *pulumirpc.DeleteRequest) (*pbempty.Empty, error) {
	k, err := d.route(req.GetUrn())
	if err != nil {
		return nil, err
	}
	if k == nil {
		return d.ResourceProviderServer.Delete(ctx, req)
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
