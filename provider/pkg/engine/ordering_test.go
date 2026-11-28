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

var sg = &Singleton{Token: "unifi:sites/v1:AclRuleOrdering", API: "official",
	Path: "/v1/sites/{siteId}/acl-rules/ordering", Property: "orderedAclRuleIds", Merge: MergeLeadingBlock}

// fakeOrder is an OrderBinding over an in-memory order with the server's PUT
// rule (every live id, exactly once).
type fakeOrder struct {
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
func (f *fakeOrder) GetOrder(context.Context, *Singleton, string) ([]string, error) {
	return append([]string(nil), f.live...), nil
}
func (f *fakeOrder) PutOrder(_ context.Context, _ *Singleton, _ string, ids []string) ([]string, error) {
	if f.beforePut != nil {
		f.beforePut(f)
	}
	f.puts = append(f.puts, ids)
	if len(ids) != len(f.live) {
		return nil, fmt.Errorf("%w: 400", ErrIncomplete)
	}
	f.live = append([]string(nil), ids...)
	return f.live, nil
}

func ids(xs ...string) resource.PropertyValue { return strings2(xs) }

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
	o := &Ordering{Binding: &fakeOrder{}}
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
		fails := o.Check(sg, c.in)
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
	o := &Ordering{Binding: f, Site: func() string { return "default" }}
	c, err := o.Create(context.Background(), sg, in(ids("r2", "r1"), ""))
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "official:site-uuid" {
		t.Errorf("id %s", c.ID)
	}
	if !reflect.DeepEqual(f.live, []string{"r2", "r1", "u1"}) {
		t.Errorf("live after create %v", f.live)
	}
	eff, _ := stringList(c.Outputs["effectiveOrder"])
	if !reflect.DeepEqual(eff, f.live) || c.Outputs["unlisted"].StringValue() != UnlistedLast {
		t.Errorf("outputs %v", c.Outputs)
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
	o := &Ordering{Binding: f}
	if _, err := o.Create(context.Background(), sg, in(ids("r1"), "")); err != nil {
		t.Fatalf("one retry must cover a rule created between GET and PUT: %v", err)
	}
	if len(f.puts) != 2 || !reflect.DeepEqual(f.live, []string{"r1", "late"}) {
		t.Errorf("puts %v live %v", f.puts, f.live)
	}
	// A second incomplete answer is an error.
	f2 := &fakeOrder{live: []string{"r1"}}
	f2.beforePut = func(f *fakeOrder) { f.live = append(f.live, fmt.Sprint("x", len(f.live))) }
	if _, err := (&Ordering{Binding: f2}).Create(context.Background(), sg, in(ids("r1"), "")); !errors.Is(err, ErrIncomplete) {
		t.Errorf("err %v, want ErrIncomplete after one retry", err)
	}
}

func TestRejectFailsAndNamesUnlisted(t *testing.T) {
	f := &fakeOrder{live: []string{"r1", "u1"}}
	_, err := (&Ordering{Binding: f}).Update(context.Background(), sg, "official:s", in(ids("r1"), UnlistedReject))
	if err == nil || !strings.Contains(err.Error(), "u1") || len(f.puts) != 0 {
		t.Errorf("err %v puts %v", err, f.puts)
	}
}

func TestReadProjectsTheLeadingBlock(t *testing.T) {
	f := &fakeOrder{live: []string{"u1", "r1", "r2"}}
	o := &Ordering{Binding: f}
	r, err := o.ReadOrder(context.Background(), sg, "official:s", in(ids("r1", "r2"), UnlistedLast), false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := stringList(r.Inputs["orderedAclRuleIds"])
	if !reflect.DeepEqual(got, []string{"u1", "r1"}) {
		t.Errorf("projected inputs %v: an unmanaged rule ahead of a managed one must show as drift", got)
	}
	d := o.DiffInputs(sg, r.Inputs, in(ids("r1", "r2"), UnlistedLast))
	if !reflect.DeepEqual(d.Changed, []string{"orderedAclRuleIds"}) || len(d.Replaces) != 0 {
		t.Errorf("diff %+v", d)
	}
	// No drift when the managed block leads.
	f.live = []string{"r1", "r2", "u1"}
	r, _ = o.ReadOrder(context.Background(), sg, "official:s", in(ids("r1", "r2"), UnlistedLast), false)
	if d := o.DiffInputs(sg, r.Inputs, in(ids("r1", "r2"), UnlistedLast)); len(d.Changed) != 0 {
		t.Errorf("no-op diff %+v", d)
	}
}

func TestImportAdoptsWholeOrder(t *testing.T) {
	f := &fakeOrder{live: []string{"a", "b"}, sites: map[string]string{"default": "uuid-1"}}
	r, err := (&Ordering{Binding: f}).ReadOrder(context.Background(), sg, "default", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := stringList(r.Inputs["orderedAclRuleIds"])
	if r.ID != "official:uuid-1" || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("import id %s inputs %v", r.ID, got)
	}
	if _, err := (&Ordering{Binding: f}).ReadOrder(context.Background(), sg, "default", nil, false); err == nil {
		t.Error("a refresh of a non-stored id must fail")
	}
}

func TestDiffSiteReplaces(t *testing.T) {
	o := &Ordering{Binding: &fakeOrder{}}
	a, b := in(ids("r1"), ""), in(ids("r1"), "")
	b["siteId"] = resource.NewStringProperty("other")
	d := o.DiffInputs(sg, a, b)
	if !reflect.DeepEqual(d.Replaces, []string{"siteId"}) {
		t.Errorf("diff %+v", d)
	}
	if d := o.DiffInputs(sg, in(ids("r1"), ""), in(ids("r1"), UnlistedLast)); len(d.Changed) != 0 {
		t.Errorf("unlisted default must equal LAST: %+v", d)
	}
}

func TestDeleteReleases(t *testing.T) {
	f := &fakeOrder{live: []string{"a"}}
	var warned string
	(&Ordering{Binding: f, Warn: func(m string) { warned = m }}).Delete(sg, "official:s")
	if len(f.puts) != 0 || !strings.Contains(warned, "released") {
		t.Errorf("delete must not call the API (puts %v) and must warn (%q)", f.puts, warned)
	}
}
