package gen

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// discDoc builds a doc whose collection POST body is a oneOf+discriminator with
// the given propertyName and value→ref mapping, so deriveDiscriminator has a real
// spec shape to invert.
func discDoc(collPath, propName string, mapping map[string]string) *openapi3.T {
	mref := openapi3.StringMap[openapi3.MappingRef]{}
	for v, ref := range mapping {
		mref[v] = openapi3.MappingRef{Ref: ref}
	}
	bodySchema := &openapi3.Schema{
		Discriminator: &openapi3.Discriminator{PropertyName: propName, Mapping: mref},
	}
	media := openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{Value: bodySchema})
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Content: media}},
	}
	paths := openapi3.NewPaths()
	paths.Set(collPath, &openapi3.PathItem{Post: op})
	return &openapi3.T{Paths: paths}
}

// TestDiscriminatorInjectPinsConst is the focused unit proof: a split variant
// whose token short name pascal-cases from a discriminator value gets that value
// pinned as Const+Default and dropped from requiredInputs.
func TestDiscriminatorInjectPinsConst(t *testing.T) {
	const coll = "/v1/sites/{siteId}/dns/policies"
	tok := "unifi:sites/v1:ARecord"

	doc := discDoc(coll, "type", map[string]string{
		"A_RECORD":    "#/components/schemas/IntegrationDnsARecordCreateUpdateDto",
		"AAAA_RECORD": "#/components/schemas/IntegrationDnsAaaaRecordCreateUpdateDto",
	})
	c := coll
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{
			tok: {
				ObjectTypeSpec: pschema.ObjectTypeSpec{},
				InputProperties: map[string]pschema.PropertySpec{
					"type":    {TypeSpec: pschema.TypeSpec{Type: "string"}},
					"enabled": {TypeSpec: pschema.TypeSpec{Type: "boolean"}},
				},
				RequiredInputs: []string{"enabled", "type"},
			},
		},
		Functions: map[string]pschema.FunctionSpec{},
	}
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{tok: {C: &c}},
	}

	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}
	if err := discriminatorInjectPass(s); err != nil {
		t.Fatalf("discriminatorInjectPass: %v", err)
	}

	res := pkg.Resources[tok]
	tp := res.InputProperties["type"]
	if tp.Const != "A_RECORD" {
		t.Errorf("type.Const = %v, want %q", tp.Const, "A_RECORD")
	}
	if tp.Default != "A_RECORD" {
		t.Errorf("type.Default = %v, want %q", tp.Default, "A_RECORD")
	}
	for _, r := range res.RequiredInputs {
		if r == "type" {
			t.Errorf("requiredInputs still contains %q after injection: %v", "type", res.RequiredInputs)
		}
	}
	// enabled must remain required (only the discriminator is dropped).
	var hasEnabled bool
	for _, r := range res.RequiredInputs {
		if r == "enabled" {
			hasEnabled = true
		}
	}
	if !hasEnabled {
		t.Errorf("requiredInputs lost a non-discriminator field: %v", res.RequiredInputs)
	}
}

// TestDiscriminatorInjectManagementProperty proves the pass works for the
// management discriminator (networks), not just type, and uses ToSdkName for the
// property key.
func TestDiscriminatorInjectManagementProperty(t *testing.T) {
	const coll = "/v1/sites/{siteId}/networks"
	tok := "unifi:sites/v1:Gateway"

	doc := discDoc(coll, "management", map[string]string{
		"GATEWAY":   "#/components/schemas/IntegrationGatewayManagedNetworkCreateUpdateDto",
		"SWITCH":    "#/components/schemas/IntegrationSwitchManagedNetworkCreateUpdateDto",
		"UNMANAGED": "#/components/schemas/IntegrationUnmanagedNetworkCreateUpdateDto",
	})
	c := coll
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{
			tok: {InputProperties: map[string]pschema.PropertySpec{
				"management": {TypeSpec: pschema.TypeSpec{Type: "string"}},
			}, RequiredInputs: []string{"management", "name"}},
		},
		Functions: map[string]pschema.FunctionSpec{},
	}
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{tok: {C: &c}},
	}
	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}
	if err := discriminatorInjectPass(s); err != nil {
		t.Fatalf("discriminatorInjectPass: %v", err)
	}
	if got := pkg.Resources[tok].InputProperties["management"].Const; got != "GATEWAY" {
		t.Errorf("management.Const = %v, want GATEWAY", got)
	}
}

// TestDiscriminatorInjectSkipsFlatResource proves a resource whose POST body has
// no discriminator is left completely untouched (no spurious const/required edit).
func TestDiscriminatorInjectSkipsFlatResource(t *testing.T) {
	const coll = "/v1/sites/{siteId}/firewall/zones"
	tok := "unifi:sites/v1:FirewallZone"

	// POST body without a discriminator.
	paths := openapi3.NewPaths()
	paths.Set(coll, &openapi3.PathItem{Post: &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Content: openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{Value: &openapi3.Schema{}}),
		}},
	}})
	doc := &openapi3.T{Paths: paths}
	c := coll
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{
			tok: {InputProperties: map[string]pschema.PropertySpec{
				"networkIds": {TypeSpec: pschema.TypeSpec{Type: "array"}},
			}, RequiredInputs: []string{"networkIds"}},
		},
		Functions: map[string]pschema.FunctionSpec{},
	}
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{tok: {C: &c}},
	}
	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}
	if err := discriminatorInjectPass(s); err != nil {
		t.Fatalf("discriminatorInjectPass: %v", err)
	}
	res := pkg.Resources[tok]
	if len(res.RequiredInputs) != 1 || res.RequiredInputs[0] != "networkIds" {
		t.Errorf("flat resource requiredInputs changed: %v", res.RequiredInputs)
	}
	if res.InputProperties["networkIds"].Const != nil {
		t.Errorf("flat resource property gained a spurious Const")
	}
}

// TestDiscriminatorInjectErrorsOnUnmatchedToken proves the loud-failure path:
// a discriminated collection whose mapping has no value pascal-casing to the
// token short name is a derivation drift, not a silent required magic string.
func TestDiscriminatorInjectErrorsOnUnmatchedToken(t *testing.T) {
	const coll = "/v1/sites/{siteId}/dns/policies"
	tok := "unifi:sites/v1:NotAVariant"
	doc := discDoc(coll, "type", map[string]string{
		"A_RECORD": "#/components/schemas/IntegrationDnsARecordCreateUpdateDto",
	})
	c := coll
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{
			tok: {InputProperties: map[string]pschema.PropertySpec{"type": {}}, RequiredInputs: []string{"type"}},
		},
		Functions: map[string]pschema.FunctionSpec{},
	}
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{tok: {C: &c}},
	}
	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}
	if err := discriminatorInjectPass(s); err == nil {
		t.Error("expected a loud error when no mapping value pascal-cases to the token name")
	}
}

// The pipeline-level assertion that every (post-rename) discriminated resource
// has its discriminator pinned to a Const lives in pass_pipeline_shape_test.go,
// since it depends on the token-rename pass having run.
