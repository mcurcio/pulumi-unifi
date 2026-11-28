package classic_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/binding/classic"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/internal/fakectl"
)

// The classic {"meta":{"rc":…},"data":[…]} envelope is unwrapped, and an rc
// other than ok is an error.
func TestClassicEnvelope(t *testing.T) {
	body := `{"meta":{"rc":"ok"},"data":[{"_id":"1","name":"x"}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/s/default/rest/") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	k := &engine.Kind{Token: engine.PrivateTokenPrefix + "Classic", API: "classic", ListPath: "rest/thing", ItemPath: "rest/thing/{id}",
		ListGet: true, ItemGet: true, IDField: "_id", Envelope: "meta-data"}
	c := &classic.Binding{BaseURL: ts.URL, Site: "default"}
	l, err := c.List(context.Background(), k)
	if err != nil || len(l) != 1 || l[0]["name"] != "x" {
		t.Fatalf("list: %v %v", l, err)
	}
	o, found, err := c.Get(context.Background(), k, "1")
	if err != nil || !found || o["_id"] != "1" {
		t.Fatalf("get: %v %v %v", o, found, err)
	}
	body = `{"meta":{"rc":"error","msg":"api.err.Invalid"},"data":[]}`
	if _, err := c.List(context.Background(), k); err == nil || !strings.Contains(err.Error(), "api.err.Invalid") {
		t.Errorf("rc error: %v", err)
	}
}

// Stored ids name the private API, as for v2 (private:<_id>).
func TestStoredIDAPI(t *testing.T) {
	if got := (&classic.Binding{}).API(); got != "private" {
		t.Errorf("API() = %q, want private", got)
	}
}

func TestClassicIdInvalidIsGoneOnlyWhenTheListLacksIt(t *testing.T) {
	ctx := context.Background()
	srv := fakectl.Start("default")
	t.Cleanup(srv.Close)
	k := fakectl.Surface().Find(fakectl.CrewToken)
	c := &classic.Binding{BaseURL: srv.URL, Site: "default"}
	found, err := c.Delete(ctx, k, "00000000000000000000beef")
	if err != nil || found {
		t.Errorf("IdInvalid for an id the list lacks: found %v err %v; want gone", found, err)
	}
	// A malformed id is never in a list either, so the list cannot tell it
	// from a gone one: it is an error, never a silent success.
	if _, err := c.Delete(ctx, k, "not-an-id"); err == nil || !strings.Contains(err.Error(), "not a classic object id") {
		t.Errorf("IdInvalid for a malformed id: err %v; want an error naming the malformed id", err)
	}
	// An id the list still holds when the DELETE answers IdInvalid is an
	// error, not a silent success.
	held := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"meta":{"rc":"error","msg":"api.err.IdInvalid"},"data":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"held","name":"held"}]}`))
	}))
	t.Cleanup(held.Close)
	c2 := &classic.Binding{BaseURL: held.URL, Site: "default"}
	if _, err := c2.Delete(ctx, k, "held"); err == nil || !strings.Contains(err.Error(), "api.err.IdInvalid") {
		t.Errorf("a refused DELETE of a listed id must be an error: %v", err)
	}
}
