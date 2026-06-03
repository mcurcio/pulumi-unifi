package provider

import (
	"context"
	"net/http"
	"testing"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	fwRest "github.com/cloudy-sky-software/pulumi-provider-framework/rest"
)

// newTestHandler builds a real framework provider from minimal in-memory bytes
// (just enough to construct the live *http.Client) and stores it on the
// provider's own handler field. This lets OnConfigure tests exercise
// handler-dependent behavior (transport injection) without the generated
// schema/metadata artifacts. The handler lives on the struct, not a package
// global, so there is nothing to save/restore — each test's provider is its own.
func newTestHandler(t *testing.T, p *unifiProvider) *fwRest.Provider {
	t.Helper()
	const openapiDoc = `{"openapi":"3.1.0","info":{"title":"t","version":"1"},"servers":[{"url":"https://localhost/proxy/network/integration"}],"paths":{}}`
	const schemaSpec = `{"name":"unifi"}`
	const metadata = `{}`
	rp, err := fwRest.MakeProvider(nil, "unifi", "0.0.0", []byte(schemaSpec), []byte(openapiDoc), []byte(metadata), p)
	if err != nil {
		t.Fatalf("MakeProvider: %v", err)
	}
	h := rp.(*fwRest.Provider)
	p.handler = h
	return h
}

// TestGetAuthorizationHeaderReturnsBareKey confirms the auth value is the raw API
// key with no scheme prefix — UniFi expects the bare key in the X-API-Key header.
func TestGetAuthorizationHeaderReturnsBareKey(t *testing.T) {
	p := &unifiProvider{apiKey: "secret-key-123"}
	if got := p.GetAuthorizationHeader(); got != "secret-key-123" {
		t.Errorf("GetAuthorizationHeader() = %q, want bare key %q", got, "secret-key-123")
	}
}

// TestGetGlobalPathParamsDefault confirms the default site ID is injected as the
// {siteId} path parameter.
func TestGetGlobalPathParamsDefault(t *testing.T) {
	p := &unifiProvider{siteID: defaultSiteID}
	params, err := p.GetGlobalPathParams(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetGlobalPathParams: %v", err)
	}
	if params["siteId"] != defaultSiteID {
		t.Errorf("siteId = %q, want %q", params["siteId"], defaultSiteID)
	}
	if defaultSiteID != "default" {
		t.Errorf("defaultSiteID must be %q for single-site controllers, got %q", "default", defaultSiteID)
	}
}

// TestGetGlobalPathParamsOverride confirms a configured site ID is propagated.
func TestGetGlobalPathParamsOverride(t *testing.T) {
	p := &unifiProvider{siteID: "site-abc"}
	params, err := p.GetGlobalPathParams(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetGlobalPathParams: %v", err)
	}
	if params["siteId"] != "site-abc" {
		t.Errorf("siteId = %q, want %q", params["siteId"], "site-abc")
	}
}

// TestOnConfigureRejectsURLApiHost confirms apiHost is validated as a bare
// host[:port]: a pasted URL (scheme or path) is rejected before the framework's
// raw `baseURL.Host = apiHost` can silently corrupt the base URL. handler is nil
// here, so the schema-driven env fallback is skipped and apiHost comes straight
// from Variables. apiKey is supplied so configure reaches the apiHost check.
func TestOnConfigureRejectsURLApiHost(t *testing.T) {
	tests := []struct {
		name    string
		apiHost string
		wantErr bool
	}{
		{"bare ip", "192.168.1.1", false},
		{"host with port", "unifi.example.com:443", false},
		{"empty defers to default", "", false},
		{"https url", "https://unifi.example.com", true},
		{"http url", "http://192.168.1.1:8443", true},
		{"trailing path", "unifi.example.com/proxy/network", true},
		{"non-http scheme", "tcp://192.168.1.1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &unifiProvider{name: "unifi", siteID: defaultSiteID}
			req := &pulumirpc.ConfigureRequest{
				Variables: map[string]string{
					"unifi:config:apiKey":  "test-key",
					"unifi:config:apiHost": tt.apiHost,
				},
			}
			_, err := p.OnConfigure(context.Background(), req)
			if tt.wantErr && err == nil {
				t.Errorf("OnConfigure(apiHost=%q) = nil error, want rejection", tt.apiHost)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("OnConfigure(apiHost=%q) = %v, want no error", tt.apiHost, err)
			}
		})
	}
}

// TestInjectInsecureTransport confirms the helper swaps in an *http.Transport
// with TLS verification disabled.
func TestInjectInsecureTransport(t *testing.T) {
	c := &http.Client{Transport: http.DefaultTransport}
	injectInsecureTransport(c)
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport", c.Transport)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true")
	}
}

// TestOnConfigureAllowInsecureDefaultsOff is the no-regression guard: with
// allowInsecure unset, OnConfigure must leave the framework's transport
// untouched (still the rate-limit wrapper, not a bare *http.Transport), so the
// SSL_CERT_FILE trust path the mock/e2e tiers depend on is preserved.
func TestOnConfigureAllowInsecureDefaultsOff(t *testing.T) {
	p := &unifiProvider{name: "unifi", siteID: defaultSiteID}
	h := newTestHandler(t, p)
	before := h.GetHTTPClient().Transport

	req := &pulumirpc.ConfigureRequest{
		Variables: map[string]string{"unifi:config:apiKey": "k"},
	}
	if _, err := p.OnConfigure(context.Background(), req); err != nil {
		t.Fatalf("OnConfigure: %v", err)
	}
	if p.allowInsecure {
		t.Error("allowInsecure = true, want false by default")
	}
	after := h.GetHTTPClient().Transport
	if _, isPlain := after.(*http.Transport); isPlain {
		t.Error("default path replaced the framework transport with a plain *http.Transport")
	}
	if before != after {
		t.Error("transport pointer changed despite allowInsecure=false")
	}
}

// TestOnConfigureAllowInsecureOn confirms allowInsecure=true (a Pulumi config
// bool arrives as the string "true") injects the insecure transport.
func TestOnConfigureAllowInsecureOn(t *testing.T) {
	p := &unifiProvider{name: "unifi", siteID: defaultSiteID}
	h := newTestHandler(t, p)

	req := &pulumirpc.ConfigureRequest{
		Variables: map[string]string{
			"unifi:config:apiKey":        "k",
			"unifi:config:allowInsecure": "true",
		},
	}
	if _, err := p.OnConfigure(context.Background(), req); err != nil {
		t.Fatalf("OnConfigure: %v", err)
	}
	if !p.allowInsecure {
		t.Fatal("allowInsecure = false, want true")
	}
	tr, ok := h.GetHTTPClient().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport after injection", h.GetHTTPClient().Transport)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true")
	}
}

// TestFirstEnv covers the env-var fallback used for config defaults: nil spec is
// empty, and the first set variable (in declaration order) wins.
func TestFirstEnv(t *testing.T) {
	if got := firstEnv(nil); got != "" {
		t.Errorf("firstEnv(nil) = %q, want empty", got)
	}

	def := &pschema.DefaultSpec{Environment: []string{"UNIFI_TEST_FIRST", "UNIFI_TEST_SECOND"}}

	if got := firstEnv(def); got != "" {
		t.Errorf("firstEnv with no env set = %q, want empty", got)
	}

	// Only the second is set -> it is returned.
	t.Setenv("UNIFI_TEST_SECOND", "value-2")
	if got := firstEnv(def); got != "value-2" {
		t.Errorf("firstEnv = %q, want %q", got, "value-2")
	}

	// First takes precedence when both are set.
	t.Setenv("UNIFI_TEST_FIRST", "value-1")
	if got := firstEnv(def); got != "value-1" {
		t.Errorf("firstEnv = %q, want first-declared %q", got, "value-1")
	}
}
