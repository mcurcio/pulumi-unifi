package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// readArtifact loads a generated provider artifact (schema.json, metadata.json,
// openapi_generated.yml) from the cmd build directory. These are gitignored
// build outputs; when absent the caller is skipped rather than failed, so a bare
// `go test ./pkg/provider/` without a prior build is graceful while `make test`
// and `make test-mock` (which build first) run for real. Defined in this
// untagged file so both the default-gate wire test and the integration
// read/write tests share it.
func readArtifact(t *testing.T, name string) []byte {
	t.Helper()
	p := filepath.Join("..", "..", "cmd", "pulumi-resource-unifi", name)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		t.Skipf("generated artifact %s not found (run `make build` first)", name)
	}
	if err != nil {
		t.Fatalf("read artifact %s: %v", name, err)
	}
	return b
}

// TestWirePath asserts what the provider actually puts on the wire, against an
// in-process httptest TLS server — no Docker, so it runs in the default `make
// test` gate. It proves two things the Prism mock structurally cannot:
//   - the bare API key is sent in the X-API-Key header (Prism does not enforce
//     auth, so a missing/wrong header would pass there);
//   - siteId=default is substituted into the {siteId} path segment.
//
// allowInsecure=true lets the provider trust httptest's self-signed cert, which
// also exercises A3's insecure transport against a real TLS handshake.
func TestWirePath(t *testing.T) {
	const apiKey = "wire-secret-key"

	var mu sync.Mutex
	var gotKey, gotPath string

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotKey, gotPath = r.Header.Get("X-API-Key"), r.URL.Path
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		// A complete (empty) page envelope: OnPostInvoke sees data+totalCount,
		// finds the set already complete (0 >= 0) and issues no follow-up GET,
		// returning a map — so the framework never reaches its non-:list
		// type-assert to map[string]any. The body is just a stub to keep
		// Invoke from erroring; this test asserts on the request, not the result.
		_, _ = w.Write([]byte(`{"count":0,"data":[],"limit":25,"offset":0,"totalCount":0}`))
	}))
	defer srv.Close()

	// The framework swaps only the host of the generated server URL; apiHost is a
	// bare host:port (no scheme), which is exactly what OnConfigure requires.
	apiHost := strings.TrimPrefix(srv.URL, "https://")

	// makeProvider stores the framework handle on the provider struct (no package
	// globals), so each test's provider is self-contained — nothing to restore.
	rp, err := makeProvider(
		nil, "unifi", "0.0.0-test",
		readArtifact(t, "schema.json"),
		readArtifact(t, "openapi_generated.yml"),
		readArtifact(t, "metadata.json"),
	)
	if err != nil {
		t.Fatalf("makeProvider: %v", err)
	}

	ctx := context.Background()
	if _, err := rp.Configure(ctx, &pulumirpc.ConfigureRequest{
		Variables: map[string]string{
			"unifi:config:apiKey":        apiKey,
			"unifi:config:apiHost":       apiHost,
			"unifi:config:allowInsecure": "true",
		},
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	invoke := func(t *testing.T, tok string) {
		t.Helper()
		args, err := plugin.MarshalProperties(resource.PropertyMap{}, plugin.MarshalOptions{})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		resp, err := rp.Invoke(ctx, &pulumirpc.InvokeRequest{Tok: tok, Args: args})
		if err != nil {
			t.Fatalf("Invoke(%s): %v", tok, err)
		}
		if fs := resp.GetFailures(); len(fs) > 0 {
			t.Fatalf("Invoke(%s) failures: %v", tok, fs)
		}
	}

	t.Run("x-api-key on the wire", func(t *testing.T) {
		invoke(t, "unifi:countries/v1:getCountry")
		mu.Lock()
		defer mu.Unlock()
		if gotKey != apiKey {
			t.Errorf("X-API-Key on the wire = %q, want bare key %q", gotKey, apiKey)
		}
	})

	t.Run("siteId substituted on the wire", func(t *testing.T) {
		invoke(t, "unifi:sites/v1:listWifiBroadcasts")
		mu.Lock()
		defer mu.Unlock()
		const wantSuffix = "/v1/sites/default/wifi/broadcasts"
		if !strings.HasSuffix(gotPath, wantSuffix) {
			t.Errorf("request path = %q, want suffix %q (siteId=default substituted)", gotPath, wantSuffix)
		}
		if strings.Contains(gotPath, "{") {
			t.Errorf("request path %q still contains an unsubstituted {param}", gotPath)
		}
	})
}

// TestWirePathResourceSiteIDOverride is the D-M3.2 regression guard for the
// per-resource siteId override. It is the in-process counterpart of TestWirePath:
// an httptest TLS server, no Docker, so it runs in the default `make test` gate.
//
// It proves the framework's getPathParamsMap resolves {siteId} from the RESOURCE's
// own inputs first, falling back to the provider-global value only when absent (the
// code comment: "we look for this after checking the resource state, so it can be
// overridden at a resource level"). The provider is configured with the global
// siteId="default", but a FirewallZone is Created with a resource-level
// siteId="site-b" — the POST must land on /sites/site-b/, NOT /sites/default/. This
// is the guard that the override stays honored across framework bumps.
func TestWirePathResourceSiteIDOverride(t *testing.T) {
	const apiKey = "wire-secret-key"
	const globalSite = "default"
	const resourceSite = "site-b"

	var mu sync.Mutex
	var gotPath string

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		// A minimal create response: the framework extracts `id` from the body to
		// build the CreateResponse. The values are irrelevant — this test asserts on
		// the request path, not the parsed result.
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"zone-1","name":"wire-zone"}`))
	}))
	defer srv.Close()

	apiHost := strings.TrimPrefix(srv.URL, "https://")

	rp, err := makeProvider(
		nil, "unifi", "0.0.0-test",
		readArtifact(t, "schema.json"),
		readArtifact(t, "openapi_generated.yml"),
		readArtifact(t, "metadata.json"),
	)
	if err != nil {
		t.Fatalf("makeProvider: %v", err)
	}

	ctx := context.Background()
	if _, err := rp.Configure(ctx, &pulumirpc.ConfigureRequest{
		Variables: map[string]string{
			"unifi:config:apiKey":        apiKey,
			"unifi:config:apiHost":       apiHost,
			"unifi:config:siteId":        globalSite, // provider-global site
			"unifi:config:allowInsecure": "true",
		},
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	// FirewallZone's create path is /v1/sites/{siteId}/firewall/zones. The resource
	// carries a siteId input (D-M3.2); setting it to site-b must win over the global
	// "default". The framework validates the request body against the spec before
	// sending, so the body must be schema-valid: name + networkIds (a uuid array).
	const tok = "unifi:sites/v1:FirewallZone"
	const urn = "urn:pulumi:test::test::" + tok + "::wire-zone"
	const exampleNetworkID = "dfb21062-8ea0-4dca-b1d8-1eb3da00e58b" // spec's example network uuid
	props, err := plugin.MarshalProperties(resource.PropertyMap{
		"name": resource.NewStringProperty("wire-zone"),
		"networkIds": resource.NewArrayProperty([]resource.PropertyValue{
			resource.NewStringProperty(exampleNetworkID),
		}),
		"siteId": resource.NewStringProperty(resourceSite),
	}, plugin.MarshalOptions{})
	if err != nil {
		t.Fatalf("marshal props: %v", err)
	}

	if _, err := rp.Create(ctx, &pulumirpc.CreateRequest{Urn: urn, Properties: props}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	wantSuffix := "/v1/sites/" + resourceSite + "/firewall/zones"
	if !strings.HasSuffix(gotPath, wantSuffix) {
		t.Errorf("request path = %q, want suffix %q (resource-level siteId=%q must override global %q)", gotPath, wantSuffix, resourceSite, globalSite)
	}
	if strings.Contains(gotPath, "/sites/"+globalSite+"/") {
		t.Errorf("request path = %q used the provider-global site %q; the resource-level siteId override was NOT honored", gotPath, globalSite)
	}
}
