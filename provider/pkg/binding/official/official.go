// Package official is the official (Integration API) binding of the engine
// (docs/design/private-binding.md §S2.2, L2): the wire calls for engine-served
// official kinds, today the site-keyed singletons (AclRuleOrdering, read and
// replaced on the kind's item path with the site's UUID). It maps wire
// outcomes to the engine's domain outcomes (404 is gone, a 400 not-all-*
// refusal is engine.ErrIncomplete) and never decides what to send. It
// implements engine.Binding and knows nothing of Pulumi.
package official

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/controller"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
)

// API names the official API in stored ids ("official:<site UUID>").
const API = "official"

// Binding implements engine.Binding over the official API.
type Binding struct {
	C     *controller.Client
	Sites *controller.SiteResolver
}

// New returns a binding over c.
func New(c *controller.Client) *Binding {
	return &Binding{C: c, Sites: &controller.SiteResolver{C: c}}
}

// API implements engine.Binding.
func (b *Binding) API() string { return API }

// SiteID implements engine.Binding: the official API addresses a site by its
// UUID.
func (b *Binding) SiteID(ctx context.Context, site string) (string, error) {
	if !b.C.HasKey() {
		return "", errors.New("a UniFi API key is required for the official resources (set unifi:apiKey or the UNIFI_APIKEY env var)")
	}
	return b.Sites.UUID(ctx, site)
}

// url is the item path with the site's UUID (a site-keyed singleton's id).
func (b *Binding) url(k *engine.Kind, siteID string) (string, error) {
	if !k.KeyedBySite {
		return "", fmt.Errorf("%s: the official binding serves only singletons keyed by their site", k.Token)
	}
	root, err := b.C.IntegrationRoot()
	if err != nil {
		return "", err
	}
	return root + strings.ReplaceAll(k.ItemPath, "{siteId}", url.PathEscape(siteID)), nil
}

// Get implements engine.Binding: the site's document.
func (b *Binding) Get(ctx context.Context, k *engine.Kind, siteID string) (map[string]any, bool, error) {
	u, err := b.url(k, siteID)
	if err != nil {
		return nil, false, err
	}
	var body map[string]any
	if err := b.C.Do(ctx, http.MethodGet, u, nil, &body); err != nil {
		var ce *controller.Error
		if errors.As(err, &ce) && ce.Status == http.StatusNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	return body, true, nil
}

// Update implements engine.Binding: replaces the site's document and returns
// the server's answer.
func (b *Binding) Update(ctx context.Context, k *engine.Kind, siteID string, doc map[string]any) (any, error) {
	u, err := b.url(k, siteID)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := b.C.Do(ctx, http.MethodPut, u, doc, &body); err != nil {
		return nil, outcome(err)
	}
	return body, nil
}

func (b *Binding) unsupported(op string, k *engine.Kind) error {
	return fmt.Errorf("%s %s: the official binding serves only singletons keyed by their site (read and replace)", op, k.Token)
}

// List implements engine.Binding (not served).
func (b *Binding) List(_ context.Context, k *engine.Kind) ([]map[string]any, error) {
	return nil, b.unsupported("list", k)
}

// Create implements engine.Binding (not served: a singleton is adopted).
func (b *Binding) Create(_ context.Context, k *engine.Kind, _ any) (any, error) {
	return nil, b.unsupported("create", k)
}

// Delete implements engine.Binding (not served: a singleton is released).
func (b *Binding) Delete(_ context.Context, k *engine.Kind, _ string) (bool, error) {
	return false, b.unsupported("delete", k)
}

// outcome maps the wire answer to a domain outcome: a 400 saying the order
// must list every entry (probe a5: api.acl-rule.ordering.not-all-acl-rules-
// specified) is engine.ErrIncomplete; a 404 is engine.ErrNotFound; anything
// else stays the controller's short error, with the server's message.
func outcome(err error) error {
	var ce *controller.Error
	if !errors.As(err, &ce) {
		return err
	}
	switch {
	case ce.Status == http.StatusBadRequest && strings.Contains(ce.Code, "not-all-"):
		return fmt.Errorf("%w: %v", engine.ErrIncomplete, err)
	case ce.Status == http.StatusNotFound:
		return fmt.Errorf("%w: %v", engine.ErrNotFound, err)
	}
	return err
}
