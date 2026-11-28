// Package v2 is the binding of the Network application's v2 API
// (docs/design/private-binding.md §2, §7, §S2.2 L2, §S2.6): kind paths under
// <network root>/v2/api/site/{name}/, bare JSON bodies, 404 as gone. The
// network root is the authenticated edge (https://<apiHost>/proxy/network, the
// same apiKey as the official API) or the networkBaseUrl override (the Network
// app directly, before the wizard). The wire itself (TLS policy, X-API-Key, 429
// retry) is the controller layer's; this package maps paths and statuses to
// engine outcomes. It implements engine.Binding and knows nothing of Pulumi.
package v2

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/binding/internal/netapp"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/controller"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
)

// API names the private API in stored ids ("private:<_id>"); the v2 and
// classic bindings share it (frame rule 3; private-binding.md §S2.9).
const API = "private"

// HTTPError is a non-2xx answer (the body kept, trimmed).
type HTTPError = netapp.HTTPError

// Binding calls the v2 API. Its fields are netapp.Conn's.
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

func siteBase(site string) string { return "/v2/api/site/" + site + "/" }

func (b *Binding) url(ctx context.Context, rel, id string) (string, error) {
	return b.conn().URL(ctx, siteBase, rel, id)
}

func (b *Binding) do(ctx context.Context, method, u string, body any) (any, int, error) {
	code, raw, err := b.conn().Send(ctx, method, u, body)
	if err != nil {
		return nil, code, err
	}
	if code < 200 || code > 299 {
		return nil, code, netapp.NewHTTPError(method, u, code, raw)
	}
	v, err := netapp.Decode(method, u, raw)
	return v, code, err
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
	return v, err
}

// Update implements engine.Binding.
func (b *Binding) Update(ctx context.Context, k *engine.Kind, id string, body map[string]any) (any, error) {
	u, err := b.url(ctx, k.ItemPath, id)
	if err != nil {
		return nil, err
	}
	v, _, err := b.do(ctx, http.MethodPut, u, body)
	return v, err
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
	return err == nil, err
}
