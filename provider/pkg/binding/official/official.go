// Package official is the official (Integration API) binding of the engine
// (docs/design/private-binding.md §S2.2, L2): the wire calls for engine-served
// official kinds, today the ordered singletons (AclRuleOrdering). It maps wire
// outcomes to the engine's domain outcomes and never decides what to send.
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

// API is the stored-id prefix of objects this binding serves ("official:").
const API = "official"

// Binding implements engine.OrderBinding over the official API.
type Binding struct {
	C     *controller.Client
	Sites *controller.SiteResolver
}

// New returns a binding over c.
func New(c *controller.Client) *Binding {
	return &Binding{C: c, Sites: &controller.SiteResolver{C: c}}
}

// API implements engine.OrderBinding.
func (b *Binding) API() string { return API }

// SiteID implements engine.OrderBinding: the official API addresses a site by
// its UUID.
func (b *Binding) SiteID(ctx context.Context, site string) (string, error) {
	if !b.C.HasKey() {
		return "", errors.New("a UniFi API key is required for the official resources (set unifi:apiKey or the UNIFI_APIKEY env var)")
	}
	return b.Sites.UUID(ctx, site)
}

func (b *Binding) url(s *engine.Singleton, siteID string) (string, error) {
	root, err := b.C.IntegrationRoot()
	if err != nil {
		return "", err
	}
	return root + strings.ReplaceAll(s.Path, "{siteId}", url.PathEscape(siteID)), nil
}

// GetOrder implements engine.OrderBinding.
func (b *Binding) GetOrder(ctx context.Context, s *engine.Singleton, siteID string) ([]string, error) {
	u, err := b.url(s, siteID)
	if err != nil {
		return nil, err
	}
	var body map[string][]string
	if err := b.C.Do(ctx, http.MethodGet, u, nil, &body); err != nil {
		return nil, outcome(err)
	}
	ids, ok := body[s.Property]
	if !ok {
		return nil, fmt.Errorf("GET %s: the answer has no %s", u, s.Property)
	}
	return ids, nil
}

// PutOrder implements engine.OrderBinding.
func (b *Binding) PutOrder(ctx context.Context, s *engine.Singleton, siteID string, ids []string) ([]string, error) {
	u, err := b.url(s, siteID)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []string{}
	}
	var body map[string][]string
	if err := b.C.Do(ctx, http.MethodPut, u, map[string][]string{s.Property: ids}, &body); err != nil {
		return nil, outcome(err)
	}
	if got, ok := body[s.Property]; ok {
		return got, nil
	}
	return ids, nil
}

// outcome maps the wire answer to a domain outcome: a 400 saying the order
// must list every entry (probe a5: api.acl-rule.ordering.not-all-acl-rules-
// specified) is engine.ErrIncomplete; a 404 is engine.ErrGone; anything else
// stays the controller's short error, with the server's message.
func outcome(err error) error {
	var ce *controller.Error
	if !errors.As(err, &ce) {
		return err
	}
	switch {
	case ce.Status == http.StatusBadRequest && strings.Contains(ce.Code, "not-all-"):
		return fmt.Errorf("%w: %v", engine.ErrIncomplete, err)
	case ce.Status == http.StatusNotFound:
		return fmt.Errorf("%w: %v", engine.ErrGone, err)
	}
	return err
}
