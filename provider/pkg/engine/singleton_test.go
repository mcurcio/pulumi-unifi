package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// sg is the AclRuleOrdering kind as surface.json carries it: a singleton
// keyed by its site with one leadingBlock field (Kind data, no name check).
var sg = &Kind{Token: "unifi:sites/v1:AclRuleOrdering", API: APIOfficial, Shape: ShapeSingleton, KeyedBySite: true,
	ItemPath: "/v1/sites/{siteId}/acl-rules/ordering", ItemGet: true,
	Fields: map[string]*Field{"orderedAclRuleIds": {Wire: "orderedAclRuleIds", Property: "orderedAclRuleIds",
		Type: "array", Items: &Field{Type: "string"}, Required: true, Merge: MergeLeadingBlock}}}

// fakeOrder is the official Binding over an in-memory order with the server's
// PUT rule (every live id, exactly once).
type fakeOrder struct {
	unbound
	live      []string
	puts      [][]string
	beforePut func(f *fakeOrder)
	sites     map[string]string
}

func (f *fakeOrder) API() string { return "official" }
func (f *fakeOrder) SiteID(_ context.Context, site string) (string, error) {
	if id, ok := f.sites[site]; ok {
		return id, nil
	}
	return site, nil
}
func (f *fakeOrder) Get(context.Context, *Kind, string) (map[string]any, bool, error) {
	var l []any
	for _, id := range f.live {
		l = append(l, id)
	}
	return map[string]any{"orderedAclRuleIds": l}, true, nil
}
func (f *fakeOrder) Update(_ context.Context, _ *Kind, _ string, body map[string]any) (any, error) {
	if f.beforePut != nil {
		f.beforePut(f)
	}
	ids := body["orderedAclRuleIds"].([]string)
	f.puts = append(f.puts, ids)
	if len(ids) != len(f.live) {
		return nil, fmt.Errorf("%w: 400", ErrIncomplete)
	}
	f.live = append([]string(nil), ids...)
	return body, nil
}

// eng is an engine whose official binding is f.
func eng(f *fakeOrder) *Engine { return &Engine{Bindings: map[string]Binding{APIOfficial: f}} }

func ids(xs ...string) resource.PropertyValue { return idList(xs) }

func in(managed resource.PropertyValue, unlisted string) resource.PropertyMap {
	m := resource.PropertyMap{"orderedAclRuleIds": managed}
	if unlisted != "" {
		m["unlisted"] = resource.NewStringProperty(unlisted)
	}
	return m
}

func TestCompose(t *testing.T) {
	for _, c := range []struct {
		managed, live []string
		unlisted      string
		want          []string
		err           string
	}{
		{[]string{"r2", "r1"}, []string{"r1", "r2", "u1"}, UnlistedLast, []string{"r2", "r1", "u1"}, ""},
		{[]string{"r1"}, []string{"u1", "r1", "u2"}, UnlistedLast, []string{"r1", "u1", "u2"}, ""},
		{[]string{"gone"}, []string{"r1"}, UnlistedLast, []string{"gone", "r1"}, ""}, // the server names it
		{[]string{"r1"}, []string{"r1", "u1", "u2"}, UnlistedReject, nil, "u1, u2"},
		{[]string{"r1", "r2"}, []string{"r2", "r1"}, UnlistedReject, []string{"r1", "r2"}, ""},
	} {
		got, err := Compose(c.managed, c.live, c.unlisted)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("Compose(%v, %v, %s): err %v, want one naming %s", c.managed, c.live, c.unlisted, err, c.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("Compose(%v, %v, %s) = %v, %v; want %v", c.managed, c.live, c.unlisted, got, err, c.want)
		}
	}
}

func TestProject(t *testing.T) {
	if got := Project([]string{"a", "b", "c"}, 2); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Project = %v", got)
	}
	if got := Project([]string{"a"}, 3); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("Project (gone managed id) = %v", got)
	}
}

func TestCheck(t *testing.T) {
	for name, c := range map[string]struct {
		in   resource.PropertyMap
		want string
	}{
		"ok":          {in(ids("a", "b"), ""), ""},
		"unknown ids": {in(resource.NewArrayProperty([]resource.PropertyValue{resource.MakeComputed(resource.NewStringProperty(""))}), ""), ""},
		"missing":     {resource.PropertyMap{}, "required"},
		"duplicate":   {in(ids("a", "a"), ""), "twice"},
		"private id":  {in(ids("private:65f0"), ""), "aclRuleId"},
		"bad policy":  {in(ids("a"), "FIRST"), "LAST or REJECT"},
	} {
		fails := eng(&fakeOrder{}).Check(sg, nil, c.in)
		var got []string
		for _, f := range fails {
			got = append(got, f.Reason)
		}
		joined := strings.Join(got, "; ")
		if c.want == "" && len(fails) != 0 || c.want != "" && !strings.Contains(joined, c.want) {
			t.Errorf("%s: failures %q, want %q", name, joined, c.want)
		}
	}
}

func TestCreateAdoptsAndKeepsUnmanagedTail(t *testing.T) {
	f := &fakeOrder{live: []string{"r1", "u1", "r2"}, sites: map[string]string{"default": "site-uuid"}}
	o := eng(f)
	o.Site = func() string { return "default" }
	id, out, err := o.Create(context.Background(), sg, in(ids("r2", "r1"), ""))
	if err != nil {
		t.Fatal(err)
	}
	if id != "official:site-uuid" {
		t.Errorf("id %s", id)
	}
	if !reflect.DeepEqual(f.live, []string{"r2", "r1", "u1"}) {
		t.Errorf("live after create %v", f.live)
	}
	eff, _ := stringList(out["effectiveOrder"])
	if !reflect.DeepEqual(eff, f.live) || out["unlisted"].StringValue() != UnlistedLast {
		t.Errorf("outputs %v", out)
	}
}

func TestCreateRetriesOnceOnIncomplete(t *testing.T) {
	f := &fakeOrder{live: []string{"r1"}}
	n := 0
	f.beforePut = func(f *fakeOrder) {
		if n++; n == 1 {
			f.live = append(f.live, "late") // created elsewhere between GET and PUT
		}
	}
	if _, _, err := eng(f).Create(context.Background(), sg, in(ids("r1"), "")); err != nil {
		t.Fatalf("one retry must cover a rule created between GET and PUT: %v", err)
	}
	if len(f.puts) != 2 || !reflect.DeepEqual(f.live, []string{"r1", "late"}) {
		t.Errorf("puts %v live %v", f.puts, f.live)
	}
	// A second incomplete answer is an error.
	f2 := &fakeOrder{live: []string{"r1"}}
	f2.beforePut = func(f *fakeOrder) { f.live = append(f.live, fmt.Sprint("x", len(f.live))) }
	if _, _, err := eng(f2).Create(context.Background(), sg, in(ids("r1"), "")); !errors.Is(err, ErrIncomplete) {
		t.Errorf("err %v, want ErrIncomplete after one retry", err)
	}
}

func TestRejectFailsAndNamesUnlisted(t *testing.T) {
	f := &fakeOrder{live: []string{"r1", "u1"}}
	_, err := eng(f).Update(context.Background(), sg, "official:s", nil, in(ids("r1"), UnlistedReject))
	if err == nil || !strings.Contains(err.Error(), "u1") || len(f.puts) != 0 {
		t.Errorf("err %v puts %v", err, f.puts)
	}
}

func TestReadProjectsTheLeadingBlock(t *testing.T) {
	f := &fakeOrder{live: []string{"u1", "r1", "r2"}}
	o := eng(f)
	r, err := o.Read(context.Background(), sg, "official:s", in(ids("r1", "r2"), UnlistedLast), false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := stringList(r.Inputs["orderedAclRuleIds"])
	if !reflect.DeepEqual(got, []string{"u1", "r1"}) {
		t.Errorf("projected inputs %v: an unmanaged rule ahead of a managed one must show as drift", got)
	}
	d, _ := o.DiffLive(sg, r.Inputs, in(ids("r1", "r2"), UnlistedLast), nil)
	if !reflect.DeepEqual(d.Changed, []string{"orderedAclRuleIds"}) || len(d.Replaces) != 0 {
		t.Errorf("diff %+v", d)
	}
	// No drift when the managed block leads.
	f.live = []string{"r1", "r2", "u1"}
	r, _ = o.Read(context.Background(), sg, "official:s", in(ids("r1", "r2"), UnlistedLast), false)
	if d, _ := o.DiffLive(sg, r.Inputs, in(ids("r1", "r2"), UnlistedLast), nil); len(d.Changed) != 0 {
		t.Errorf("no-op diff %+v", d)
	}
}

func TestImportAdoptsWholeOrder(t *testing.T) {
	f := &fakeOrder{live: []string{"a", "b"}, sites: map[string]string{"default": "uuid-1"}}
	r, err := eng(f).Read(context.Background(), sg, "default", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := stringList(r.Inputs["orderedAclRuleIds"])
	if r.ID != "official:uuid-1" || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("import id %s inputs %v", r.ID, got)
	}
	if _, err := eng(f).Read(context.Background(), sg, "default", nil, false); err == nil {
		t.Error("a refresh of a non-stored id must fail")
	}
}

func TestDiffSiteReplaces(t *testing.T) {
	o := eng(&fakeOrder{})
	a, b := in(ids("r1"), ""), in(ids("r1"), "")
	b["siteId"] = resource.NewStringProperty("other")
	d, _ := o.DiffLive(sg, a, b, nil)
	if !reflect.DeepEqual(d.Replaces, []string{"siteId"}) {
		t.Errorf("diff %+v", d)
	}
	if d, _ := o.DiffLive(sg, in(ids("r1"), ""), in(ids("r1"), UnlistedLast), nil); len(d.Changed) != 0 {
		t.Errorf("unlisted default must equal LAST: %+v", d)
	}
}

func TestDeleteReleases(t *testing.T) {
	f := &fakeOrder{live: []string{"a"}}
	var warned string
	o := eng(f)
	o.Warn = func(m string) { warned = m }
	if err := o.Delete(context.Background(), sg, "official:s"); err != nil {
		t.Fatal(err)
	}
	if len(f.puts) != 0 || !strings.Contains(warned, "released") {
		t.Errorf("delete must not call the API (puts %v) and must warn (%q)", f.puts, warned)
	}
}

// With REJECT, an unmanaged entry added after create must show on refresh, so
// that the next update fails in Compose and names it (review finding: Read
// projected only the first n ids, so REJECT never fired after create).
func TestRejectShowsUnlistedAfterCreateAsDrift(t *testing.T) {
	ctx := context.Background()
	f := &fakeOrder{live: []string{"r1", "r2"}}
	o := eng(f)
	prog := in(ids("r1", "r2"), UnlistedReject)
	id, _, err := o.Create(ctx, sg, prog)
	if err != nil {
		t.Fatal(err)
	}
	// No unlisted entry: the refresh is a no-op.
	r, err := o.Read(ctx, sg, id, prog, false)
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := o.DiffLive(sg, r.Inputs, prog, nil); len(d.Changed) != 0 {
		t.Fatalf("no unlisted entry: diff %v, want none", d.Changed)
	}
	f.live = append(f.live, "x") // created elsewhere, in the tail
	r, err = o.Read(ctx, sg, id, prog, false)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := o.DiffLive(sg, r.Inputs, prog, nil)
	if len(d.Changed) == 0 || d.Changed[0] != "orderedAclRuleIds" {
		t.Fatalf("unlisted tail entry with REJECT: diff %v, want orderedAclRuleIds changed", d.Changed)
	}
	if _, err := o.Update(ctx, sg, id, nil, prog); err == nil || !strings.Contains(err.Error(), "x") {
		t.Fatalf("update after drift: err %v, want REJECT naming x", err)
	}
	// LAST keeps the tail out of the projection.
	r, _ = o.Read(ctx, sg, id, in(ids("r1", "r2"), UnlistedLast), false)
	if d, _ := o.DiffLive(sg, r.Inputs, in(ids("r1", "r2"), UnlistedLast), nil); len(d.Changed) != 0 {
		t.Fatalf("LAST: diff %v, want none", d.Changed)
	}
}

// Stored ids name the binding's API (Binding.API()), not a constant: the
// singleton's id is official:<site id>, and a private id is refused for it.
func TestSiteKeyedStoredIDComesFromTheBinding(t *testing.T) {
	o := eng(&fakeOrder{live: []string{"a"}})
	if _, err := o.Read(context.Background(), sg, "private:s", in(ids("a"), ""), false); err == nil ||
		!strings.Contains(err.Error(), "official:<site id>") {
		t.Errorf("a private: id for an official kind: %v", err)
	}
	if got := o.StoredID(sg, "s"); got != "official:s" {
		t.Errorf("StoredID %s", got)
	}
}

func TestDefaultsFillUnlisted(t *testing.T) {
	o := eng(&fakeOrder{})
	got, changed := o.Defaults(sg, in(ids("a"), ""))
	if !changed || got["unlisted"].StringValue() != UnlistedLast {
		t.Errorf("defaults %v %v", got, changed)
	}
	if got, _ := o.Defaults(sg, in(ids("a"), UnlistedReject)); got["unlisted"].StringValue() != UnlistedReject {
		t.Errorf("a declared policy is kept: %v", got)
	}
	k := &Kind{Token: "unifi:network:X", API: APIV2}
	if _, changed := o.Defaults(k, resource.PropertyMap{}); changed {
		t.Error("a kind without engine-owned defaults is left as it is")
	}
}
