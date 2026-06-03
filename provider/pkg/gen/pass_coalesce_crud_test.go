package gen

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// newPathItem builds an openapi3.PathItem with the named verbs present (a verb's
// operation is non-nil iff requested), so a test can declare exactly which HTTP
// methods an item path exposes.
func newPathItem(get, put, patch, del bool) *openapi3.PathItem {
	pi := &openapi3.PathItem{}
	if get {
		pi.Get = &openapi3.Operation{}
	}
	if put {
		pi.Put = &openapi3.Operation{}
	}
	if patch {
		pi.Patch = &openapi3.Operation{}
	}
	if del {
		pi.Delete = &openapi3.Operation{}
	}
	return pi
}

// docWith builds an openapi3.T whose Paths hold the given items.
func docWith(items map[string]*openapi3.PathItem) *openapi3.T {
	paths := openapi3.NewPaths()
	for p, pi := range items {
		paths.Set(p, pi)
	}
	return &openapi3.T{Paths: paths}
}

// TestCoalescePassFillsItemVerbs is the focused unit proof of Phase 1: a
// create-only resource token gains R/D/P (and U where PATCH exists) from its
// collection's canonical item path, while a verb the spec does not expose stays
// nil and a verb pulschema already bound is never overwritten.
func TestCoalescePassFillsItemVerbs(t *testing.T) {
	const coll = "/v1/sites/{siteId}/wifi/broadcasts"
	const item = coll + "/{id}"

	doc := docWith(map[string]*openapi3.PathItem{
		coll: newPathItem(true, false, false, false), // collection: GET+POST (only POST matters here)
		item: newPathItem(true, true, false, true),   // item: GET, PUT, DELETE; no PATCH
	})
	tok := "unifi:sites/v1:Standard"
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{tok: {}},
		Functions: map[string]pschema.FunctionSpec{},
	}
	c := coll
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{
			tok: {C: &c}, // create-only stub, as pulschema emits
		},
	}

	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}
	if err := coalesceDiscriminatedCRUDPass(s); err != nil {
		t.Fatalf("coalesceDiscriminatedCRUDPass: %v", err)
	}

	m := meta.ResourceCRUDMap[tok]
	if m.R == nil || *m.R != item {
		t.Errorf("R = %v, want item path %q", m.R, item)
	}
	if m.D == nil || *m.D != item {
		t.Errorf("D = %v, want item path %q", m.D, item)
	}
	if m.P == nil || *m.P != item {
		t.Errorf("P = %v, want item path %q (item exposes PUT)", m.P, item)
	}
	if m.U != nil {
		t.Errorf("U = %v, want nil (item has no PATCH)", *m.U)
	}
	if m.C == nil || *m.C != coll {
		t.Errorf("C = %v, want unchanged collection path %q", m.C, coll)
	}
}

// TestCoalescePassDoesNotOverwriteBoundVerbs confirms a verb pulschema already
// bound is preserved (the 3 already-complete resources stay untouched).
func TestCoalescePassDoesNotOverwriteBoundVerbs(t *testing.T) {
	const coll = "/v1/sites/{siteId}/firewall/zones"
	const item = coll + "/{firewallZoneId}"
	const preBoundR = "/v1/some/other/read/path"

	doc := docWith(map[string]*openapi3.PathItem{
		item: newPathItem(true, true, false, true),
	})
	tok := "unifi:sites/v1:FirewallZone"
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{tok: {}},
		Functions: map[string]pschema.FunctionSpec{},
	}
	c, r := coll, preBoundR
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{
			tok: {C: &c, R: &r}, // R already bound — must not be replaced
		},
	}

	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}
	if err := coalesceDiscriminatedCRUDPass(s); err != nil {
		t.Fatalf("coalesceDiscriminatedCRUDPass: %v", err)
	}
	if got := *meta.ResourceCRUDMap[tok].R; got != preBoundR {
		t.Errorf("R = %q, want preserved pre-bound %q", got, preBoundR)
	}
}

// TestCoalescePassPrunesOrphans is the focused unit proof of Phase 2: a crudMap
// key bound to neither a live resource nor a function is dropped, while live
// resource and function keys survive.
func TestCoalescePassPrunesOrphans(t *testing.T) {
	const resTok = "unifi:sites/v1:FirewallZone"
	const fnTok = "unifi:sites/v1:getCountrie"
	const orphanTok = "unifi:sites/v1:FirewallZoneCreateUpdateDto"

	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{resTok: {}},
		Functions: map[string]pschema.FunctionSpec{fnTok: {}},
	}
	c := "/v1/sites/{siteId}/firewall/zones"
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{
			resTok:    {C: &c},
			fnTok:     {R: &c},
			orphanTok: {C: &c},
		},
	}

	s := &GenState{Pkg: pkg, Meta: meta, Doc: docWith(nil)}
	if err := coalesceDiscriminatedCRUDPass(s); err != nil {
		t.Fatalf("coalesceDiscriminatedCRUDPass: %v", err)
	}
	if _, ok := meta.ResourceCRUDMap[orphanTok]; ok {
		t.Errorf("orphan key %q survived pruning", orphanTok)
	}
	if _, ok := meta.ResourceCRUDMap[resTok]; !ok {
		t.Errorf("live resource key %q was pruned", resTok)
	}
	if _, ok := meta.ResourceCRUDMap[fnTok]; !ok {
		t.Errorf("live function key %q was pruned", fnTok)
	}
}
