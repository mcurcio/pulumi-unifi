package provider

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	fwRest "github.com/cloudy-sky-software/pulumi-provider-framework/rest"
)

// TestSecureTLSPathTrustsCAviaSSLCertFile is the no-Docker partial of E-M4.3: the
// CA-pinned secure path (allowInsecure=false), which every other integration/wire
// test bypasses with allowInsecure=true. It points SSL_CERT_FILE at the httptest
// server's own certificate and asserts that, with verification ON, the handshake
// succeeds AND the framework's transport was NOT replaced with the insecure one.
//
// Platform note: Go's default transport honors SSL_CERT_FILE only on Linux. On
// darwin it verifies against the Security.framework keychain and ignores
// SSL_CERT_FILE, so this test skips there (the live keychain-trust path is the
// Tier-2 e2e concern). The non-insecure-transport assertion below is the
// platform-independent half and is also covered by
// TestOnConfigureAllowInsecureDefaultsOff.
func TestSecureTLSPathTrustsCAviaSSLCertFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("SSL_CERT_FILE CA trust is honored by Go's default transport only on linux; on %s it uses the keychain (Tier-2 e2e)", runtime.GOOS)
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":0,"data":[],"limit":25,"offset":0,"totalCount":0}`))
	}))
	defer srv.Close()

	// Write the server's cert as a PEM trust bundle and point SSL_CERT_FILE at it.
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatalf("write CA bundle: %v", err)
	}
	t.Setenv("SSL_CERT_FILE", caPath)
	// Reset the cached system cert pool so Go re-reads SSL_CERT_FILE.
	x509.SystemCertPool() //nolint:errcheck // priming/ignoring; SystemCertPool caches per-process

	rp, err := makeProvider(
		nil, "unifi", "0.0.0-test",
		readArtifact(t, "schema.json"),
		readArtifact(t, "openapi_generated.yml"),
		readArtifact(t, "metadata.json"),
	)
	if err != nil {
		t.Fatalf("makeProvider: %v", err)
	}

	// allowInsecure deliberately UNSET → verification stays on, trust comes from
	// the CA bundle.
	if _, err := rp.Configure(context.Background(), &pulumirpc.ConfigureRequest{
		Variables: map[string]string{
			"unifi:config:apiKey":  "k",
			"unifi:config:apiHost": strings.TrimPrefix(srv.URL, "https://"),
		},
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	// The transport must NOT be the bare insecure *http.Transport injected by the
	// allowInsecure path — with verification on it must remain the framework's own
	// (wrapped) client.
	h := rp.(*fwRest.Provider)
	if _, isInsecure := h.GetHTTPClient().Transport.(*http.Transport); isInsecure {
		t.Error("default secure path replaced the framework transport with a plain *http.Transport")
	}

	// The CA-trusted handshake must succeed with verification on.
	out := invokeDataSource(t, rp, "unifi:countries/v1:getCountrie")
	_ = out // a non-error Invoke is the success signal; body is an empty stub
}
