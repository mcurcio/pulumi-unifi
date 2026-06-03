package gen

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// TestNormalizeFunction is the focused unit table for the function-name cleanup:
// strip Integration/Dto, fix snake casing + acronyms, repair irregular singulars,
// and settle the explicit get/list near-duplicates.
func TestNormalizeFunction(t *testing.T) {
	cases := map[string]string{
		// strip Integration prefix + Dto suffix
		"getIntegrationDnsARecordDto": "getDnsARecord",
		"getIntegrationIpAclRuleDto":  "getIpAclRule",
		// snake-in-camel + acronym normalization
		"getGateway_managed_network_details": "getGatewayManagedNetworkDetails",
		"getVPN_client_connection_details":   "getVpnClientConnectionDetails",
		"getWired_client_details":            "getWiredClientDetails",
		// irregular singular repair
		"getCountrie":                "getCountry",
		"getDpiApplicationCategorie": "getDpiApplicationCategory",
		// explicit get/list settlements
		"listTrafficMatching":  "getTrafficMatchingList",
		"listTrafficMatchings": "listTrafficMatchingLists",
		"getFirewallPolicie":   "listFirewallPolicies",
		// untouched names (no false positives)
		"getInfo":              "getInfo",
		"getFirewallPolicy":    "getFirewallPolicy",
		"getWifiBroadcastPage": "getWifiBroadcastPage", // *Page left to D-M2.5
	}
	for in, want := range cases {
		if got := normalizeFunction(in); got != want {
			t.Errorf("normalizeFunction(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRenameResourcesEntityPrefix is the focused unit proof of D-M2.2: a
// discriminated variant token is prefixed with its entity, and the crudMap /
// autoNameMap keys move with it.
func TestRenameResourcesEntityPrefix(t *testing.T) {
	const coll = "/v1/sites/{siteId}/wifi/broadcasts"
	oldTok := "unifi:sites/v1:Standard"
	newTok := "unifi:sites/v1:WifiBroadcastStandard"

	doc := discDoc(coll, "type", map[string]string{
		"STANDARD":      "#/components/schemas/IntegrationStandardWifiBroadcastCreateUpdateDto",
		"IOT_OPTIMIZED": "#/components/schemas/IntegrationIotOptimizedWifiBroadcastCreateUpdateDto",
	})
	c := coll
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{oldTok: {}},
		Functions: map[string]pschema.FunctionSpec{},
	}
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{oldTok: {C: &c}},
		AutoNameMap:     map[string]string{oldTok: "name"},
	}
	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}
	if err := renameResources(s); err != nil {
		t.Fatalf("renameResources: %v", err)
	}
	if _, ok := pkg.Resources[oldTok]; ok {
		t.Errorf("old token %q still present", oldTok)
	}
	if _, ok := pkg.Resources[newTok]; !ok {
		t.Errorf("new token %q missing", newTok)
	}
	if _, ok := meta.ResourceCRUDMap[newTok]; !ok {
		t.Errorf("crudMap key not moved to %q", newTok)
	}
	if _, ok := meta.AutoNameMap[newTok]; !ok {
		t.Errorf("autoNameMap key not moved to %q", newTok)
	}
}

// TestRenameResourcesErrorsOnStalePrefix proves the loud guard: a prefix-table
// entry whose collection POST carries no discriminator is a stale-table error.
func TestRenameResourcesErrorsOnStalePrefix(t *testing.T) {
	// /networks is in entityPrefixes, but here its POST body has no discriminator.
	const coll = "/v1/sites/{siteId}/networks"
	tok := "unifi:sites/v1:Gateway"

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
	if err := renameResources(s); err == nil {
		t.Error("expected a stale-prefix-table error when the collection has no discriminator")
	}
}

// TestTokenRenameNoCollisionsOnPipeline is the integration guard: after the full
// pipeline (which includes the rename), no two resource or function tokens share
// a short name, and the entity-prefixed targets are present.
func TestTokenRenameNoCollisionsOnPipeline(t *testing.T) {
	pkg, _ := runPipelineTyped(t)

	seen := map[string]string{}
	dup := func(tok string) {
		short := tok[strings.LastIndex(tok, ":")+1:]
		if prior, ok := seen[short]; ok {
			t.Errorf("duplicate short name %q: %q and %q", short, prior, tok)
		}
		seen[short] = tok
	}
	for tok := range pkg.Resources {
		dup(tok)
	}
	// functions share the short-name namespace check separately (SDK keeps them
	// distinct), but assert the renamed resource targets exist:
	want := []string{
		"unifi:sites/v1:DnsARecord",
		"unifi:sites/v1:WifiBroadcastStandard",
		"unifi:sites/v1:WifiBroadcastIotOptimized",
		"unifi:sites/v1:ManagedNetworkGateway",
		"unifi:sites/v1:TrafficMatchIpv4Addresses",
		"unifi:sites/v1:TrafficMatchMac",
	}
	for _, w := range want {
		if _, ok := pkg.Resources[w]; !ok {
			t.Errorf("expected renamed resource %q to exist", w)
		}
	}
	// And the bare pre-rename names must be gone.
	for _, bare := range []string{"unifi:sites/v1:Standard", "unifi:sites/v1:Mac", "unifi:sites/v1:Gateway", "unifi:sites/v1:ARecord"} {
		if _, ok := pkg.Resources[bare]; ok {
			t.Errorf("bare pre-rename token %q still present", bare)
		}
	}
}
