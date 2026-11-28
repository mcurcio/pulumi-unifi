// Package classic is the binding of the Network application's classic API
// (docs/design/private-binding.md §S2.2 L2, §S2.8): kind paths under
// <network root>/api/s/{name}/ with the {"meta":…,"data":[…]} envelope and its
// wire semantics: create answers 200 with data:[obj]; a GET answering data:[]
// is gone; an rc:"error" body is a ClassicError; a DELETE answering
// api.err.IdInvalid is gone only once a list confirms it. It also reads the
// Network version (stat/sysinfo) for the version guard. The wire itself (TLS
// policy, X-API-Key, 429 retry) is the controller layer's. It implements
// engine.Binding and knows nothing of Pulumi.
package classic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/binding/internal/netapp"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/controller"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
)

// API names the private API in stored ids ("private:<_id>"); the v2 and
// classic bindings share it (frame rule 3; private-binding.md §S2.9).
const API = "private"

// HTTPError is a non-2xx answer that is not a classic refusal.
type HTTPError = netapp.HTTPError

// Binding calls the classic API. Its fields are netapp.Conn's.
type Binding struct {
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

func (b *Binding) conn() netapp.Conn { return netapp.Conn(*b) }

func siteBase(site string) string { return "/api/s/" + site + "/" }

func (b *Binding) url(ctx context.Context, rel, id string) (string, error) {
	return b.conn().URL(ctx, siteBase, rel, id)
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

func (b *Binding) do(ctx context.Context, method, u string, body any) (any, int, error) {
	code, raw, err := b.conn().Send(ctx, method, u, body)
	if err != nil {
		return nil, code, err
	}
	if code < 200 || code > 299 {
		if ce := classicError(method, u, code, raw); ce != nil {
			return nil, code, ce
		}
		return nil, code, netapp.NewHTTPError(method, u, code, raw)
	}
	v, err := netapp.Decode(method, u, raw)
	return v, code, err
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

// API implements engine.Binding.
func (b *Binding) API() string { return API }

// SiteID implements engine.Binding: the private paths take the site name.
func (b *Binding) SiteID(ctx context.Context, site string) (string, error) {
	return b.conn().SiteName(ctx, site)
}

// List implements engine.Binding.
func (b *Binding) List(ctx context.Context, k *engine.Kind) ([]map[string]any, error) {
	if !k.ListGet {
		return nil, fmt.Errorf("%s cannot be listed (no collection GET), so it cannot be found by natural key", k.Token)
	}
	u, err := b.url(ctx, k.ListPath, "")
	if err != nil {
		return nil, err
	}
	v, _, err := b.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	v, err = unwrap(k, v)
	if err != nil {
		return nil, err
	}
	return netapp.Objects(v), nil
}

// Get implements engine.Binding.
func (b *Binding) Get(ctx context.Context, k *engine.Kind, id string) (map[string]any, bool, error) {
	u, err := b.url(ctx, k.ItemPath, id)
	if err != nil {
		return nil, false, err
	}
	v, code, err := b.do(ctx, http.MethodGet, u, nil)
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
	objs := netapp.Objects(v)
	if len(objs) == 0 {
		return nil, false, nil
	}
	return objs[0], true, nil
}

// Create implements engine.Binding.
func (b *Binding) Create(ctx context.Context, k *engine.Kind, body any) (any, error) {
	u, err := b.url(ctx, k.CreatePath, "")
	if err != nil {
		return nil, err
	}
	v, _, err := b.do(ctx, http.MethodPost, u, body)
	if err != nil {
		return nil, err
	}
	return unwrap(k, v)
}

// Update implements engine.Binding.
func (b *Binding) Update(ctx context.Context, k *engine.Kind, id string, body map[string]any) (any, error) {
	u, err := b.url(ctx, k.ItemPath, id)
	if err != nil {
		return nil, err
	}
	v, _, err := b.do(ctx, http.MethodPut, u, body)
	if err != nil {
		return nil, err
	}
	return unwrap(k, v)
}

// idInvalid is the classic answer to a DELETE of an id it does not hold, and
// equally of a malformed id (probe f1).
const idInvalid = "api.err.IdInvalid"

// objectID is the shape of a classic _id (a 24-hex-digit object id). A
// malformed id is absent from every list, so only its shape tells it from a
// gone one.
func objectID(id string) bool {
	if len(id) != 24 {
		return false
	}
	for _, r := range id {
		if !('0' <= r && r <= '9' || 'a' <= r && r <= 'f' || 'A' <= r && r <= 'F') {
			return false
		}
	}
	return true
}

// Delete implements engine.Binding.
func (b *Binding) Delete(ctx context.Context, k *engine.Kind, id string) (bool, error) {
	u, err := b.url(ctx, k.ItemPath, id)
	if err != nil {
		return false, err
	}
	_, code, err := b.do(ctx, http.MethodDelete, u, nil)
	if code == http.StatusNotFound {
		return false, nil
	}
	var ce *ClassicError
	if errors.As(err, &ce) && ce.Msg == idInvalid && k.ListGet {
		// Gone, or a malformed id: a malformed id is an error; a well-formed
		// one is gone only when a list lacks it.
		if k.IDField == "_id" && !objectID(id) {
			return false, fmt.Errorf("%w: %q is not a classic object id (24 hex digits)", err, id)
		}
		all, lerr := b.List(ctx, k)
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
func (b *Binding) NetworkVersion(ctx context.Context) (string, error) {
	k := &engine.Kind{API: engine.APIClassic, Envelope: "meta-data"}
	u, err := b.url(ctx, "stat/sysinfo", "")
	if err != nil {
		return "", err
	}
	v, _, err := b.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	v, err = unwrap(k, v)
	if err != nil {
		return "", err
	}
	for _, o := range netapp.Objects(v) {
		if s, ok := o["version"].(string); ok && s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf("GET %s: the answer holds no Network version", u)
}
