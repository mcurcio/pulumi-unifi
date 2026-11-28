package binding_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/controller"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/binding"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/internal/fakectl"
)

const siteUUID = "88f7af54-98f8-306a-a1c7-c9349722b1f6"

// edge is a TLS stand-in for the UniFi OS edge after the wizard: every path
// needs the key; /proxy/network/integration/v1/sites lists the site;
// /proxy/network/v2/... answers private calls (the first one with a 429).
type edge struct {
	mu        sync.Mutex
	paths     []string
	siteCalls int
	throttled bool
}

func (e *edge) serve(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r.Header.Get("X-API-Key") != "edge-key-not-real" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<html><body>401 Authorization Required</body></html>`))
		return
	}
	e.paths = append(e.paths, r.Method+" "+r.URL.Path)
	switch {
	case r.URL.Path == "/proxy/network/integration/v1/sites":
		e.siteCalls++
		_, _ = w.Write([]byte(`{"data":[{"id":"` + siteUUID + `","internalReference":"lab","name":"Lab"}]}`))
	case strings.HasPrefix(r.URL.Path, "/proxy/network/v2/api/site/lab/"):
		if !e.throttled {
			e.throttled = true
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	case r.URL.Path == "/proxy/network/api/s/lab/stat/sysinfo":
		_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"version":"10.4.57","build":"atag_10.4.57_1"}]}`))
	default:
		http.NotFound(w, r)
	}
}

func edgeClient(t *testing.T, key, site string) (*binding.Client, *edge) {
	t.Helper()
	e := &edge{}
	srv := httptest.NewTLSServer(http.HandlerFunc(e.serve))
	t.Cleanup(srv.Close)
	ctl := controller.New(controller.Config{APIHost: strings.TrimPrefix(srv.URL, "https://"), APIKey: key, AllowInsecure: true})
	ctl.Sleep = func(time.Duration) {}
	root, err := ctl.NetworkRoot()
	if err != nil {
		t.Fatal(err)
	}
	return &binding.Client{BaseURL: root, Site: site, Ctl: ctl, Sites: &controller.SiteResolver{C: ctl}}, e
}

// Through the edge: the Network root is https://<apiHost>/proxy/network, the
// key goes on every private call, a 429 is retried by the controller layer, a
// UUID siteId is turned into the site name once (cached), and a site name
// costs no call.
func TestEdgeKeySiteAndRetry(t *testing.T) {
	ctx := context.Background()
	k := fakectl.Surface().Find(fakectl.ThingToken)

	c, e := edgeClient(t, "edge-key-not-real", siteUUID)
	for i := 0; i < 2; i++ {
		if _, err := c.List(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	if v, err := c.NetworkVersion(ctx); err != nil || v != "10.4.57" {
		t.Errorf("NetworkVersion = %q, %v", v, err)
	}
	want := []string{
		"GET /proxy/network/integration/v1/sites",
		"GET /proxy/network/v2/api/site/lab/things", // 429
		"GET /proxy/network/v2/api/site/lab/things",
		"GET /proxy/network/v2/api/site/lab/things",
		"GET /proxy/network/api/s/lab/stat/sysinfo",
	}
	if strings.Join(e.paths, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(e.paths, "\n"), strings.Join(want, "\n"))
	}

	byName, e2 := edgeClient(t, "edge-key-not-real", "lab")
	if _, err := byName.List(ctx, k); err != nil {
		t.Fatal(err)
	}
	if e2.siteCalls != 0 {
		t.Errorf("a site name must cost no site lookup (%d calls)", e2.siteCalls)
	}
}

// Without the key the edge answers 401 with an HTML page: the error is short,
// says what to configure, and never holds the page.
func TestEdgeRefusalIsExplained(t *testing.T) {
	c, _ := edgeClient(t, "wrong-key-not-real", "lab")
	_, err := c.List(context.Background(), fakectl.Surface().Find(fakectl.ThingToken))
	var he *binding.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "<html") || !strings.Contains(err.Error(), "unifi:apiKey") {
		t.Errorf("error = %v", err)
	}
	if strings.Contains(err.Error(), "wrong-key-not-real") {
		t.Error("the error holds the key")
	}
}

// With no key a UUID siteId is used as given (the pre-wizard direct path has
// no official API to resolve it with).
func TestNoKeyUsesSiteAsGiven(t *testing.T) {
	srv := fakectl.Start(siteUUID)
	defer srv.Close()
	ctl := controller.New(controller.Config{NetworkBaseURL: srv.URL})
	root, _ := ctl.NetworkRoot()
	c := &binding.Client{BaseURL: root, Site: siteUUID, Ctl: ctl, Sites: &controller.SiteResolver{C: ctl}}
	if _, err := c.List(context.Background(), fakectl.Surface().Find(fakectl.ThingToken)); err != nil {
		t.Fatal(err)
	}
	if r := srv.Requests(); len(r) != 1 || r[0].Path != "/v2/api/site/"+siteUUID+"/things" {
		t.Errorf("requests %+v", r)
	}
}
