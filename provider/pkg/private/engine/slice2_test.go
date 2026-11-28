package engine_test

// Slice 2 engine facts (docs/design/private-binding.md §S2.7, §S2.8) against
// the fake controller: the variant pin (G-Q4 variant exception), create
// defaults and output-only fields, natural-key uniqueness, the classic
// envelope and wire semantics, and the settings singleton's adopt/release.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/binding"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/internal/fakectl"
)

// --- variant pin -----------------------------------------------------------------

func TestVariantCreateInjectsPinAndDefaults(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	k := e.Surface.Find(fakectl.Rule6Token)
	id, out, err := e.Create(ctx, k, pm(map[string]any{"name": "r6", "action": "BLOCK"}))
	if err != nil {
		t.Fatal(err)
	}
	var post fakectl.Request
	for _, r := range srv.Requests() {
		if r.Method == "POST" {
			post = r
		}
	}
	body := post.Body.(map[string]any)
	if body["kind"] != "V6" {
		t.Errorf("create body must carry the pinned variant kind=V6: %v", body)
	}
	if body["slot"] != float64(0) {
		t.Errorf("create body must carry the create default slot=0: %v", body)
	}
	if out["ruleId"].StringValue() != "00000000-0000-4000-8000-0000000000aa" || out["slot"].NumberValue() != 0 {
		t.Errorf("output-only fields must be outputs: %v", out)
	}
	if _, ok := out["kind"]; ok {
		t.Error("the discriminator is the binding's, not an output")
	}
	// The create default is never managed: a refresh adopts no slot input.
	r, err := e.Read(ctx, k, id, pm(map[string]any{"name": "r6", "action": "BLOCK"}), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Inputs["slot"]; ok {
		t.Errorf("slot is not an input after a refresh: %v", r.Inputs)
	}
	// Import adopts the key + required fields, never output-only ones.
	r, err = e.Read(ctx, k, "r6", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Inputs["ruleId"]; ok || r.Inputs["name"].StringValue() != "r6" {
		t.Errorf("import inputs: %v", r.Inputs)
	}
}

func TestVariantListAndKeyLookupKeepOnlyThePinnedValue(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	k := e.Surface.Find(fakectl.Rule6Token)
	// An official (V4) rule with the same name is not this kind's.
	srv.Put("rules", map[string]any{"name": "same", "action": "ALLOW", "kind": "V4", "slot": 0})
	if _, _, err := e.Create(ctx, k, pm(map[string]any{"name": "same", "action": "BLOCK"})); err != nil {
		t.Fatalf("a V4 rule of the same name must not block a V6 create: %v", err)
	}
	r, err := e.Read(ctx, k, "same", nil, true)
	if err != nil {
		t.Fatalf("import by name must see the V6 rule only: %v", err)
	}
	if r.Inputs["action"].StringValue() != "BLOCK" {
		t.Errorf("imported the wrong rule: %v", r.Inputs)
	}
	if _, err := e.Read(ctx, k, "v4only", nil, true); err == nil {
		t.Error("import of a missing key must fail")
	}
	srv.Put("rules", map[string]any{"name": "v4only", "action": "ALLOW", "kind": "L2", "slot": 1})
	if _, err := e.Read(ctx, k, "v4only", nil, true); err == nil || !strings.Contains(err.Error(), "no unifi:network:Rule6 has") {
		t.Errorf("a rule of another variant is not importable here: %v", err)
	}
}

func TestVariantChangedOutOfBandIsLoudNeverGone(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	k := e.Surface.Find(fakectl.Rule6Token)
	in := pm(map[string]any{"name": "flip", "action": "BLOCK"})
	id, _, err := e.Create(ctx, k, in)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.TrimPrefix(id, "private:")
	o := srv.Object("rules", raw)
	o["kind"] = "V4" // the official PUT changed it in place, same id (probe a3)
	srv.Put("rules", o)

	r, err := e.Read(ctx, k, id, in, false)
	if r != nil || err == nil || !strings.Contains(err.Error(), "unifi:sites/v1:RuleV4 or unifi:sites/v1:RuleL2") {
		t.Fatalf("read of a changed variant: %v %v; want a loud error naming the official tokens", r, err)
	}
	if _, err := e.Update(ctx, k, id, in, pm(map[string]any{"name": "flip", "action": "ALLOW"})); err == nil {
		t.Error("update of a changed variant must fail, not overwrite an official rule")
	}
	if err := e.Delete(ctx, k, id); err == nil {
		t.Error("delete of a changed variant must fail, not delete an official rule")
	}
	if srv.Object("rules", raw) == nil {
		t.Error("the rule must still exist")
	}
}

func TestVariantUpdateKeepsOutputOnlyAndPin(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	k := e.Surface.Find(fakectl.Rule6Token)
	in := pm(map[string]any{"name": "u6", "action": "BLOCK"})
	id, _, err := e.Create(ctx, k, in)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.TrimPrefix(id, "private:")
	o := srv.Object("rules", raw)
	o["slot"] = 7 // the ordering renumbered it
	srv.Put("rules", o)
	srv.Requests()
	if _, err := e.Update(ctx, k, id, in, pm(map[string]any{"name": "u6", "action": "ALLOW"})); err != nil {
		t.Fatal(err)
	}
	var put map[string]any
	for _, r := range srv.Requests() {
		if r.Method == "PUT" {
			put = r.Body.(map[string]any)
		}
	}
	if put["slot"] != float64(7) || put["kind"] != "V6" || put["ext_id"] == nil || put["action"] != "ALLOW" {
		t.Errorf("PUT body must keep the live output-only fields and the pin: %v", put)
	}
	if err := e.Delete(ctx, k, id); err != nil {
		t.Fatal(err)
	}
	if srv.Count("rules") != 0 {
		t.Error("delete left the rule")
	}
}

func TestCheckOutputOnlyNeverRequired(t *testing.T) {
	e, _ := setup(t)
	k := e.Surface.Find(fakectl.Rule6Token)
	if fs := e.Check(k, nil, pm(map[string]any{"name": "c", "action": "ALLOW"})); len(fs) != 0 {
		t.Errorf("Check: %s", failures(fs))
	}
}

// --- natural-key uniqueness --------------------------------------------------------

func TestCreateRefusesADuplicateNaturalKey(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	for _, tok := range []string{fakectl.ThingToken, fakectl.CrewToken} {
		k := e.Surface.Find(tok)
		if _, _, err := e.Create(ctx, k, pm(map[string]any{"name": "dup"})); err != nil {
			t.Fatalf("%s: %v", tok, err)
		}
		_, _, err := e.Create(ctx, k, pm(map[string]any{"name": "dup"}))
		if err == nil || !strings.Contains(err.Error(), "already exists (id private:") || !strings.Contains(err.Error(), "import it") {
			t.Errorf("%s: a second create with the same key: %v; want already exists + import hint", tok, err)
		}
	}
	if srv.Count("rest/crew") != 1 || srv.Count("things") != 1 {
		t.Error("the refused create must not reach the controller")
	}
}

// --- classic collection --------------------------------------------------------

func TestClassicCollectionLifecycle(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	k := e.Surface.Find(fakectl.CrewToken)
	in := pm(map[string]any{"name": "c1", "rateMax": 500})
	id, out, err := e.Create(ctx, k, in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "private:") || out["rateMax"].NumberValue() != 500 {
		t.Fatalf("create: %s %v", id, out)
	}
	raw := strings.TrimPrefix(id, "private:")
	for _, r := range srv.Requests() {
		if !strings.HasPrefix(r.Path, "/api/s/default/rest/crew") {
			t.Errorf("classic path: %s %s", r.Method, r.Path)
		}
	}
	// Read: refresh is a no-op; an unmanaged field is kept.
	o := srv.Object("rest/crew", raw)
	o["attr_hidden"] = true
	srv.Put("rest/crew", o)
	r, err := e.Read(ctx, k, id, in, false)
	if err != nil || r == nil || r.Inputs["rateMax"].NumberValue() != 500 {
		t.Fatalf("read: %v %v", r, err)
	}
	// Update: dropping rateMax resets it to its default (G-Q1).
	if _, err := e.Update(ctx, k, id, in, pm(map[string]any{"name": "c1"})); err != nil {
		t.Fatal(err)
	}
	o = srv.Object("rest/crew", raw)
	if o["rate_max"] != float64(-1) || o["attr_hidden"] != true {
		t.Errorf("after the drop: %v; want rate_max -1 and the unmanaged field kept", o)
	}
	// A refused value is a typed error naming the field and pattern.
	_, err = e.Update(ctx, k, id, pm(map[string]any{"name": "c1"}), pm(map[string]any{"name": "c1", "rateMax": 1}))
	var ce *binding.ClassicError
	if !errors.As(err, &ce) || ce.Field != "rate_max" || !strings.Contains(err.Error(), "field rate_max must match") {
		t.Errorf("refused value: %v", err)
	}
	// Delete, then a second delete (IdInvalid, confirmed absent by a list) is fine.
	if err := e.Delete(ctx, k, id); err != nil {
		t.Fatal(err)
	}
	if err := e.Delete(ctx, k, id); err != nil {
		t.Errorf("deleting a gone classic object: %v", err)
	}
	// A gone object reads as gone (200 with data:[]).
	if r, err := e.Read(ctx, k, id, in, false); r != nil || err != nil {
		t.Errorf("read of a gone object: %v %v", r, err)
	}
}

func TestClassicIdInvalidIsGoneOnlyWhenTheListLacksIt(t *testing.T) {
	ctx := context.Background()
	srv := fakectl.Start("default")
	t.Cleanup(srv.Close)
	k := fakectl.Surface().Find(fakectl.CrewToken)
	c := &binding.Client{BaseURL: srv.URL, Site: "default"}
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
	c2 := &binding.Client{BaseURL: held.URL, Site: "default"}
	if _, err := c2.Delete(ctx, k, "held"); err == nil || !strings.Contains(err.Error(), "api.err.IdInvalid") {
		t.Errorf("a refused DELETE of a listed id must be an error: %v", err)
	}
}

// --- classic settings singleton ---------------------------------------------------

func TestSingletonAdoptUpdateRelease(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	k := e.Surface.Find(fakectl.TickToken)
	in := pm(map[string]any{"mode": "manual"})
	id, out, err := e.Create(ctx, k, in)
	if err != nil {
		t.Fatal(err)
	}
	if id != "private:"+fakectl.TickID {
		t.Fatalf("adopt id %s", id)
	}
	if out["mode"].StringValue() != "manual" || out["server"].StringValue() != "time.example.test" {
		t.Errorf("adopt outputs: %v", out)
	}
	for _, r := range srv.Requests() {
		if r.Method == "POST" || r.Method == "DELETE" {
			t.Errorf("a singleton is never created or deleted: %s %s", r.Method, r.Path)
		}
	}
	if o := srv.Object("rest/setting/tick", fakectl.TickID); o["mode"] != "manual" || o["key"] != "tick" {
		t.Errorf("adopt PUT: %v", o)
	}
	// Refresh: no-op; drift on a managed field shows; unmanaged changes do not.
	o := srv.Object("rest/setting/tick", fakectl.TickID)
	o["server"] = "other.example.test"
	srv.Put("rest/setting/tick", o)
	r, err := e.Read(ctx, k, id, in, false)
	if err != nil || r.Inputs["mode"].StringValue() != "manual" || len(r.Inputs) != 1 {
		t.Fatalf("refresh: %v %v", r, err)
	}
	// Import by the section name and by stored id.
	for _, imp := range []string{"tick", id} {
		r, err := e.Read(ctx, k, imp, nil, true)
		if err != nil || r.ID != id {
			t.Errorf("import %q: %v %v", imp, r, err)
		}
	}
	if _, err := e.Read(ctx, k, "other", nil, true); err == nil || !strings.Contains(err.Error(), `import it by "tick"`) {
		t.Errorf("import by another name: %v", err)
	}
	// Drop the managed field: reset to its default (G-Q1).
	if _, err := e.Update(ctx, k, id, in, resource.PropertyMap{}); err != nil {
		t.Fatal(err)
	}
	if o := srv.Object("rest/setting/tick", fakectl.TickID); o["mode"] != "auto" || o["server"] != "other.example.test" {
		t.Errorf("drop-reset: %v", o)
	}
	// Release: no call, a warning.
	var warned string
	e.Warn = func(m string) { warned = m }
	srv.Requests()
	if err := e.Delete(ctx, k, id); err != nil {
		t.Fatal(err)
	}
	if len(srv.Requests()) != 0 || !strings.Contains(warned, "released") {
		t.Errorf("release: requests sent or no warning (%q)", warned)
	}
	if srv.Object("rest/setting/tick", fakectl.TickID) == nil {
		t.Error("release must leave the document")
	}
}

func TestSingletonRefusedValueIsTyped(t *testing.T) {
	ctx := context.Background()
	e, _ := setup(t)
	k := e.Surface.Find(fakectl.TickToken)
	_, _, err := e.Create(ctx, k, pm(map[string]any{"mode": "weekly"}))
	var ce *binding.ClassicError
	if !errors.As(err, &ce) || ce.Msg != "api.err.InvalidPayload" || ce.Field != "mode" {
		t.Errorf("refused enum: %v", err)
	}
}
