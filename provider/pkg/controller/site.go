package controller

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var reUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID reports whether s is a UUID (the official API's site id form).
func IsUUID(s string) bool { return reUUID.MatchString(s) }

// SiteResolver turns the configured site (a name such as "default", or a
// UUID) into the official site UUID (docs/design/private-binding.md §S2.6). It
// is lazy: a UUID is returned as given with no call; a name costs one
// GET /v1/sites, and the answer is cached for the life of the provider.
type SiteResolver struct {
	C *Client

	mu    sync.Mutex
	byRef map[string]string // name or internalReference -> UUID
}

type sitePage struct {
	Data []struct {
		ID                string `json:"id"`
		InternalReference string `json:"internalReference"`
		Name              string `json:"name"`
	} `json:"data"`
}

// UUID returns the site's official UUID.
func (r *SiteResolver) UUID(ctx context.Context, site string) (string, error) {
	if site == "" {
		return "", fmt.Errorf("no site given")
	}
	if IsUUID(site) {
		return strings.ToLower(site), nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.byRef[site]; ok {
		return id, nil
	}
	root, err := r.C.IntegrationRoot()
	if err != nil {
		return "", err
	}
	var page sitePage
	if err := r.C.Do(ctx, "GET", root+"/v1/sites?limit=200", nil, &page); err != nil {
		return "", fmt.Errorf("resolving site %q: %w", site, err)
	}
	if r.byRef == nil {
		r.byRef = map[string]string{}
	}
	var names []string
	for _, s := range page.Data {
		if s.InternalReference != "" {
			r.byRef[s.InternalReference] = strings.ToLower(s.ID)
		}
		names = append(names, s.InternalReference)
	}
	if id, ok := r.byRef[site]; ok {
		return id, nil
	}
	for _, s := range page.Data {
		if s.Name == site {
			return strings.ToLower(s.ID), nil
		}
	}
	sort.Strings(names)
	return "", fmt.Errorf("site %q not found on the controller (sites: %v); set siteId to a site name or UUID", site, names)
}
