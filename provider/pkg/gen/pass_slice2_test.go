package gen

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// Tests for the passes slice 2 added (docs/design/private-binding.md §S2.3,
// §S2.5): variant-required, aliases, output-only and singletons, plus the
// alias-aware breaking-delta detector.

// --- variant-required ----------------------------------------------------------

// variantDoc is a collection POST whose oneOf+discriminator maps one value to an
// allOf variant: a $ref base requiring `base`, an inline member requiring
// `inline`, and the variant's own `required` list.
func variantDoc(coll string, own, inline, base []string) *openapi3.T {
	baseSchema := &openapi3.SchemaRef{Ref: "#/components/schemas/Base", Value: &openapi3.Schema{Required: base}}
	variant := &openapi3.Schema{
		AllOf:    openapi3.SchemaRefs{baseSchema, {Value: &openapi3.Schema{Required: inline}}},
		Required: own,
	}
	ref := "#/components/schemas/Variant"
	body := &openapi3.Schema{
		OneOf: openapi3.SchemaRefs{{Ref: ref, Value: variant}},
		Discriminator: &openapi3.Discriminator{PropertyName: "type",
			Mapping: openapi3.StringMap[openapi3.MappingRef]{"MAC": {Ref: ref}}},
	}
	paths := openapi3.NewPaths()
	paths.Set(coll, &openapi3.PathItem{Post: &openapi3.Operation{RequestBody: &openapi3.RequestBodyRef{
		Value: &openapi3.RequestBody{Content: openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{Value: body})}}}})
	return &openapi3.T{Paths: paths}
}

func TestVariantRequiredLiftsOwnAndInlineRequired(t *testing.T) {
	const coll = "/v1/sites/{siteId}/acl-rules"
	tok := "unifi:sites/v1:Mac"
	str := pschema.PropertySpec{TypeSpec: pschema.TypeSpec{Type: "string"}}
	pkg := &pschema.PackageSpec{Resources: map[string]pschema.ResourceSpec{tok: {
		InputProperties: map[string]pschema.PropertySpec{
			"type": str, "name": str, "networkIdFilter": str, "inlineOnly": str, "baseOnly": str,
		},
		RequiredInputs: []string{"name"},
	}}}
	c := coll
	s := &GenState{Pkg: pkg, Doc: variantDoc(coll,
		[]string{"name", "networkIdFilter", "type", "notAnInput"}, // own (type = discriminator)
		[]string{"inlineOnly"}, // inline allOf member
		[]string{"baseOnly"}),  // $ref base: pulschema's job, never lifted
		Meta: &openapigen.ProviderMetadata{ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{tok: {C: &c}}}}
	if err := variantRequiredPass(s); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(pkg.Resources[tok].RequiredInputs, ",")
	if got != "name,inlineOnly,networkIdFilter" {
		t.Errorf("requiredInputs = %s, want name,inlineOnly,networkIdFilter (own + inline; never the discriminator, the $ref base, or a non-input)", got)
	}
}

func TestVariantRequiredOnThePinnedSpec(t *testing.T) {
	pkg, _ := runPipelineTyped(t)
	mac := pkg.Resources["unifi:sites/v1:AclRuleMac"]
	if !namesOf(mac.RequiredInputs)["networkIdFilter"] {
		t.Errorf("AclRuleMac.requiredInputs = %v: the spec (and the server, probe a2) require networkIdFilter", mac.RequiredInputs)
	}
	for tok, res := range pkg.Resources {
		for _, r := range res.RequiredInputs {
			if ps, ok := res.InputProperties[r]; ok && ps.Const != nil {
				t.Errorf("%s: pinned discriminator %s lifted into requiredInputs", tok, r)
			}
		}
	}
}

// --- aliases -------------------------------------------------------------------

func aliasState() *GenState {
	return &GenState{Pkg: &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{"m:New": {}, "m:Other": {}},
		Functions: map[string]pschema.FunctionSpec{"m:getThing": {}},
	}}
}

func TestAliasesSet(t *testing.T) {
	s := aliasState()
	if err := applyAliases(s, map[string][]string{"m:New": {"m:Old2", "m:Old"}}); err != nil {
		t.Fatal(err)
	}
	a := s.Pkg.Resources["m:New"].Aliases
	if len(a) != 2 || a[0].Type != "m:Old" || a[1].Type != "m:Old2" {
		t.Errorf("aliases = %+v, want sorted [m:Old m:Old2]", a)
	}
}

func TestAliasesLoudErrors(t *testing.T) {
	for name, pins := range map[string]map[string][]string{
		"dead key":        {"m:Gone": {"m:Old"}},
		"alias is live":   {"m:New": {"m:Other"}},
		"alias is a func": {"m:New": {"m:getThing"}},
		"self alias":      {"m:New": {"m:New"}},
		"shared alias":    {"m:New": {"m:Old"}, "m:Other": {"m:Old"}},
	} {
		if err := applyAliases(aliasState(), pins); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestAliasesOnThePinnedSpec(t *testing.T) {
	pkg, _ := runPipelineTyped(t)
	for tok, old := range map[string]string{
		"unifi:sites/v1:AclRuleIpv4": "unifi:sites/v1:TrafficMatchIpv4",
		"unifi:sites/v1:AclRuleMac":  "unifi:sites/v1:TrafficMatchMac",
	} {
		a := pkg.Resources[tok].Aliases
		if len(a) != 1 || a[0].Type != old {
			t.Errorf("%s aliases = %+v, want [%s]", tok, a, old)
		}
		if _, live := pkg.Resources[old]; live {
			t.Errorf("%s is still a live token; the rename must remove it", old)
		}
	}
}

// --- output-only ---------------------------------------------------------------

func outputOnlyState() *GenState {
	p := pschema.PropertySpec{TypeSpec: pschema.TypeSpec{Type: "integer"}}
	return &GenState{Pkg: &pschema.PackageSpec{Resources: map[string]pschema.ResourceSpec{"m:Rule": {
		ObjectTypeSpec:  pschema.ObjectTypeSpec{Properties: map[string]pschema.PropertySpec{"index": p, "name": p}},
		InputProperties: map[string]pschema.PropertySpec{"index": p, "name": p, "inOnly": p},
		RequiredInputs:  []string{"index", "name"},
	}}}}
}

func TestOutputOnly(t *testing.T) {
	s := outputOnlyState()
	if err := applyOutputOnly(s, []string{"m:Rule.index"}); err != nil {
		t.Fatal(err)
	}
	r := s.Pkg.Resources["m:Rule"]
	if _, ok := r.InputProperties["index"]; ok {
		t.Error("index is still an input")
	}
	if _, ok := r.Properties["index"]; !ok {
		t.Error("index output was dropped")
	}
	if strings.Join(r.RequiredInputs, ",") != "name" {
		t.Errorf("requiredInputs = %v", r.RequiredInputs)
	}
}

func TestOutputOnlyDeadEntriesFail(t *testing.T) {
	for _, pin := range []string{"m:Gone.index", "m:Rule.nope", "m:Rule.inOnly", "noDot", "m:Rule."} {
		if err := applyOutputOnly(outputOnlyState(), []string{pin}); err == nil {
			t.Errorf("%q: want an error", pin)
		}
	}
}

func TestIndexIsOutputOnlyOnThePinnedSpec(t *testing.T) {
	pkg, _ := runPipelineTyped(t)
	for _, tok := range []string{"unifi:sites/v1:AclRuleIpv4", "unifi:sites/v1:AclRuleMac"} {
		r := pkg.Resources[tok]
		if _, ok := r.InputProperties["index"]; ok {
			t.Errorf("%s.index is an input; AclRuleOrdering owns the order", tok)
		}
		if _, ok := r.Properties["index"]; !ok {
			t.Errorf("%s.index output missing", tok)
		}
	}
}

// --- singletons ----------------------------------------------------------------

func TestSingletonOnThePinnedSpec(t *testing.T) {
	pkg, meta := runPipelineTyped(t)
	tok := "unifi:sites/v1:AclRuleOrdering"
	r, ok := pkg.Resources[tok]
	if !ok {
		t.Fatalf("%s missing", tok)
	}
	if _, ok := meta.ResourceCRUDMap[tok]; ok {
		t.Errorf("%s has a framework crudMap entry", tok)
	}
	if strings.Join(r.RequiredInputs, ",") != "orderedAclRuleIds" {
		t.Errorf("requiredInputs = %v", r.RequiredInputs)
	}
	for _, in := range []string{"orderedAclRuleIds", "unlisted", "siteId"} {
		if _, ok := r.InputProperties[in]; !ok {
			t.Errorf("input %s missing", in)
		}
	}
	if !r.InputProperties["siteId"].ReplaceOnChanges {
		t.Error("siteId must be replaceOnChanges, like every resource's")
	}
	if _, ok := r.Properties["effectiveOrder"]; !ok {
		t.Error("output effectiveOrder missing")
	}
	if _, ok := pkg.Types[tok+"Unlisted"]; !ok {
		t.Errorf("enum %sUnlisted missing", tok)
	}
	found := false
	for _, p := range mappingExcludedPaths() {
		found = found || p == "/v1/sites/{siteId}/acl-rules/ordering"
	}
	if !found {
		t.Error("the ordering path must stay excluded from pulschema")
	}
}

func singletonDoc(withPut bool, body *openapi3.Schema) *openapi3.T {
	paths := openapi3.NewPaths()
	item := &openapi3.PathItem{Get: &openapi3.Operation{}}
	if withPut {
		item.Put = &openapi3.Operation{RequestBody: &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Content: openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{Value: body})}}}
	}
	paths.Set("/v1/sites/{siteId}/x/ordering", item)
	return &openapi3.T{Paths: paths}
}

func TestSingletonLoudErrors(t *testing.T) {
	sg := Singleton{Path: "/v1/sites/{siteId}/x/ordering", Token: "unifi:sites/v1:XOrdering", Property: "orderedIds", Merge: MergeLeadingBlock}
	ids := &openapi3.Schema{Type: &openapi3.Types{"array"}, Items: &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}}
	good := &openapi3.Schema{Properties: openapi3.Schemas{"orderedIds": {Value: ids}}, Required: []string{"orderedIds"}}
	state := func(doc *openapi3.T) *GenState {
		return &GenState{Doc: doc,
			Pkg:  &pschema.PackageSpec{Resources: map[string]pschema.ResourceSpec{}, Functions: map[string]pschema.FunctionSpec{}, Types: map[string]pschema.ComplexTypeSpec{}},
			Meta: &openapigen.ProviderMetadata{ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{}}}
	}
	if err := addSingleton(state(singletonDoc(true, good)), sg); err != nil {
		t.Fatalf("good entry: %v", err)
	}
	notRequired := &openapi3.Schema{Properties: good.Properties}
	scalar := &openapi3.Schema{Properties: openapi3.Schemas{"orderedIds": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}}, Required: []string{"orderedIds"}}
	for name, s := range map[string]*GenState{
		"no PUT":       state(singletonDoc(false, nil)),
		"not required": state(singletonDoc(true, notRequired)),
		"not a list":   state(singletonDoc(true, scalar)),
	} {
		if err := addSingleton(s, sg); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	s := state(singletonDoc(true, good))
	s.Meta.ResourceCRUDMap[sg.Token] = &openapigen.CRUDOperationsMap{}
	if err := addSingleton(s, sg); err == nil {
		t.Error("a token with a framework crudMap entry: want an error")
	}
}

func TestSingletonsValidate(t *testing.T) {
	base := mappingsFile{ExcludedPaths: []string{"/p"}}
	for name, sgs := range map[string][]Singleton{
		"not excluded": {{Path: "/q", Token: "t", Property: "p", Merge: MergeLeadingBlock}},
		"bad merge":    {{Path: "/p", Token: "t", Property: "p", Merge: "replace"}},
		"incomplete":   {{Path: "/p", Token: "t", Merge: MergeLeadingBlock}},
		"twice": {{Path: "/p", Token: "t", Property: "p", Merge: MergeLeadingBlock},
			{Path: "/p", Token: "t", Property: "p", Merge: MergeLeadingBlock}},
	} {
		m := base
		m.Singletons = sgs
		if err := m.validateSingletons(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// --- alias-aware breaking delta ------------------------------------------------

func TestBreakingDeltaFollowsAliases(t *testing.T) {
	str := pschema.PropertySpec{TypeSpec: pschema.TypeSpec{Type: "string"}}
	base := pschema.PackageSpec{Resources: map[string]pschema.ResourceSpec{
		"m:Old":  {InputProperties: map[string]pschema.PropertySpec{"a": str, "b": str}, RequiredInputs: []string{"a"}},
		"m:Gone": {InputProperties: map[string]pschema.PropertySpec{"a": str}},
	}}
	cur := pschema.PackageSpec{Resources: map[string]pschema.ResourceSpec{
		"m:New": {InputProperties: map[string]pschema.PropertySpec{"a": str, "b": str}, RequiredInputs: []string{"a", "b"},
			Aliases: []pschema.AliasSpec{{Type: "m:Old"}}},
	}}
	items := detectBreakingDelta(base, cur)
	if len(items) != 1 || items[0].String() != "added-required-input: m:New.b" {
		t.Errorf("items = %v, want exactly [added-required-input: m:New.b] (the rename is followed; an unaliased removal is left to the byte golden)", items)
	}
}
