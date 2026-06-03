package gen

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// TestMappingsLoad proves the embedded mappings.yaml parses and carries the
// editorial content the engine relies on. A garbled/empty embed (or a YAML key
// rename) fails here instead of silently emitting an un-pinned surface.
func TestMappingsLoad(t *testing.T) {
	if len(mappingExcludedPaths()) == 0 {
		t.Error("excludedPaths is empty after load")
	}
	// A representative pin from each table must resolve.
	if p, ok := entityPrefix("/v1/sites/{siteId}/dns/policies"); !ok || p != "Dns" {
		t.Errorf("entityPrefix(dns/policies) = %q,%v; want Dns,true", p, ok)
	}
	if v, ok := acronymFixup("VPN"); !ok || v != "Vpn" {
		t.Errorf("acronymFixup(VPN) = %q,%v; want Vpn,true", v, ok)
	}
	if got := irregularSingularMap()["Countrie"]; got != "Country" {
		t.Errorf("irregularSingulars[Countrie] = %q; want Country", got)
	}
	if v, ok := explicitFunctionRename("getFirewallPolicie"); !ok || v != "listFirewallPolicies" {
		t.Errorf("explicitFunctionRename(getFirewallPolicie) = %q,%v; want listFirewallPolicies,true", v, ok)
	}
}

// TestMappingsExcludedPathsMatchSpec asserts every excludedPaths entry loaded
// from mappings.yaml resolves against the real spec — the same dead-entry guard
// as TestExcludedPathsResolve, here pinned to the data-layer accessor so a bad
// migration of the list into YAML is caught.
func TestMappingsExcludedPathsMatchSpec(t *testing.T) {
	doc := fixedDoc(t)
	for _, p := range mappingExcludedPaths() {
		if doc.Paths.Find(p) == nil {
			t.Errorf("excludedPaths entry %q (from mappings.yaml) no longer matches any spec path", p)
		}
	}
}

// TestRenameResourcesUnmappedEntityFailsLoud is the MAPPING-LAYER.md acceptance
// criterion: a discriminated entity (its create body carries a discriminator,
// so pulschema split it into bare per-variant tokens) whose collection path is
// NOT pinned in mappings.yaml's entityPrefixes is an "unmapped entity" — the
// engine must fail loud rather than ship an un-pinned, context-free public token.
// Uses a synthetic collection absent from the data file.
func TestRenameResourcesUnmappedEntityFailsLoud(t *testing.T) {
	const coll = "/v1/sites/{siteId}/not-in-mappings"
	tok := "unifi:sites/v1:SomeVariant"

	// A discriminated POST body whose mapping value pascal-cases to the token.
	doc := discDoc(coll, "type", map[string]string{
		"SOME_VARIANT":  "#/components/schemas/SomeVariantDto",
		"OTHER_VARIANT": "#/components/schemas/OtherVariantDto",
	})
	c := coll
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{tok: {}},
		Functions: map[string]pschema.FunctionSpec{},
	}
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{tok: {C: &c}},
	}
	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}
	if err := renameResources(s); err == nil {
		t.Error("expected an 'unmapped entity' error for a discriminated collection absent from mappings.yaml entityPrefixes")
	}
}

// TestRenameResourcesFlatUnmappedIsFine confirms the loud guard fires ONLY for
// discriminated entities: a flat (non-discriminated) resource whose collection is
// not in the prefix table is left untouched, not failed.
func TestRenameResourcesFlatUnmappedIsFine(t *testing.T) {
	const coll = "/v1/sites/{siteId}/flat-thing"
	tok := "unifi:sites/v1:FlatThing"

	paths := openapi3.NewPaths()
	paths.Set(coll, &openapi3.PathItem{Post: &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Content: openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{Value: &openapi3.Schema{}}),
		}},
	}})
	doc := &openapi3.T{Paths: paths}
	c := coll
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{tok: {}},
		Functions: map[string]pschema.FunctionSpec{},
	}
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{tok: {C: &c}},
	}
	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}
	if err := renameResources(s); err != nil {
		t.Errorf("flat unmapped resource should be left untouched, got error: %v", err)
	}
	if _, ok := pkg.Resources[tok]; !ok {
		t.Errorf("flat resource token %q should be unchanged", tok)
	}
}
