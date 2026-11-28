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

// SiteResolver maps the configured site between its two forms
// (docs/design/private-binding.md §S2.6): the official API takes the site
// UUID, the private v2 and classic APIs take the site name
// (internalReference, e.g. "default"). It is lazy: when the form given is the
// form needed there is no call; otherwise one GET /v1/sites, cached for the
// life of the provider.
type SiteResolver struct {
	C *Client

	mu     sync.Mutex
	loaded bool
	byRef  map[string]string // internalReference -> UUID
	byName map[string]string // display name -> UUID
	refOf  map[string]string // UUID -> internalReference
	refs   []string
}

type sitePage struct {
	Data []struct {
		ID                string `json:"id"`
		InternalReference string `json:"internalReference"`
		Name              string `json:"name"`
	} `json:"data"`
}

// load fetches the site list once. The caller holds mu.
func (r *SiteResolver) load(ctx context.Context, site string) error {
	if r.loaded {
		return nil
	}
	root, err := r.C.IntegrationRoot()
	if err != nil {
		return err
	}
	var page sitePage
	if err := r.C.Do(ctx, "GET", root+"/v1/sites?limit=200", nil, &page); err != nil {
		return fmt.Errorf("resolving site %q: %w", site, err)
	}
	r.byRef, r.byName, r.refOf = map[string]string{}, map[string]string{}, map[string]string{}
	for _, s := range page.Data {
		id := strings.ToLower(s.ID)
		if s.InternalReference != "" {
			r.byRef[s.InternalReference] = id
			r.refOf[id] = s.InternalReference
		}
		if s.Name != "" {
			r.byName[s.Name] = id
		}
		r.refs = append(r.refs, s.InternalReference)
	}
	sort.Strings(r.refs)
	r.loaded = true
	return nil
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
	if err := r.load(ctx, site); err != nil {
		return "", err
	}
	if id, ok := r.byRef[site]; ok {
		return id, nil
	}
	if id, ok := r.byName[site]; ok {
		return id, nil
	}
	return "", fmt.Errorf("site %q not found on the controller (sites: %v); set siteId to a site name or UUID", site, r.refs)
}

// Name returns the site name the private APIs take in their paths. A name is
// returned as given, with no call. A UUID needs the official site list, so
// with no API key it is returned as given (the slice-1, pre-wizard path,
// where siteId is used verbatim).
func (r *SiteResolver) Name(ctx context.Context, site string) (string, error) {
	if site == "" {
		return "", fmt.Errorf("no site given")
	}
	if !IsUUID(site) || r == nil || r.C == nil || !r.C.HasKey() {
		return site, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(ctx, site); err != nil {
		return "", err
	}
	if ref, ok := r.refOf[strings.ToLower(site)]; ok {
		return ref, nil
	}
	return "", fmt.Errorf("site %q not found on the controller (sites: %v); set siteId to a site name or UUID", site, r.refs)
}
