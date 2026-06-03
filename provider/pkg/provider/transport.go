package provider

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// injectInsecureTransport replaces the client's transport with one that skips
// TLS verification. The framework builds its client with an unexported
// rateLimitTransport wrapper and exposes no transport setter, so we overwrite
// the whole Transport — losing the 429 retry wrapper. We mirror the framework's
// inner *http.Transport settings (rest/provider.go) so only TLS verification
// changes. Acceptable for single-controller live testing against a self-signed
// cert; the clean fix is an upstream exported transport constructor/setter.
func injectInsecureTransport(c *http.Client) {
	c.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- opt-in allowInsecure for self-signed controller certs
	}
}
