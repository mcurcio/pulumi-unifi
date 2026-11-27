package binding_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/binding"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/internal/fakectl"
)

// Request shapes: v2 site paths, JSON headers, X-API-Key only when set,
// list at the list path, list body for CreateList kinds, 404 = gone.
func TestRequestShapes(t *testing.T) {
	ctx := context.Background()
	srv := fakectl.Start("lab")
	defer srv.Close()
	s := fakectl.Surface()
	thing, group := s.Find(fakectl.ThingToken), s.Find(fakectl.GroupToken)

	c := &binding.Client{BaseURL: srv.URL + "/", Site: "lab"}
	resp, err := c.Create(ctx, thing, map[string]any{"name": "a"})
	if err != nil {
		t.Fatal(err)
	}
	id := resp.(map[string]any)["_id"].(string)
	if _, found, err := c.Get(ctx, thing, id); !found || err != nil {
		t.Fatalf("get: %v %v", found, err)
	}
	if _, found, err := c.Get(ctx, thing, "missing"); found || err != nil {
		t.Fatalf("get missing: found=%v err=%v", found, err)
	}
	if _, err := c.Update(ctx, thing, id, map[string]any{"_id": id, "name": "b"}); err != nil {
		t.Fatal(err)
	}
	if found, err := c.Delete(ctx, thing, id); !found || err != nil {
		t.Fatalf("delete: %v %v", found, err)
	}
	if found, err := c.Delete(ctx, thing, id); found || err != nil {
		t.Fatalf("delete again: found=%v err=%v", found, err)
	}
	if _, err := c.Create(ctx, group, []any{map[string]any{"name": "g", "kind": "A"}}); err != nil {
		t.Fatal(err)
	}
	l, err := c.List(ctx, group)
	if err != nil || len(l) != 1 {
		t.Fatalf("list: %v %v", l, err)
	}
	want := []string{
		"POST /v2/api/site/lab/things",
		"GET /v2/api/site/lab/things/" + id,
		"GET /v2/api/site/lab/things/missing",
		"PUT /v2/api/site/lab/things/" + id,
		"DELETE /v2/api/site/lab/things/" + id,
		"DELETE /v2/api/site/lab/things/" + id,
		"POST /v2/api/site/lab/groups",
		"GET /v2/api/site/lab/groups-all",
	}
	reqs := srv.Requests()
	if len(reqs) != len(want) {
		t.Fatalf("got %d requests, want %d: %+v", len(reqs), len(want), reqs)
	}
	for i, r := range reqs {
		if got := r.Method + " " + r.Path; got != want[i] {
			t.Errorf("request %d: %s, want %s", i, got, want[i])
		}
		if r.APIKey != "" {
			t.Errorf("request %d sent X-API-Key without a key configured", i)
		}
	}
	if _, isList := reqs[6].Body.([]any); !isList {
		t.Error("CreateList body must be a list")
	}

	c.APIKey = "test-key-not-real"
	if _, err := c.List(ctx, thing); err != nil {
		t.Fatal(err)
	}
	if r := srv.Requests(); r[0].APIKey != "test-key-not-real" {
		t.Errorf("X-API-Key = %q", r[0].APIKey)
	}
}

func TestErrorsKeepTheControllerMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"api.err.Invalid","message":"name must not be empty"}`))
	}))
	defer ts.Close()
	k := fakectl.Surface().Find(fakectl.ThingToken)
	_, err := (&binding.Client{BaseURL: ts.URL, Site: "default"}).Create(context.Background(), k, map[string]any{})
	var he *binding.HTTPError
	if !errors.As(err, &he) || he.Status != 400 || !strings.Contains(err.Error(), "name must not be empty") {
		t.Fatalf("err = %v", err)
	}
	if _, err := (&binding.Client{Site: "default"}).List(context.Background(), k); err == nil || !strings.Contains(err.Error(), "networkBaseUrl") {
		t.Errorf("no base URL: %v", err)
	}
}

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
	k := &engine.Kind{Token: engine.TokenPrefix + "Classic", API: "classic", ListPath: "rest/thing", ItemPath: "rest/thing/{id}",
		ListGet: true, ItemGet: true, IDField: "_id", Envelope: "meta-data"}
	c := &binding.Client{BaseURL: ts.URL, Site: "default"}
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
