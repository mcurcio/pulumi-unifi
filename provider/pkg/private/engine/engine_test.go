package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/binding"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/internal/fakectl"
)

func setup(t *testing.T) (*engine.Engine, *fakectl.Server) {
	t.Helper()
	srv := fakectl.Start("default")
	t.Cleanup(srv.Close)
	s := fakectl.Surface()
	return &engine.Engine{Surface: s, Binding: &binding.Client{BaseURL: srv.URL, Site: "default"}}, srv
}

func pm(m map[string]any) resource.PropertyMap { return resource.NewPropertyMapFromMap(m) }

func failures(fs []engine.Failure) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Property+": "+f.Reason)
	}
	return strings.Join(out, "\n")
}

func TestParseID(t *testing.T) {
	if id, err := engine.ParseID("private:abc"); err != nil || id != "abc" {
		t.Fatalf("got %q %v", id, err)
	}
	for _, bad := range []string{"abc", "official:abc", "mac:aa:bb", "private:"} {
		if _, err := engine.ParseID(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestCheckDeclaredValues(t *testing.T) {
	e, _ := setup(t)
	k := e.Surface.Find(fakectl.ThingToken)
	fails := e.Check(k, nil, pm(map[string]any{
		"name": "", "rateKbps": 5000, "mode": "MEDIUM", "code": "abc",
		"window": map[string]any{"start": "08:00"},
	}))
	got := failures(fails)
	for _, want := range []string{
		"name: name must not be empty",
		"rateKbps: rateKbps must be at most 1000",
		"mode: mode must be one of FAST, SLOW",
		"code: code must match [A-Z]{3}",
		"mode is required", // window.mode
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing failure %q in:\n%s", want, got)
		}
	}
	if fails := e.Check(k, nil, pm(map[string]any{"name": "ok"})); len(fails) != 0 {
		t.Errorf("valid inputs: %s", failures(fails))
	}
	// Missing required.
	if got := failures(e.Check(k, nil, pm(map[string]any{}))); !strings.Contains(got, "name is required") {
		t.Errorf("want name required, got %s", got)
	}
	// Wrong type.
	if got := failures(e.Check(k, nil, pm(map[string]any{"name": "x", "rateKbps": "fast"}))); !strings.Contains(got, "want an integer") {
		t.Errorf("want a type failure, got %s", got)
	}
}

// Rule 1: a value equal to the old input (e.g. read back from the controller)
// is never rejected, even when it breaks a declared rule.
func TestCheckNeverRejectsReadBackValues(t *testing.T) {
	e, _ := setup(t)
	k := e.Surface.Find(fakectl.ThingToken)
	olds := pm(map[string]any{"name": "x", "code": "lowercase-from-controller"})
	if fails := e.Check(k, olds, olds.Copy()); len(fails) != 0 {
		t.Errorf("read-back values rejected: %s", failures(fails))
	}
	news := pm(map[string]any{"name": "x", "code": "still-bad"})
	if fails := e.Check(k, olds, news); len(fails) != 1 {
		t.Errorf("a changed value must be validated: %s", failures(fails))
	}
}

// G-Q1: dropping a managed path with no default is a Check error naming it;
// one with a default (or a nullable one) is fine.
func TestCheckDroppedNeedsDefault(t *testing.T) {
	e, _ := setup(t)
	k := e.Surface.Find(fakectl.ThingToken)
	olds := pm(map[string]any{"name": "x", "rateKbps": 10, "mode": "SLOW", "note": "n",
		"window": map[string]any{"mode": "CUSTOM", "start": "08:00", "end": "17:00"}})
	news := pm(map[string]any{"name": "x", "window": map[string]any{"mode": "CUSTOM"}})
	got := failures(e.Check(k, olds, news))
	for _, want := range []string{"rateKbps was removed", "window.end was removed"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, not := range []string{"mode was removed", "note was removed", "window.start was removed"} {
		if strings.Contains(got, not) {
			t.Errorf("unexpected %q in:\n%s", not, got)
		}
	}
}

func TestDiffManagedPathsOnly(t *testing.T) {
	e, _ := setup(t)
	k := e.Surface.Find(fakectl.ThingToken)
	olds := pm(map[string]any{"name": "x", "mode": "FAST", "code": "ABC", "window": map[string]any{"mode": "ALWAYS"}})
	r, err := e.Diff(k, olds, olds.Copy())
	if err != nil || len(r.Changed) != 0 {
		t.Fatalf("no change expected: %+v %v", r, err)
	}
	news := pm(map[string]any{"name": "x", "mode": "SLOW", "code": "XYZ", "tags": []any{"a"}, "window": map[string]any{"mode": "ALWAYS"}})
	r, err = e.Diff(k, olds, news)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.Changed, ",") != "code,mode,tags" {
		t.Errorf("changed = %v", r.Changed)
	}
	if strings.Join(r.Replaces, ",") != "code" {
		t.Errorf("replaces = %v (code is createOnly)", r.Replaces)
	}
	if r.Detailed["tags"] != "add" || r.Detailed["mode"] != "update" {
		t.Errorf("detailed = %v", r.Detailed)
	}
	// A dropped path is a delete.
	r, _ = e.Diff(k, olds, pm(map[string]any{"name": "x", "code": "ABC", "window": map[string]any{"mode": "ALWAYS"}}))
	if r.Detailed["mode"] != "delete" {
		t.Errorf("dropped mode: detailed = %v", r.Detailed)
	}
	// A nested drop is reported at its own path.
	r, _ = e.Diff(k, pm(map[string]any{"name": "x", "window": map[string]any{"mode": "ALWAYS", "start": "1"}}),
		pm(map[string]any{"name": "x", "window": map[string]any{"mode": "ALWAYS"}}))
	if len(r.Detailed) != 1 || r.Detailed["window.start"] != "delete" || strings.Join(r.Changed, ",") != "window" {
		t.Errorf("nested drop: %+v", r)
	}
	// Unknown values are changes.
	news = olds.Copy()
	news["mode"] = resource.MakeComputed(resource.NewStringProperty(""))
	r, _ = e.Diff(k, olds, news)
	if strings.Join(r.Changed, ",") != "mode" {
		t.Errorf("unknown: changed = %v", r.Changed)
	}
}

func TestCreateReadUpdateDelete(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	k := e.Surface.Find(fakectl.ThingToken)
	in := pm(map[string]any{"name": "t1", "rateKbps": 100, "mode": "SLOW", "psk": "s3cret",
		"window": map[string]any{"mode": "CUSTOM", "start": "08:00", "end": "17:00"}})
	id, out, err := e.Create(ctx, k, in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "private:") {
		t.Fatalf("id %q", id)
	}
	reqs := srv.Requests()
	post := reqs[0]
	if post.Method != "POST" || post.Path != "/v2/api/site/default/things" {
		t.Fatalf("create request %s %s", post.Method, post.Path)
	}
	body := post.Body.(map[string]any)
	if len(body) != 5 || body["rate_kbps"].(float64) != 100 || body["window"].(map[string]any)["start"] != "08:00" {
		t.Errorf("create body must hold only the inputs, by wire name: %v", body)
	}
	if out["createdAt"].StringValue() != "2026-01-01T00:00:00Z" {
		t.Errorf("read-only output missing: %v", out)
	}
	if !out["psk"].IsSecret() || out["psk"].SecretValue().Element.StringValue() != "s3cret" {
		t.Errorf("a declared, not-returned secret keeps its input value, as a secret: %v", out["psk"])
	}
	if _, ok := out["extra"]; ok {
		t.Error("unmodelled fields are not outputs")
	}
	raw := strings.TrimPrefix(id, "private:")

	// Refresh: inputs are the live object projected onto the managed paths.
	live := srv.Object("things", raw)
	live["mode"] = "FAST"     // drift on a managed path
	live["tags"] = []any{"x"} // an unmanaged field
	srv.Put("things", live)
	r, err := e.Read(ctx, k, id, in, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Inputs["mode"].StringValue() != "FAST" {
		t.Errorf("managed drift not read back: %v", r.Inputs["mode"])
	}
	if _, ok := r.Inputs["tags"]; ok {
		t.Error("an unmanaged field must not enter the inputs")
	}
	if !r.Inputs["psk"].IsSecret() || r.Inputs["psk"].SecretValue().Element.StringValue() != "s3cret" {
		t.Errorf("a not-returned managed secret keeps its old input, as a secret: %v", r.Inputs["psk"])
	}
	if !r.Outputs["psk"].IsSecret() {
		t.Errorf("a declared secret output stays a secret: %v", r.Outputs["psk"])
	}
	if d, _ := e.Diff(k, r.Inputs, in); strings.Join(d.Changed, ",") != "mode" {
		t.Errorf("after refresh only the drifted path diffs: %v", d.Changed)
	}

	// Update (rule 2, G-Q1): change rateKbps, drop mode (default FAST... set
	// SLOW live first) and window.start (nullable); the unmanaged tags and the
	// unmodelled extra stay.
	live = srv.Object("things", raw)
	live["mode"] = "SLOW"
	srv.Put("things", live)
	srv.Requests()
	news := pm(map[string]any{"name": "t1", "rateKbps": 200, "psk": "s3cret",
		"window": map[string]any{"mode": "CUSTOM", "end": "17:00"}})
	if fails := e.Check(k, in, news); len(fails) != 0 {
		t.Fatalf("check: %s", failures(fails))
	}
	if _, err := e.Update(ctx, k, id, in, news); err != nil {
		t.Fatal(err)
	}
	reqs = srv.Requests()
	if reqs[0].Method != "GET" || reqs[1].Method != "PUT" || reqs[1].Path != "/v2/api/site/default/things/"+raw {
		t.Fatalf("update must read fresh, then PUT the item: %+v", reqs)
	}
	put := reqs[1].Body.(map[string]any)
	if _, ok := put["created_at"]; ok {
		t.Error("read-only fields are not sent")
	}
	if put["_id"] != raw {
		t.Error("the id travels with the body")
	}
	got := srv.Object("things", raw)
	if got["rate_kbps"].(float64) != 200 || got["mode"] != "FAST" || got["window"].(map[string]any)["start"] != nil ||
		got["window"].(map[string]any)["end"] != "17:00" {
		t.Errorf("managed paths: %v", got)
	}
	if got["extra"] != "controller-only" || len(got["tags"].([]any)) != 1 {
		t.Errorf("unmanaged fields must keep their live values: %v", got)
	}

	// Delete, then delete again (404 = success); a read of a gone object is nil.
	if err := e.Delete(ctx, k, id); err != nil {
		t.Fatal(err)
	}
	if err := e.Delete(ctx, k, id); err != nil {
		t.Errorf("second delete: %v", err)
	}
	if r, err := e.Read(ctx, k, id, in, false); err != nil || r != nil {
		t.Errorf("read after delete: %v %v", r, err)
	}
	// An id naming another API is refused.
	if err := e.Delete(ctx, k, "official:"+raw); err == nil {
		t.Error("an official id must not reach the private API")
	}
}

// Group: item GET is 405, so reads list and filter; the list lives at
// another path; create takes a list.
func TestListFilterAndCreateList(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	k := e.Surface.Find(fakectl.GroupToken)
	srv.Put("groups", map[string]any{"name": "other", "members": []any{"m"}, "kind": "A"})
	id, out, err := e.Create(ctx, k, pm(map[string]any{"name": "g1", "members": []any{"a", "b"}, "kind": "B"}))
	if err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if _, isList := reqs[0].Body.([]any); !isList {
		t.Errorf("create body must be a list: %v", reqs[0].Body)
	}
	for _, r := range reqs {
		if r.Method == "GET" && strings.HasPrefix(r.Path, "/v2/api/site/default/groups/") {
			t.Errorf("item GET used on a 405 kind: %s", r.Path)
		}
	}
	if out["name"].StringValue() != "g1" {
		t.Errorf("outputs %v", out)
	}
	r, err := e.Read(ctx, k, id, pm(map[string]any{"name": "g1", "kind": "B"}), false)
	if err != nil || r == nil {
		t.Fatalf("read: %v %v", r, err)
	}
	if r.ID != id || len(r.Inputs) != 2 {
		t.Errorf("read %+v", r)
	}
	reqs = srv.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/v2/api/site/default/groups-all" {
		t.Errorf("read must list at the list path: %+v", reqs)
	}
	if _, err := e.Update(ctx, k, id, pm(map[string]any{"name": "g1", "kind": "B"}), pm(map[string]any{"name": "g1", "kind": "A"})); err != nil {
		t.Fatal(err)
	}
	if got := srv.Object("groups", strings.TrimPrefix(id, "private:")); got["kind"] != "A" || len(got["members"].([]any)) != 2 {
		t.Errorf("after update %v", got)
	}
}

// Rule 3: import by natural key; zero or several matches is an error naming
// the candidates.
func TestImportByNaturalKey(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	k := e.Surface.Find(fakectl.ThingToken)
	a := srv.Put("things", map[string]any{"name": "alpha", "mode": "SLOW", "created_at": "t", "psk": "hidden"})
	srv.Put("things", map[string]any{"name": "dup"})
	srv.Put("things", map[string]any{"name": "dup"})

	for _, id := range []string{"alpha", "name=alpha", "private:" + a} {
		r, err := e.Read(ctx, k, id, nil, true)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if r.ID != "private:"+a {
			t.Errorf("%s: id %s", id, r.ID)
		}
		// The adopted managed paths are the natural key + the required
		// fields (window.mode only within a required window; window is not
		// required here, so it is not adopted).
		if r.Inputs["name"].StringValue() != "alpha" || len(r.Inputs) != 1 {
			t.Errorf("%s: inputs %v, want only the natural key", id, r.Inputs)
		}
		if r.Outputs["mode"].StringValue() != "SLOW" {
			t.Errorf("%s: outputs %v keep every returned field", id, r.Outputs)
		}
		// A program that declares more than was adopted, at the live values,
		// previews with no changes; a different value is a change.
		same := pm(map[string]any{"name": "alpha", "mode": "SLOW"})
		if d, err := e.DiffLive(k, r.Inputs, same, r.Outputs); err != nil || len(d.Changed) != 0 {
			t.Errorf("%s: declaring live values after import: %v %v", id, d.Changed, err)
		}
		other := pm(map[string]any{"name": "alpha", "mode": "FAST"})
		if d, _ := e.DiffLive(k, r.Inputs, other, r.Outputs); strings.Join(d.Changed, ",") != "mode" || d.Detailed["mode"] != "add" {
			t.Errorf("%s: declaring a new value after import: %v %v", id, d.Changed, d.Detailed)
		}
		if _, ok := r.Inputs["createdAt"]; ok {
			t.Errorf("%s: read-only fields are not inputs", id)
		}
		if _, ok := r.Inputs["psk"]; ok {
			t.Errorf("%s: an undeclared secret is never read into state", id)
		}
		if _, ok := r.Outputs["psk"]; ok {
			t.Errorf("%s: an undeclared secret is never an output", id)
		}
	}
	if _, err := e.Read(ctx, k, "nope", nil, true); err == nil || !strings.Contains(err.Error(), "candidates: alpha, dup, dup") {
		t.Errorf("zero matches: %v", err)
	}
	if _, err := e.Read(ctx, k, "dup", nil, true); err == nil || !strings.Contains(err.Error(), "2 unifi:network:Thing objects") {
		t.Errorf("two matches: %v", err)
	}
	if _, err := e.Read(ctx, k, "official:"+a, nil, true); err == nil || !strings.Contains(err.Error(), "names another API") {
		t.Errorf("other API id: %v", err)
	}
}

func TestMergeBodyLeafReplacesSubtree(t *testing.T) {
	k := fakectl.Surface().Find(fakectl.ThingToken)
	live := map[string]any{"_id": "1", "name": "x", "window": map[string]any{"mode": "CUSTOM", "start": "1", "end": "2"}, "created_at": "t"}
	olds := map[string]any{"name": "x", "window": map[string]any{"mode": "CUSTOM", "start": "1"}}
	news := map[string]any{"name": "x", "window": map[string]any{"mode": "ALWAYS"}}
	body, err := engine.MergeBody(k, live, olds, news)
	if err != nil {
		t.Fatal(err)
	}
	w := body["window"].(map[string]any)
	if w["mode"] != "ALWAYS" || w["start"] != nil || w["end"] != "2" {
		t.Errorf("window = %v (managed mode set, dropped start nulled, unmanaged end kept)", w)
	}
	if _, ok := body["created_at"]; ok {
		t.Error("read-only not stripped")
	}
	// A dropped path with no default cannot be merged.
	if _, err := engine.MergeBody(k, live, map[string]any{"name": "x", "rate_kbps": 1}, map[string]any{"name": "x"}); err == nil {
		t.Error("want a G-Q1 error")
	}
}

// A declared {} for an object manages no path: update keeps the live
// object's unmanaged subfields (rule 2), and a refresh neither adopts them
// nor makes the next check fail G-Q1.
func TestEmptyObjectIsAContainer(t *testing.T) {
	ctx := context.Background()
	e, srv := setup(t)
	k := e.Surface.Find(fakectl.ThingToken)
	in := pm(map[string]any{"name": "e1", "window": map[string]any{"mode": "CUSTOM", "start": "08:00", "end": "17:00"}})
	id, _, err := e.Create(ctx, k, in)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.TrimPrefix(id, "private:")
	if got := engine.LeafPaths(k, map[string]any{"name": "x", "window": map[string]any{}}); len(got) != 1 || got[0].String() != "name" {
		t.Errorf("leaf paths of a declared {}: %v", got)
	}

	// Update from a program that declares window: {} (olds manage nothing in
	// window either, so nothing is dropped).
	olds := pm(map[string]any{"name": "e1", "window": map[string]any{}})
	news := pm(map[string]any{"name": "e1", "window": map[string]any{}})
	if _, err := e.Update(ctx, k, id, olds, news); err != nil {
		t.Fatal(err)
	}
	w := srv.Object("things", raw)["window"].(map[string]any)
	if w["mode"] != "CUSTOM" || w["start"] != "08:00" || w["end"] != "17:00" {
		t.Errorf("a declared {} must keep the live subfields: %v", w)
	}

	// Refresh, then check and diff the same program.
	r, err := e.Read(ctx, k, id, news, false)
	if err != nil {
		t.Fatal(err)
	}
	rw, ok := r.Inputs["window"]
	if !ok || !rw.IsObject() || len(rw.ObjectValue()) != 0 {
		t.Errorf("refresh must keep window as a declared {} without adopting subfields: %v", r.Inputs)
	}
	for _, f := range e.Check(k, r.Inputs, news) {
		if strings.Contains(f.Reason, "G-Q1") {
			t.Errorf("check after refresh: %s: %s", f.Property, f.Reason)
		}
	}
	if d, err := e.Diff(k, r.Inputs, news); err != nil || len(d.Changed) != 0 {
		t.Errorf("diff after refresh: %v %v", d.Changed, err)
	}

	// Going from managed subfields to {} drops them (G-Q1 applies).
	if got := engine.Dropped(k, map[string]any{"window": map[string]any{"start": "1"}}, map[string]any{"window": map[string]any{}}); len(got) != 1 || got[0].String() != "window.start" {
		t.Errorf("dropped under a declared {}: %v", got)
	}
}
