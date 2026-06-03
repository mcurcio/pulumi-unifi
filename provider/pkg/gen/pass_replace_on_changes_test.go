package gen

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// replaceOnChanges reports whether an input property of a resource is marked
// replaceOnChanges in the (post-pipeline) schema.
func replaceOnChanges(pkg pschema.PackageSpec, tok, prop string) bool {
	res, ok := pkg.Resources[tok]
	if !ok {
		return false
	}
	ps, ok := res.InputProperties[prop]
	return ok && ps.ReplaceOnChanges
}

// TestReplaceOnChangesPinsVlanId is a RED-when-broken guard: every managed-network
// variant (the only resources carrying vlanId, an identity field) must have vlanId
// marked replaceOnChanges — pinned via mappings.yaml immutableFields.
func TestReplaceOnChangesPinsVlanId(t *testing.T) {
	pkg, _ := runPipelineTyped(t)

	for _, tok := range []string{
		"unifi:sites/v1:ManagedNetworkGateway",
		"unifi:sites/v1:ManagedNetworkSwitch",
		"unifi:sites/v1:ManagedNetworkUnmanaged",
	} {
		if !replaceOnChanges(pkg, tok, "vlanId") {
			t.Errorf("%s: vlanId must be replaceOnChanges (immutableFields pin)", tok)
		}
	}
}

// TestReplaceOnChangesMarksDiscriminator is a RED-when-broken guard: every
// discriminated-variant resource has its discriminator (type / management) — now a
// Const from D-M2.1 — marked replaceOnChanges.
func TestReplaceOnChangesMarksDiscriminator(t *testing.T) {
	pkg, _ := runPipelineTyped(t)

	discProp := map[string]string{
		"unifi:sites/v1:DnsARecord":                "type",
		"unifi:sites/v1:DnsCnameRecord":            "type",
		"unifi:sites/v1:WifiBroadcastStandard":     "type",
		"unifi:sites/v1:WifiBroadcastIotOptimized": "type",
		"unifi:sites/v1:ManagedNetworkGateway":     "management",
		"unifi:sites/v1:ManagedNetworkSwitch":      "management",
		"unifi:sites/v1:ManagedNetworkUnmanaged":   "management",
		"unifi:sites/v1:TrafficMatchMac":           "type",
		"unifi:sites/v1:TrafficMatchPorts":         "type",
	}
	for tok, prop := range discProp {
		if !replaceOnChanges(pkg, tok, prop) {
			t.Errorf("%s: discriminator %q must be replaceOnChanges", tok, prop)
		}
	}
}

// TestReplaceOnChangesMarksSiteId is the D-M3.2 guard: siteId is the per-resource
// site-scope override (the framework honors a resource-level siteId over the
// provider-global one), and the UniFi API has no move-to-another-site edit, so
// changing it must recreate the resource. Pinned via mappings.yaml immutableFields,
// so EVERY resource carrying a siteId input must have it replaceOnChanges.
func TestReplaceOnChangesMarksSiteId(t *testing.T) {
	pkg, _ := runPipelineTyped(t)

	marked := 0
	for tok, res := range pkg.Resources {
		if _, ok := res.InputProperties["siteId"]; !ok {
			continue
		}
		marked++
		if !replaceOnChanges(pkg, tok, "siteId") {
			t.Errorf("%s: siteId must be replaceOnChanges (immutableFields pin, D-M3.2)", tok)
		}
	}
	if marked == 0 {
		t.Error("no resource carried a siteId input — the pin would be dead")
	}
}

// TestReplaceOnChangesLeavesMutableInputsAlone proves the pass is targeted: an
// ordinary mutable input (a network's name) is left updatable, not forced to
// replace.
func TestReplaceOnChangesLeavesMutableInputsAlone(t *testing.T) {
	pkg, _ := runPipelineTyped(t)

	if replaceOnChanges(pkg, "unifi:sites/v1:ManagedNetworkGateway", "name") {
		t.Error("ManagedNetworkGateway.name must stay mutable (not replaceOnChanges)")
	}
}

// TestReplaceOnChangesUnitMarksAllThreeSources is the focused unit proof, isolated
// from the spec: a synthetic resource gets its readOnly input, its discriminator,
// and its immutableFields pins (vlanId + siteId, D-M3.2) marked, while an ordinary
// mutable input is left untouched. The discriminator is identified by its Const (as
// D-M2.1 leaves it) — this pass runs after token-rename, so it does not re-derive
// from the token.
func TestReplaceOnChangesUnitMarksAllThreeSources(t *testing.T) {
	const coll = "/v1/sites/{siteId}/networks"
	tok := "unifi:sites/v1:ManagedNetworkGateway" // post-rename name

	// Create body marks `serverAssignedId` readOnly; the rest are mutable.
	variant := &openapi3.Schema{
		Properties: openapi3.Schemas{
			"serverAssignedId": {Value: &openapi3.Schema{ReadOnly: true}},
			"name":             {Value: &openapi3.Schema{}},
			"vlanId":           {Value: &openapi3.Schema{}},
			"siteId":           {Value: &openapi3.Schema{}},
		},
	}
	media := openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{Value: variant})
	paths := openapi3.NewPaths()
	paths.Set(coll, &openapi3.PathItem{Post: &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Content: media}},
	}})
	doc := &openapi3.T{Paths: paths}

	c := coll
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{
			tok: {InputProperties: map[string]pschema.PropertySpec{
				// management is Const-pinned, as D-M2.1 leaves the discriminator.
				"management":       {TypeSpec: pschema.TypeSpec{Type: "string"}, Const: "GATEWAY"},
				"serverAssignedId": {TypeSpec: pschema.TypeSpec{Type: "string"}},
				"name":             {TypeSpec: pschema.TypeSpec{Type: "string"}},
				"vlanId":           {TypeSpec: pschema.TypeSpec{Type: "integer"}},
				"siteId":           {TypeSpec: pschema.TypeSpec{Type: "string"}},
			}},
		},
		Functions: map[string]pschema.FunctionSpec{},
	}
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{tok: {C: &c}},
	}
	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}

	if err := replaceOnChangesPass(s); err != nil {
		t.Fatalf("replaceOnChangesPass: %v", err)
	}

	ip := pkg.Resources[tok].InputProperties
	for _, want := range []string{"serverAssignedId", "management", "vlanId", "siteId"} {
		if !ip[want].ReplaceOnChanges {
			t.Errorf("%s must be replaceOnChanges (readOnly/discriminator/pin)", want)
		}
	}
	if ip["name"].ReplaceOnChanges {
		t.Error("name must stay mutable")
	}
}

// TestReplaceOnChangesDeadPinErrors proves the dead-pin guard: an immutableFields
// pin that no resource carries is a loud error, so the data layer cannot silently
// rot across a spec bump. (Asserted via the standalone engine on a resource that
// has none of the pinned names.)
func TestReplaceOnChangesDeadPinErrors(t *testing.T) {
	// The pinned name list comes from mappings.yaml; build a resource set that has
	// NONE of those names so every pin is dead.
	pins := immutableFieldPins()
	if len(pins) == 0 {
		t.Skip("no immutableFields pins to exercise the dead-pin guard")
	}

	tok := "unifi:sites/v1:Bare"
	c := "/v1/sites/{siteId}/bare"
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{
			tok: {InputProperties: map[string]pschema.PropertySpec{
				"name": {TypeSpec: pschema.TypeSpec{Type: "string"}},
			}},
		},
		Functions: map[string]pschema.FunctionSpec{},
	}
	doc := &openapi3.T{Paths: openapi3.NewPaths()}
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{tok: {C: &c}},
	}
	s := &GenState{Pkg: pkg, Meta: meta, Doc: doc}

	if err := replaceOnChangesPass(s); err == nil {
		t.Error("expected a loud error when an immutableFields pin matches no resource input")
	}
}
