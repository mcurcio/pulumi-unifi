package fakectl

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Official is an in-memory stand-in for the official API's site list and ACL
// rule ordering (/proxy/network/integration/v1/sites[/{siteId}/acl-rules/
// ordering]), with the 400 rules measured live (probe a5): the PUT must list
// every live rule exactly once, and only live rules. It serves TLS, like the
// edge, and requires an X-API-Key.
type Official struct {
	*httptest.Server
	Key      string
	SiteID   string
	SiteName string

	mu    sync.Mutex
	order []string
	// BeforePut, when set, runs before each PUT is checked (to simulate a
	// rule created elsewhere between GET and PUT).
	BeforePut func(o *Official)
	Gets      int
	Puts      int
	SiteLists int
}

// NewOfficial starts the fake with the given live rule order.
func NewOfficial(key, siteID, siteName string, order ...string) *Official {
	o := &Official{Key: key, SiteID: siteID, SiteName: siteName, order: append([]string(nil), order...)}
	o.Server = httptest.NewTLSServer(http.HandlerFunc(o.serve))
	return o
}

// Order returns the live order.
func (o *Official) Order() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.order...)
}

// SetOrder replaces the live order (a change made outside the provider).
func (o *Official) SetOrder(ids ...string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.order = append([]string(nil), ids...)
}

// Host is the host:port to configure as apiHost.
func (o *Official) Host() string { return strings.TrimPrefix(o.URL, "https://") }

func (o *Official) fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"statusCode": status, "code": code, "message": msg})
}

func (o *Official) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-API-Key") != o.Key {
		o.fail(w, http.StatusUnauthorized, "", "Unauthorized")
		return
	}
	const root = "/proxy/network/integration/v1/sites"
	switch {
	case r.URL.Path == root && r.Method == http.MethodGet:
		o.mu.Lock()
		o.SiteLists++
		o.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"offset": 0, "limit": 200, "count": 1, "totalCount": 1,
			"data": []any{map[string]any{"id": o.SiteID, "internalReference": o.SiteName, "name": "Default"}}})
	case r.URL.Path == root+"/"+o.SiteID+"/acl-rules/ordering":
		o.ordering(w, r)
	case strings.HasPrefix(r.URL.Path, root+"/"):
		o.fail(w, http.StatusNotFound, "api.resource-not-found", "not found")
	default:
		o.fail(w, http.StatusNotFound, "", "no route")
	}
}

func (o *Official) ordering(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && o.BeforePut != nil {
		o.BeforePut(o)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		o.Gets++
		_ = json.NewEncoder(w).Encode(map[string]any{"orderedAclRuleIds": o.order})
	case http.MethodPut:
		o.Puts++
		var body struct {
			IDs []string `json:"orderedAclRuleIds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			o.fail(w, http.StatusBadRequest, "api.request.error", err.Error())
			return
		}
		live := map[string]bool{}
		for _, id := range o.order {
			live[id] = true
		}
		seen := map[string]bool{}
		for _, id := range body.IDs {
			if seen[id] {
				o.fail(w, http.StatusBadRequest, "api.acl-rule.ordering.duplicate-acl-rule-id", "duplicate "+id)
				return
			}
			seen[id] = true
			if !live[id] {
				o.fail(w, http.StatusBadRequest, "api.request.invalid-reference", "unknown ACL rule "+id)
				return
			}
		}
		if len(seen) != len(live) {
			o.fail(w, http.StatusBadRequest, "api.acl-rule.ordering.not-all-acl-rules-specified", "not all ACL rules specified")
			return
		}
		o.order = append([]string(nil), body.IDs...)
		_ = json.NewEncoder(w).Encode(map[string]any{"orderedAclRuleIds": o.order})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
