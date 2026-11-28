package provider

import (
	"net/http"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/controller"
)

// injectInsecureTransport replaces the client's transport with one that skips
// TLS verification: the controller layer's TLS policy (controller.Transport),
// which mirrors the framework's inner *http.Transport settings so only TLS
// verification changes. The framework builds its client with an unexported
// rateLimitTransport wrapper and exposes no transport setter, so we overwrite
// the whole Transport — losing the 429 retry wrapper. Acceptable for
// single-controller live testing against a self-signed cert; the clean fix is
// an upstream exported transport constructor/setter. The engine-served kinds
// use the controller layer's own client, which keeps 429 retry on both TLS
// paths.
func injectInsecureTransport(c *http.Client) {
	c.Transport = controller.Transport(true)
}
