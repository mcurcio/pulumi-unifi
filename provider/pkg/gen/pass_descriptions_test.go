package gen

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// TestDescriptionsBackfilledOnPipeline is the D-M2.4 regression guard: after the
// full pipeline, EVERY resource and EVERY function carries a non-empty top-level
// Description (the headline best-practices miss this pass fixes). A new token
// that slips through with no summary and no override regresses this.
func TestDescriptionsBackfilledOnPipeline(t *testing.T) {
	pkg, _ := runPipelineTyped(t)

	for tok := range pkg.Resources {
		if strings.TrimSpace(pkg.Resources[tok].Description) == "" {
			t.Errorf("resource %q has an empty top-level description", tok)
		}
	}
	for tok := range pkg.Functions {
		if strings.TrimSpace(pkg.Functions[tok].Description) == "" {
			t.Errorf("function %q has an empty top-level description", tok)
		}
	}

	// Spot-check derive-by-default (flat resource uses the spec summary) and
	// pin-by-exception (a discriminated variant uses its precise override, not the
	// shared collection summary).
	if got := pkg.Resources["unifi:sites/v1:FirewallZone"].Description; got != "Create Custom Firewall Zone" {
		t.Errorf("FirewallZone description = %q; want the spec summary", got)
	}
	if got := pkg.Resources["unifi:sites/v1:DnsARecord"].Description; !strings.Contains(got, "IPv4") {
		t.Errorf("DnsARecord description = %q; want the precise IPv4 override, not the shared 'Create DNS Policy' summary", got)
	}
	// The Voucher/AdoptDevice action/batch RPCs are excluded from the resource set
	// entirely (D-M3.3, excludeResources), so they must NOT appear here at all.
	for _, tok := range []string{"unifi:sites/v1:Voucher", "unifi:sites/v1:AdoptDevice"} {
		if _, ok := pkg.Resources[tok]; ok {
			t.Errorf("excluded action/batch RPC %q is still a resource", tok)
		}
	}
}

// TestDescriptionsPassErrorsOnMissingSummary proves the loud-failure path: a
// resource with neither a create-operation summary nor a mappings.yaml override
// is a coverage gap, not a silently-empty description.
func TestDescriptionsPassErrorsOnMissingSummary(t *testing.T) {
	const coll = "/v1/sites/{siteId}/no-summary-thing"
	tok := "unifi:sites/v1:NoSummaryThing" // not in descriptions overrides

	paths := openapi3.NewPaths()
	// POST with no summary.
	paths.Set(coll, &openapi3.PathItem{Post: &openapi3.Operation{}})
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
	if err := descriptionsPass(s); err == nil {
		t.Error("expected a loud error for a resource with no create summary and no override")
	}
}

// TestDescriptionsPassUsesSummaryDefault proves the derive-by-default path on a
// synthetic token: a resource whose create op has a summary and no override gets
// exactly that summary.
func TestDescriptionsPassUsesSummaryDefault(t *testing.T) {
	const coll = "/v1/sites/{siteId}/widgets"
	tok := "unifi:sites/v1:Widget"

	paths := openapi3.NewPaths()
	paths.Set(coll, &openapi3.PathItem{Post: &openapi3.Operation{Summary: "Create Widget"}})
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
	if err := descriptionsPass(s); err != nil {
		t.Fatalf("descriptionsPass: %v", err)
	}
	if got := pkg.Resources[tok].Description; got != "Create Widget" {
		t.Errorf("Widget description = %q; want the spec summary 'Create Widget'", got)
	}
}
