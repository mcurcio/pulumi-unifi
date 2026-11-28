// Package netapp is the wire the Network application's two private APIs share
// (docs/design/private-binding.md §S2.2, L2): the Network root (the
// authenticated edge https://<apiHost>/proxy/network, or the networkBaseUrl
// override), the site name the private paths take, sending through the
// controller layer (TLS policy, X-API-Key, 429 retry), and the error and
// decoding rules common to v2 and classic. The v2 and classic bindings build on
// it; it knows no kinds' paths and no envelope.
package netapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/controller"
)

// Conn is where a private binding sends. The v2 and classic Binding types
// have exactly these fields, so each converts to a Conn.
type Conn struct {
	// BaseURL is the Network application root: https://<host>/proxy/network
	// through the UniFi OS edge, or http://127.0.0.1:8081 directly.
	BaseURL string
	// Site is the configured site: a name ("default") or the official UUID,
	// which Sites turns into the name the private paths take.
	Site string
	// Ctl is the controller access (TLS policy, X-API-Key, 429 retry); nil
	// uses a plain client with no key (tests, and the zero value).
	Ctl *controller.Client
	// Sites resolves a UUID Site to its name; nil uses Site as given.
	Sites *controller.SiteResolver
}

var plain = sync.OnceValue(func() *controller.Client { return controller.New(controller.Config{}) })

func (c Conn) controller() *controller.Client {
	if c.Ctl != nil {
		return c.Ctl
	}
	return plain()
}

// SiteName is the site name for private paths.
func (c Conn) SiteName(ctx context.Context, site string) (string, error) {
	if c.Sites == nil {
		return site, nil
	}
	return c.Sites.Name(ctx, site)
}

// URL is BaseURL + siteBase(the escaped site name) + rel, with {id} filled.
func (c Conn) URL(ctx context.Context, siteBase func(site string) string, rel, id string) (string, error) {
	site, err := c.SiteName(ctx, c.Site)
	if err != nil {
		return "", err
	}
	p := rel
	if id != "" {
		p = strings.ReplaceAll(p, "{id}", url.PathEscape(id))
	}
	return strings.TrimRight(c.BaseURL, "/") + siteBase(url.PathEscape(site)) + p, nil
}

// Send sends one request through the controller layer and returns the status
// and body, whatever the status.
func (c Conn) Send(ctx context.Context, method, u string, body any) (int, []byte, error) {
	if c.BaseURL == "" {
		return 0, nil, fmt.Errorf("the private API base URL is not set: set unifi:apiHost (the edge, https://<apiHost>/proxy/network, with unifi:apiKey) or unifi:networkBaseUrl")
	}
	return c.controller().Send(ctx, method, u, body)
}

// Decode decodes a 2xx body (numbers as json.Number); an empty body is nil.
func Decode(method, u string, raw []byte) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("%s %s: decoding the response: %w", method, u, err)
	}
	return v, nil
}

// HTTPError is a non-2xx answer. The body is kept (trimmed): the controller's
// validation messages name the field and rule.
type HTTPError struct {
	Method, URL string
	Status      int
	Body        string
}

// NewHTTPError builds the error for a non-2xx answer.
func NewHTTPError(method, u string, status int, raw []byte) *HTTPError {
	return &HTTPError{Method: method, URL: u, Status: status, Body: errBody(status, raw)}
}

func (e *HTTPError) Error() string {
	msg := fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, e.Body)
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		msg += " (the controller refused the request: through the UniFi OS edge the private API needs " +
			"unifi:apiKey, a key minted on this controller; without a key only a Network app reached " +
			"directly before its setup wizard (unifi:networkBaseUrl) answers)"
	}
	return msg
}

// errBody is the server's answer shortened for an error: an HTML page (the
// edge's error and login pages) is reduced to its status text.
func errBody(status int, raw []byte) string {
	msg := strings.TrimSpace(string(raw))
	if strings.HasPrefix(msg, "<") {
		return http.StatusText(status)
	}
	if len(msg) > 2000 {
		msg = msg[:2000] + "…"
	}
	return msg
}

// Objects reads a decoded answer as a list of objects.
func Objects(v any) []map[string]any {
	var out []map[string]any
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
	case map[string]any:
		// Some lists come wrapped ({"data": [...]}).
		if d, ok := t["data"].([]any); ok {
			return Objects(d)
		}
		out = append(out, t)
	}
	return out
}
