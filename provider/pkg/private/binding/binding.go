// Package binding is the HTTP binding of the private API
// (docs/design/private-binding.md §2, §7, §S2.6, §S2.8): v2 paths under
// <network root>/v2/api/site/{name}/ and classic paths under
// <network root>/api/s/{name}/ with their {"meta":…,"data":[…]} envelope and
// wire semantics (create answers 200 with data:[obj]; a GET answering data:[]
// is gone; an rc:"error" body is a ClassicError; a DELETE answering
// api.err.IdInvalid is gone only once a list confirms it). The network root is the authenticated edge
// (https://<apiHost>/proxy/network, the same apiKey as the official API) or the
// networkBaseUrl override (the Network app directly, before the wizard). The
// wire itself (TLS policy, X-API-Key, 429 retry) is the controller layer's; this
// package maps paths and statuses to engine outcomes. It implements
// engine.Binding.
package binding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/controller"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
)

// Client calls the private API.
type Client struct {
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

	once sync.Once
	ctl  *controller.Client
}

func (c *Client) controller() *controller.Client {
	if c.Ctl != nil {
		return c.Ctl
	}
	c.once.Do(func() { c.ctl = controller.New(controller.Config{}) })
	return c.ctl
}

// siteName is the site name for private paths.
func (c *Client) siteName(ctx context.Context) (string, error) {
	if c.Sites == nil {
		return c.Site, nil
	}
	return c.Sites.Name(ctx, c.Site)
}

// apiBase is the site base of an API, relative to BaseURL.
func apiBase(api, site string) string {
	site = url.PathEscape(site)
	if api == "classic" {
		return "/api/s/" + site + "/"
	}
	return "/v2/api/site/" + site + "/"
}

func (c *Client) url(ctx context.Context, k *engine.Kind, rel, id string) (string, error) {
	site, err := c.siteName(ctx)
	if err != nil {
		return "", err
	}
	p := rel
	if id != "" {
		p = strings.ReplaceAll(p, "{id}", url.PathEscape(id))
	}
	return strings.TrimRight(c.BaseURL, "/") + apiBase(k.API, site) + p, nil
}

// HTTPError is a non-2xx answer. The body is kept (trimmed): the controller's
// validation messages name the field and rule.
type HTTPError struct {
	Method, URL string
	Status      int
	Body        string
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

// ClassicError is a classic API refusal: an {"meta":{"rc":"error","msg":…}}
// body, with the first validationError {field, pattern} when there is one.
type ClassicError struct {
	Method, URL string
	Status      int
	Msg         string // api.err.InvalidPayload, api.err.IdInvalid, …
	Detail      string // the item's own msg (api.err.InvalidValue)
	Field       string
	Pattern     string
}

func (e *ClassicError) Error() string {
	msg := fmt.Sprintf("%s %s: HTTP %d: the controller refused it: %s", e.Method, e.URL, e.Status, e.Msg)
	if e.Detail != "" && e.Detail != e.Msg {
		msg += " (" + e.Detail + ")"
	}
	if e.Field != "" {
		msg += fmt.Sprintf(": field %s must match %s", e.Field, e.Pattern)
	}
	return msg
}

// classicError parses a classic error body; nil when it is not one.
func classicError(method, u string, status int, raw []byte) *ClassicError {
	var doc struct {
		Meta struct {
			RC  string `json:"rc"`
			Msg string `json:"msg"`
		} `json:"meta"`
		Data []struct {
			Msg             string `json:"msg"`
			ValidationError struct {
				Field   string `json:"field"`
				Pattern string `json:"pattern"`
			} `json:"validationError"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Meta.RC != "error" {
		return nil
	}
	e := &ClassicError{Method: method, URL: u, Status: status, Msg: doc.Meta.Msg}
	for _, d := range doc.Data {
		if d.ValidationError.Field != "" {
			e.Detail, e.Field, e.Pattern = d.Msg, d.ValidationError.Field, d.ValidationError.Pattern
			break
		}
		if e.Detail == "" {
			e.Detail = d.Msg
		}
	}
	return e
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

func (c *Client) do(ctx context.Context, method, u string, body any) (any, int, error) {
	if c.BaseURL == "" {
		return nil, 0, fmt.Errorf("the private API base URL is not set: set unifi:apiHost (the edge, https://<apiHost>/proxy/network, with unifi:apiKey) or unifi:networkBaseUrl")
	}
	code, raw, err := c.controller().Send(ctx, method, u, body)
	if err != nil {
		return nil, code, err
	}
	if code < 200 || code > 299 {
		if ce := classicError(method, u, code, raw); ce != nil {
			return nil, code, ce
		}
		return nil, code, &HTTPError{Method: method, URL: u, Status: code, Body: errBody(code, raw)}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, code, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, code, fmt.Errorf("%s %s: decoding the response: %w", method, u, err)
	}
	return v, code, nil
}

// unwrap removes the classic {"meta":{"rc":"ok"},"data":[…]} envelope; an
// rc other than "ok" is an error.
func unwrap(k *engine.Kind, v any) (any, error) {
	if k.Envelope != "meta-data" {
		return v, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v, nil
	}
	if meta, ok := m["meta"].(map[string]any); ok {
		if rc, _ := meta["rc"].(string); rc != "" && rc != "ok" {
			msg, _ := meta["msg"].(string)
			return nil, &ClassicError{Status: http.StatusOK, Msg: msg}
		}
	}
	return m["data"], nil
}

func objects(v any) []map[string]any {
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
			return objects(d)
		}
		out = append(out, t)
	}
	return out
}

// List implements engine.Binding.
func (c *Client) List(ctx context.Context, k *engine.Kind) ([]map[string]any, error) {
	if !k.ListGet {
		return nil, fmt.Errorf("%s cannot be listed (no collection GET), so it cannot be found by natural key", k.Token)
	}
	u, err := c.url(ctx, k, k.ListPath, "")
	if err != nil {
		return nil, err
	}
	v, _, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	v, err = unwrap(k, v)
	if err != nil {
		return nil, err
	}
	return objects(v), nil
}

// Get implements engine.Binding.
func (c *Client) Get(ctx context.Context, k *engine.Kind, id string) (map[string]any, bool, error) {
	u, err := c.url(ctx, k, k.ItemPath, id)
	if err != nil {
		return nil, false, err
	}
	v, code, err := c.do(ctx, http.MethodGet, u, nil)
	if code == http.StatusNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	v, err = unwrap(k, v)
	if err != nil {
		return nil, false, err
	}
	objs := objects(v)
	if len(objs) == 0 {
		return nil, false, nil
	}
	return objs[0], true, nil
}

// Create implements engine.Binding.
func (c *Client) Create(ctx context.Context, k *engine.Kind, body any) (any, error) {
	u, err := c.url(ctx, k, k.CreatePath, "")
	if err != nil {
		return nil, err
	}
	v, _, err := c.do(ctx, http.MethodPost, u, body)
	if err != nil {
		return nil, err
	}
	return unwrap(k, v)
}

// Update implements engine.Binding.
func (c *Client) Update(ctx context.Context, k *engine.Kind, id string, body map[string]any) (any, error) {
	u, err := c.url(ctx, k, k.ItemPath, id)
	if err != nil {
		return nil, err
	}
	v, _, err := c.do(ctx, http.MethodPut, u, body)
	if err != nil {
		return nil, err
	}
	return unwrap(k, v)
}

// idInvalid is the classic answer to a DELETE of an id it does not hold, and
// equally of a malformed id (probe f1).
const idInvalid = "api.err.IdInvalid"

// Delete implements engine.Binding.
func (c *Client) Delete(ctx context.Context, k *engine.Kind, id string) (bool, error) {
	u, err := c.url(ctx, k, k.ItemPath, id)
	if err != nil {
		return false, err
	}
	_, code, err := c.do(ctx, http.MethodDelete, u, nil)
	if code == http.StatusNotFound {
		return false, nil
	}
	var ce *ClassicError
	if errors.As(err, &ce) && ce.Msg == idInvalid && k.ListGet {
		// Gone, or a malformed id: only a list that lacks the id says gone.
		all, lerr := c.List(ctx, k)
		if lerr != nil {
			return false, fmt.Errorf("%w (and the list to confirm it is gone failed: %v)", err, lerr)
		}
		for _, o := range all {
			if fmt.Sprint(o[k.IDField]) == id {
				return false, err
			}
		}
		return false, nil
	}
	return err == nil, err
}

// NetworkVersion reads the Network application version the controller runs,
// from the classic stat/sysinfo (probe b3), for the version guard of the
// private kinds (docs/design/private-binding.md §S2.6).
func (c *Client) NetworkVersion(ctx context.Context) (string, error) {
	k := &engine.Kind{API: "classic", Envelope: "meta-data"}
	u, err := c.url(ctx, k, "stat/sysinfo", "")
	if err != nil {
		return "", err
	}
	v, _, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	v, err = unwrap(k, v)
	if err != nil {
		return "", err
	}
	for _, o := range objects(v) {
		if s, ok := o["version"].(string); ok && s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf("GET %s: the answer holds no Network version", u)
}
