// Package binding is the HTTP binding of the private API
// (docs/design/private-binding.md §2, §7): v2 paths under
// <networkBaseUrl>/v2/api/site/{site}/, and already the classic
// {"meta":…,"data":[…]} envelope. It implements engine.Binding.
package binding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
)

// Client calls the private API.
type Client struct {
	// BaseURL is the network app base: https://<host>/proxy/network through
	// the UniFi OS edge, or http://127.0.0.1:8081 directly.
	BaseURL string
	Site    string
	// APIKey is sent as X-API-Key when set. Never logged.
	APIKey string
	HTTP   *http.Client
}

// apiBase is the site base of an API, relative to BaseURL.
func (c *Client) apiBase(api string) string {
	site := url.PathEscape(c.Site)
	if api == "classic" {
		return "/api/s/" + site + "/"
	}
	return "/v2/api/site/" + site + "/"
}

func (c *Client) url(k *engine.Kind, rel, id string) string {
	p := rel
	if id != "" {
		p = strings.ReplaceAll(p, "{id}", url.PathEscape(id))
	}
	return strings.TrimRight(c.BaseURL, "/") + c.apiBase(k.API) + p
}

// HTTPError is a non-2xx answer. The body is kept (trimmed): the controller's
// validation messages name the field and rule.
type HTTPError struct {
	Method, URL string
	Status      int
	Body        string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, e.Body)
}

func (c *Client) do(ctx context.Context, method, u string, body any) (any, int, error) {
	if c.BaseURL == "" {
		return nil, 0, fmt.Errorf("the private API base URL is not set: set unifi:networkBaseUrl (or unifi:apiHost for https://<apiHost>/proxy/network)")
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s %s: %w", method, u, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 2000 {
			msg = msg[:2000] + "…"
		}
		return nil, resp.StatusCode, &HTTPError{Method: method, URL: u, Status: resp.StatusCode, Body: msg}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, resp.StatusCode, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%s %s: decoding the response: %w", method, u, err)
	}
	return v, resp.StatusCode, nil
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
			return nil, fmt.Errorf("controller answered rc=%s: %s", rc, msg)
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
	v, _, err := c.do(ctx, http.MethodGet, c.url(k, k.ListPath, ""), nil)
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
	v, code, err := c.do(ctx, http.MethodGet, c.url(k, k.ItemPath, id), nil)
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
	v, _, err := c.do(ctx, http.MethodPost, c.url(k, k.CreatePath, ""), body)
	if err != nil {
		return nil, err
	}
	return unwrap(k, v)
}

// Update implements engine.Binding.
func (c *Client) Update(ctx context.Context, k *engine.Kind, id string, body map[string]any) (any, error) {
	v, _, err := c.do(ctx, http.MethodPut, c.url(k, k.ItemPath, id), body)
	if err != nil {
		return nil, err
	}
	return unwrap(k, v)
}

// Delete implements engine.Binding.
func (c *Client) Delete(ctx context.Context, k *engine.Kind, id string) (bool, error) {
	_, code, err := c.do(ctx, http.MethodDelete, c.url(k, k.ItemPath, id), nil)
	if code == http.StatusNotFound {
		return false, nil
	}
	return err == nil, err
}
