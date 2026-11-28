package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pbempty "github.com/golang/protobuf/ptypes/empty"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/binding/classic"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/internal/fakectl"
)

// fakeFramework records what reaches the official side.
type fakeFramework struct {
	pulumirpc.UnimplementedResourceProviderServer
	calls      []string
	configured *pulumirpc.ConfigureRequest
	checked    *pulumirpc.CheckRequest
}

func (f *fakeFramework) Configure(_ context.Context, r *pulumirpc.ConfigureRequest) (*pulumirpc.ConfigureResponse, error) {
	f.calls = append(f.calls, "Configure")
	f.configured = r
	return &pulumirpc.ConfigureResponse{AcceptSecrets: true}, nil
}

func (f *fakeFramework) CheckConfig(_ context.Context, r *pulumirpc.CheckRequest) (*pulumirpc.CheckResponse, error) {
	f.calls = append(f.calls, "CheckConfig")
	f.checked = r
	return &pulumirpc.CheckResponse{Inputs: r.GetNews()}, nil
}

func (f *fakeFramework) Create(context.Context, *pulumirpc.CreateRequest) (*pulumirpc.CreateResponse, error) {
	f.calls = append(f.calls, "Create")
	return &pulumirpc.CreateResponse{Id: "official-1"}, nil
}

func (f *fakeFramework) Delete(context.Context, *pulumirpc.DeleteRequest) (*pbempty.Empty, error) {
	f.calls = append(f.calls, "Delete")
	return &pbempty.Empty{}, nil
}

func (f *fakeFramework) GetPluginInfo(context.Context, *pbempty.Empty) (*pulumirpc.PluginInfo, error) {
	return &pulumirpc.PluginInfo{Version: "9.9.9"}, nil
}

const (
	thingURN    = "urn:pulumi:dev::proj::unifi:network:Thing::t1"
	officialURN = "urn:pulumi:dev::proj::unifi:sites/v1:FirewallZone::z1"
	missingURN  = "urn:pulumi:dev::proj::unifi:network:Nope::n1"
)

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := plugin.MarshalProperties(resource.NewPropertyMapFromMap(m), plugin.MarshalOptions{KeepSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newDispatcher(t *testing.T) (*Dispatcher, *fakeFramework, *fakectl.Server) {
	t.Helper()
	srv := fakectl.Start("default")
	t.Cleanup(srv.Close)
	fw := &fakeFramework{}
	d, err := NewDispatcher(fw, "unifi", []byte(`{"name":"unifi"}`), fakectl.SurfaceJSON())
	if err != nil {
		t.Fatal(err)
	}
	return d, fw, srv
}

func TestDispatcherConfigAndRouting(t *testing.T) {
	ctx := context.Background()
	d, fw, srv := newDispatcher(t)
	t.Setenv("UNIFI_APIKEY", "")
	t.Setenv("UNIFI_API_HOST", "")
	t.Setenv("UNIFI_NETWORK_BASE_URL", "")

	if s, _ := d.GetSchema(ctx, &pulumirpc.GetSchemaRequest{}); s.Schema != `{"name":"unifi"}` {
		t.Errorf("GetSchema must return the merged schema, got %s", s.Schema)
	}
	if info, _ := d.GetPluginInfo(ctx, &pbempty.Empty{}); info.Version != "9.9.9" {
		t.Error("unrouted calls go to the framework")
	}

	// CheckConfig: networkBaseUrl never reaches the framework, but is kept.
	resp, err := d.CheckConfig(ctx, &pulumirpc.CheckRequest{News: mustStruct(t, map[string]any{"networkBaseUrl": srv.URL, "siteId": "default"})})
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := fw.checked.GetNews().GetFields()["networkBaseUrl"]; leaked {
		t.Error("networkBaseUrl forwarded to the framework's CheckConfig")
	}
	if resp.GetInputs().GetFields()["networkBaseUrl"].GetStringValue() != srv.URL || len(resp.Failures) != 0 {
		t.Errorf("networkBaseUrl not kept: %v", resp)
	}
	bad, _ := d.CheckConfig(ctx, &pulumirpc.CheckRequest{News: mustStruct(t, map[string]any{"networkBaseUrl": "127.0.0.1:8081"})})
	if len(bad.Failures) != 1 {
		t.Error("a networkBaseUrl without a scheme must fail CheckConfig")
	}

	// Configure without an apiKey: the private side works, the official side
	// is not configured and says why.
	if _, err := d.Configure(ctx, &pulumirpc.ConfigureRequest{Variables: map[string]string{"unifi:config:networkBaseUrl": srv.URL + "/"}}); err != nil {
		t.Fatal(err)
	}
	if fw.configured != nil {
		t.Error("the framework must not be configured without an apiKey when private kinds are served")
	}
	if d.v2.BaseURL != srv.URL || d.v2.Site != "default" || d.v2.Ctl.HasKey() || *d.classic != v2Conn(d) {
		t.Errorf("private bindings %+v %+v", d.v2, d.classic)
	}
	if _, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: officialURN}); err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Errorf("official call without a key: %v", err)
	}

	// Private lifecycle through the gRPC surface.
	in := mustStruct(t, map[string]any{"name": "t1", "mode": "SLOW"})
	chk, err := d.Check(ctx, &pulumirpc.CheckRequest{Urn: thingURN, News: in})
	if err != nil || len(chk.Failures) != 0 {
		t.Fatalf("check: %v %v", chk, err)
	}
	cr, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: thingURN, Properties: in})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cr.Id, "private:") || srv.Count("things") != 1 {
		t.Fatalf("create: %v", cr)
	}
	rr, err := d.Read(ctx, &pulumirpc.ReadRequest{Urn: thingURN, Id: cr.Id, Inputs: in, Properties: cr.Properties})
	if err != nil || rr.Id != cr.Id || len(rr.Inputs.GetFields()) != 2 {
		t.Fatalf("read: %v %v", rr, err)
	}
	// A declared secret input stays secret through a refresh (the engine
	// does not re-apply secretness for a provider that accepts secrets).
	sec := mustStruct(t, map[string]any{"name": "t1", "mode": "SLOW", "psk": "s3cret"})
	sr, err := d.Read(ctx, &pulumirpc.ReadRequest{Urn: thingURN, Id: cr.Id, Inputs: sec, Properties: cr.Properties})
	if err != nil {
		t.Fatal(err)
	}
	for name, st := range map[string]*structpb.Struct{"inputs": sr.Inputs, "properties": sr.Properties} {
		got, err := plugin.UnmarshalProperties(st, marshalOpts)
		if err != nil {
			t.Fatal(err)
		}
		if v, ok := got["psk"]; !ok || !v.IsSecret() {
			t.Errorf("%s: psk must stay a secret after a refresh: %v", name, got["psk"])
		}
	}
	news := mustStruct(t, map[string]any{"name": "t1", "mode": "FAST"})
	df, err := d.Diff(ctx, &pulumirpc.DiffRequest{Urn: thingURN, Id: cr.Id, OldInputs: in, News: news})
	if err != nil || df.Changes != pulumirpc.DiffResponse_DIFF_SOME || df.DetailedDiff["mode"].Kind != pulumirpc.PropertyDiff_UPDATE {
		t.Fatalf("diff: %v %v", df, err)
	}
	codeChange := mustStruct(t, map[string]any{"name": "t1", "mode": "SLOW", "code": "ABC"})
	df, _ = d.Diff(ctx, &pulumirpc.DiffRequest{Urn: thingURN, Id: cr.Id, OldInputs: in, News: codeChange})
	if df.DetailedDiff["code"].Kind != pulumirpc.PropertyDiff_ADD_REPLACE || len(df.Replaces) != 1 {
		t.Errorf("createOnly diff: %v", df)
	}
	if _, err := d.Update(ctx, &pulumirpc.UpdateRequest{Urn: thingURN, Id: cr.Id, OldInputs: in, News: news}); err != nil {
		t.Fatal(err)
	}
	if got := srv.Object("things", strings.TrimPrefix(cr.Id, "private:")); got["mode"] != "FAST" {
		t.Errorf("after update %v", got)
	}
	// Import by natural key (no inputs, no properties).
	imp, err := d.Read(ctx, &pulumirpc.ReadRequest{Urn: thingURN, Id: "t1"})
	if err != nil || imp.Id != cr.Id {
		t.Fatalf("import: %v %v", imp, err)
	}
	if _, err := d.Delete(ctx, &pulumirpc.DeleteRequest{Urn: thingURN, Id: cr.Id}); err != nil {
		t.Fatal(err)
	}
	if srv.Count("things") != 0 {
		t.Error("not deleted")
	}
	gone, err := d.Read(ctx, &pulumirpc.ReadRequest{Urn: thingURN, Id: cr.Id, Inputs: in})
	if err != nil || gone.Id != "" {
		t.Errorf("a gone object reads as empty: %v %v", gone, err)
	}
	if _, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: missingURN}); err == nil || !strings.Contains(err.Error(), "PRIVATE=1") {
		t.Errorf("an unserved private token: %v", err)
	}
	// Preview never calls the controller.
	srv.Requests()
	if _, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: thingURN, Properties: in, Preview: true}); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("preview made %d requests", n)
	}

	// With an apiKey, the framework is configured without networkBaseUrl, and
	// official calls go through.
	if _, err := d.Configure(ctx, &pulumirpc.ConfigureRequest{Variables: map[string]string{
		"unifi:config:apiKey": "test-key-not-real", "unifi:config:networkBaseUrl": srv.URL, "unifi:config:siteId": "lab"}}); err != nil {
		t.Fatal(err)
	}
	if _, leaked := fw.configured.GetVariables()["unifi:config:networkBaseUrl"]; leaked || fw.configured.GetVariables()["unifi:config:apiKey"] == "" {
		t.Errorf("framework config %v", fw.configured.GetVariables())
	}
	if !d.v2.Ctl.HasKey() || d.v2.Site != "lab" || *d.classic != v2Conn(d) {
		t.Errorf("private bindings %+v %+v", d.v2, d.classic)
	}
	if cr, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: officialURN}); err != nil || cr.Id != "official-1" {
		t.Errorf("official create: %v %v", cr, err)
	}
}

// networkBaseUrl defaults to the UniFi OS edge on apiHost.
func TestDispatcherDefaultBaseURL(t *testing.T) {
	d, _, _ := newDispatcher(t)
	t.Setenv("UNIFI_NETWORK_BASE_URL", "")
	if _, err := d.Configure(context.Background(), &pulumirpc.ConfigureRequest{Variables: map[string]string{
		"unifi:config:apiHost": "192.0.2.1", "unifi:config:apiKey": "k"}}); err != nil {
		t.Fatal(err)
	}
	if got := d.v2.BaseURL; got != "https://192.0.2.1/proxy/network" {
		t.Errorf("base URL %s", got)
	}
}

// v2Conn is the v2 binding's connection as a classic binding: both private
// bindings share one controller client, site and resolver.
func v2Conn(d *Dispatcher) classic.Binding {
	return classic.Binding{BaseURL: d.v2.BaseURL, Site: d.v2.Site, Ctl: d.v2.Ctl, Sites: d.v2.Sites}
}

// With no private kinds (PRIVATE=0: an empty surface, or only the official
// engine-served kinds) the dispatcher forwards Configure as is, so a missing
// apiKey fails exactly as before (in the framework callback).
func TestDispatcherNoPrivateKindsForwardsEverything(t *testing.T) {
	for name, surface := range map[string]string{"empty": `{"source":{},"kinds":[]}`, "PRIVATE=0": orderingSurface} {
		fw := &fakeFramework{}
		d, err := NewDispatcher(fw, "unifi", []byte(`{}`), []byte(surface))
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("UNIFI_APIKEY", "")
		if _, err := d.Configure(context.Background(), &pulumirpc.ConfigureRequest{Variables: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
		if fw.configured == nil {
			t.Errorf("%s: Configure not forwarded", name)
		}
		if _, err := d.Create(context.Background(), &pulumirpc.CreateRequest{Urn: officialURN}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := d.Create(context.Background(), &pulumirpc.CreateRequest{Urn: missingURN}); err == nil || !strings.Contains(err.Error(), "private kinds: []") {
			t.Errorf("%s: an unserved private token: %v", name, err)
		}
	}
}

// One surface, one engine: the private kinds and the official engine-served
// kind route to the engine side by side; their stored ids come from their
// bindings (private:<id>, official:<site id>).
func TestDispatcherRoutesEveryEngineKindByTheSurface(t *testing.T) {
	var mixed map[string]any
	if err := json.Unmarshal(fakectl.SurfaceJSON(), &mixed); err != nil {
		t.Fatal(err)
	}
	var ord map[string]any
	if err := json.Unmarshal([]byte(orderingSurface), &ord); err != nil {
		t.Fatal(err)
	}
	mixed["kinds"] = append(mixed["kinds"].([]any), ord["kinds"].([]any)...)
	b, _ := json.Marshal(mixed)
	d, err := NewDispatcher(&fakeFramework{}, "unifi", []byte(`{}`), b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{fakectl.ThingToken, fakectl.CrewToken, "unifi:sites/v1:AclRuleOrdering"} {
		k := d.served("urn:pulumi:dev::proj::" + tok + "::x")
		if k == nil {
			t.Fatalf("%s is not routed to the engine", tok)
		}
		want := "private:x"
		if !k.IsPrivate() {
			want = "official:x"
		}
		if got := d.engine.StoredID(k, "x"); got != want {
			t.Errorf("%s: stored id %s, want %s", tok, got, want)
		}
	}
	if got := d.Surface().PrivateTokens(); len(got) != 5 {
		t.Errorf("private tokens %v", got)
	}
}
