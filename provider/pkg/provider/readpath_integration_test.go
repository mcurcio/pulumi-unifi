//go:build integration

// Read-path integration test. Excluded from the default `make test` build (it
// needs a running mock); run with `make test-mock`, which brings up the Prism
// TLS mock and sets the env gates below.
//
// What it proves end-to-end through the real provider + framework code:
//   - Configure wires apiKey + apiHost from Pulumi config.
//   - A data-source Invoke composes the absolute URL
//     https://<host>/proxy/network/integration/v1/... from the generated server
//     URL + the crudMap read endpoint, over real TLS — the mock's self-signed cert
//     is accepted via allowInsecure=true (the A3 flag; see the Configure call).
//   - The JSON response decodes into a Pulumi property map.
//
// Header-name correctness (X-API-Key) and the bare-key value are covered by the
// unit tests (TestFixOpenAPIDocInjectsAuth, TestGetAuthorizationHeaderReturnsBareKey).
package provider

import (
	"context"
	"os"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// mockReadDataSource is the data-source token exercised against the mock. Its
// read endpoint (/v1/countries) takes no path params beyond the server base, so
// it isolates URL composition + TLS + decode without siteId concerns.
const mockReadDataSource = "unifi:countries/v1:getCountry"

// mockSiteScopedDataSource is a site-scoped page DTO. Reading it through the
// mock proves two things the countries control cannot: the {siteId} path
// parameter is substituted over the wire (a successful decode means
// /v1/sites/<mockSiteID>/wifi/broadcasts matched a Prism route — an unsubstituted
// {siteId} would 404), and the page envelope flows through OnPostInvoke (A4).
const mockSiteScopedDataSource = "unifi:sites/v1:listWifiBroadcasts"

// mockSiteID is the {siteId} the mock tier configures. The spec types siteId as
// format:uuid and Prism enforces it on the path param, so the provider's real
// default ("default" — a UniFi magic alias the spec doesn't encode) 422s against
// the mock. The tests pin a spec-valid UUID instead; substitution is still proven
// (a 200 means this value reached {siteId} and matched the declared format). The
// "default" defaulting itself is covered by the provider config unit tests.
const mockSiteID = "00000000-0000-0000-0000-000000000000"

func TestReadPathAgainstMock(t *testing.T) {
	apiHost := os.Getenv("UNIFI_MOCK_ADDR")
	if apiHost == "" {
		t.Skip("UNIFI_MOCK_ADDR not set; run via `make test-mock`")
	}

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
	// allowInsecure=true: the mock serves a self-signed cert via Caddy. The
	// framework's default transport verifies against the OS trust store, which on
	// darwin is the Security.framework keychain and ignores SSL_CERT_FILE — so the
	// mock tier trusts the cert through the real product knob (the A3 flag),
	// against live TLS. The CA-pinned secure path is validated in the Tier-2 live
	// test (test/e2e/).
	if _, err := rp.Configure(ctx, &pulumirpc.ConfigureRequest{
		Variables: map[string]string{
			"unifi:config:apiKey":        "mock-api-key",
			"unifi:config:apiHost":       apiHost,
			"unifi:config:siteId":        mockSiteID,
			"unifi:config:allowInsecure": "true",
		},
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	readThroughMock := func(t *testing.T, tok string) resource.PropertyMap {
		t.Helper()
		args, err := plugin.MarshalProperties(resource.PropertyMap{}, plugin.MarshalOptions{})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		resp, err := rp.Invoke(ctx, &pulumirpc.InvokeRequest{Tok: tok, Args: args})
		if err != nil {
			t.Fatalf("Invoke(%s): %v", tok, err)
		}
		if len(resp.GetFailures()) > 0 {
			t.Fatalf("Invoke(%s) returned failures: %v", tok, resp.GetFailures())
		}
		out, err := plugin.UnmarshalProperties(resp.GetReturn(), plugin.MarshalOptions{})
		if err != nil {
			t.Fatalf("unmarshal return: %v", err)
		}
		return out
	}

	// Control: a top-level read with no path params — isolates URL composition,
	// TLS, and decode from any siteId concern.
	t.Run("countries control", func(t *testing.T) {
		if out := readThroughMock(t, mockReadDataSource); len(out) == 0 {
			t.Errorf("expected a non-empty decoded response from %s", mockReadDataSource)
		}
	})

	// Site-scoped read: a successful decode proves the framework substituted the
	// configured siteId into {siteId} (an unsubstituted path would not match a
	// Prism route, and a non-uuid value would 422 on the spec's format:uuid check)
	// and that the page envelope passed through OnPostInvoke (A4).
	t.Run("siteId substitution", func(t *testing.T) {
		if out := readThroughMock(t, mockSiteScopedDataSource); len(out) == 0 {
			t.Errorf("expected a non-empty decoded response from %s", mockSiteScopedDataSource)
		}
	})
}
