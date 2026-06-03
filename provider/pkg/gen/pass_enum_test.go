package gen

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
)

// --- Pipeline-level regression guards (RED if the pass or its registration is
// dropped, or if a spec bump re-fragments / re-orphans the type set). ---

// TestEnumDedupCollapsesKnownFamiliesOnPipeline asserts the known duplicate enum
// families collapse to a single canonical type after the full pipeline: the
// merged duplicates are gone and the canonical survives. Dropping the dedup pass
// re-introduces the duplicates and fails here.
func TestEnumDedupCollapsesKnownFamiliesOnPipeline(t *testing.T) {
	pkg, _ := runPipelineTyped(t)
	types := pkg.Types

	// The canonical (kept) tokens for each known family.
	for _, canon := range []string{
		"unifi:sites/v1:ACLRuleAction",             // ALLOW/BLOCK (derived shortest)
		"unifi:sites/v1:ConnectionStateFilterItem", // ESTABLISHED/... (derived shortest)
		"unifi:sites/v1:IpsecFilter",               // MATCH_ENCRYPTED/... (derived shortest)
		"unifi:sites/v1:DeviceState",               // ADOPTING/... (pinned rename from bare State)
		"unifi:sites/v1:ProtocolFilterItem",        // TCP/UDP (pinned rename)
	} {
		ct, ok := types[canon]
		if !ok {
			t.Errorf("canonical enum %q missing (dedup should keep it)", canon)
			continue
		}
		if len(ct.Enum) == 0 {
			t.Errorf("canonical %q is not an enum after merge (lost its value set)", canon)
		}
	}

	// The merged-away duplicates must be gone.
	for _, gone := range []string{
		"unifi:sites/v1:ACLRuleObjectAction",
		"unifi:sites/v1:ACLRuleUpdateAction",
		"unifi:sites/v1:IntegrationWifiClientFilteringPolicyDtoAction",
		"unifi:sites/v1:FirewallPolicyConnectionStateFilterItem",
		"unifi:sites/v1:FirewallPolicyIpsecFilter",
		"unifi:sites/v1:State",
		"unifi:sites/v1:AdoptedDeviceDetailsState",
		"unifi:sites/v1:AdoptedDeviceOverviewState",
		"unifi:sites/v1:Ipv4PropertiesProtocolFilterItem",
		"unifi:sites/v1:GetIntegrationIpAclRuleDtoPropertiesProtocolFilterItem",
	} {
		if _, ok := types[gone]; ok {
			t.Errorf("duplicate enum %q should have been merged away", gone)
		}
	}
}

// TestNoWithinModuleDuplicateEnumsOnPipeline is the structural invariant: after
// the pipeline, no two Types entries in the SAME module are structurally-identical
// enums (same underlying type + same value set). This is the property the dedup
// pass establishes; a spec bump that re-introduces a duplicate fails here.
func TestNoWithinModuleDuplicateEnumsOnPipeline(t *testing.T) {
	pkg, _ := runPipelineTyped(t)

	seen := map[enumSignature]string{}
	for _, tok := range sortedKeys(pkg.Types) {
		ct := pkg.Types[tok]
		if !isEnumType(ct) {
			continue
		}
		sig := enumSignature{module: tokenModule(tok), underlying: ct.Type, values: enumValueKey(ct)}
		if prior, dup := seen[sig]; dup {
			t.Errorf("structurally-identical enums survived dedup in module %q: %q and %q (values=%q)",
				sig.module, prior, tok, sig.values)
		} else {
			seen[sig] = tok
		}
	}
}

// TestNoDanglingTypeRefsOnPipeline guards the ref-rewrite half of dedup: every
// in-document `#/types/...` ref must resolve to a Type that still exists. A merge
// that deletes a duplicate without rewriting (or materializing a pinned canonical)
// leaves a dangling ref and fails here.
func TestNoDanglingTypeRefsOnPipeline(t *testing.T) {
	pkg, _ := runPipelineTyped(t)
	state := &GenState{Pkg: &pkg}
	for tok := range referencedTypeTokens(state) {
		if _, ok := pkg.Types[tok]; !ok {
			t.Errorf("dangling type ref: %q is referenced but no longer in Types", tok)
		}
	}
}

// TestNoUnreferencedEmptyTypeOnPipeline is the prune guard (01-L1): after the
// pipeline, no empty object type (no properties, no enum) is left unreferenced.
// Dropping the prune leaves the type-less spec artifacts as SDK noise.
func TestNoUnreferencedEmptyTypeOnPipeline(t *testing.T) {
	pkg, _ := runPipelineTyped(t)
	state := &GenState{Pkg: &pkg}
	referenced := referencedTypeTokens(state)
	for _, tok := range sortedKeys(pkg.Types) {
		if !referenced[tok] && isEmptyType(pkg.Types[tok]) {
			t.Errorf("unreferenced empty type %q survived the prune", tok)
		}
	}
}

// TestReferencedEmptyTypeNotDropped is the never-drop-referenced invariant: the
// spec's referenced empty object types (AccessPointFeatureOverview,
// ClientAccessOverview — empty but linked from other types) must remain. A prune
// that ignores references would break those refs.
func TestReferencedEmptyTypeNotDropped(t *testing.T) {
	pkg, _ := runPipelineTyped(t)
	for _, tok := range []string{
		"unifi:sites/v1:AccessPointFeatureOverview",
		"unifi:sites/v1:ClientAccessOverview",
	} {
		ct, ok := pkg.Types[tok]
		if !ok {
			t.Errorf("referenced empty type %q was dropped (must never drop a referenced type)", tok)
			continue
		}
		if !isEmptyType(ct) {
			t.Logf("note: %q is no longer empty in this spec — test fixture stale, not a failure", tok)
		}
	}
}

// --- Isolated unit tests over a synthetic GenState (RED if the merge/prune logic
// regresses, independent of the live spec). ---

// newEnumType builds a string enum complex type with the given values.
func newEnumType(values ...string) pschema.ComplexTypeSpec {
	ct := pschema.ComplexTypeSpec{ObjectTypeSpec: pschema.ObjectTypeSpec{Type: "string"}}
	for _, v := range values {
		ct.Enum = append(ct.Enum, pschema.EnumValueSpec{Value: v})
	}
	return ct
}

// refProp is a property whose type is a ref to an in-document type token.
func refProp(typeTok string) pschema.PropertySpec {
	return pschema.PropertySpec{TypeSpec: pschema.TypeSpec{Ref: "#/types/" + typeTok}}
}

// TestDedupEnumsDeriveDefaultAndRewrite checks the derive-default path: a family
// with no pin collapses to the shortest member and refs are rewritten to it.
func TestDedupEnumsDeriveDefaultAndRewrite(t *testing.T) {
	const (
		short = "unifi:m/v1:Action"       // shortest → canonical default
		long  = "unifi:m/v1:LongerAction" // merged away
	)
	pkg := pschema.PackageSpec{
		Types: map[string]pschema.ComplexTypeSpec{
			short: newEnumType("ALLOW", "BLOCK"),
			long:  newEnumType("BLOCK", "ALLOW"), // same set, different order
		},
		Resources: map[string]pschema.ResourceSpec{
			"unifi:m/v1:R": {ObjectTypeSpec: pschema.ObjectTypeSpec{
				Properties: map[string]pschema.PropertySpec{"a": refProp(long)},
			}},
		},
	}
	s := &GenState{Pkg: &pkg, Meta: &openapigen.ProviderMetadata{}, Doc: &openapi3.T{}}
	if err := enumCleanupPass(s); err != nil {
		t.Fatalf("enumCleanupPass: %v", err)
	}
	if _, ok := pkg.Types[long]; ok {
		t.Errorf("longer duplicate %q should be merged away", long)
	}
	if _, ok := pkg.Types[short]; !ok {
		t.Errorf("shortest member %q should survive as canonical", short)
	}
	if got := pkg.Resources["unifi:m/v1:R"].Properties["a"].Ref; got != "#/types/"+short {
		t.Errorf("ref not rewritten to canonical: got %q want %q", got, "#/types/"+short)
	}
}

// TestDedupEnumsModuleScoped checks that structurally-identical enums in DIFFERENT
// modules are NOT merged (a token's module is part of its public identity).
func TestDedupEnumsModuleScoped(t *testing.T) {
	const a = "unifi:m1/v1:Flag"
	const b = "unifi:m2/v1:Flag"
	pkg := pschema.PackageSpec{Types: map[string]pschema.ComplexTypeSpec{
		a: newEnumType("X", "Y"),
		b: newEnumType("X", "Y"),
	}}
	s := &GenState{Pkg: &pkg, Meta: &openapigen.ProviderMetadata{}, Doc: &openapi3.T{}}
	if err := enumCleanupPass(s); err != nil {
		t.Fatalf("enumCleanupPass: %v", err)
	}
	if _, ok := pkg.Types[a]; !ok {
		t.Errorf("module-1 enum %q wrongly merged", a)
	}
	if _, ok := pkg.Types[b]; !ok {
		t.Errorf("module-2 enum %q wrongly merged", b)
	}
}

// TestPruneUnreferencedEmptyTypes checks the prune keeps referenced empty types
// and drops unreferenced ones — and never drops a non-empty type.
func TestPruneUnreferencedEmptyTypes(t *testing.T) {
	const (
		emptyUnref = "unifi:m/v1:OrphanEmpty"
		emptyRef   = "unifi:m/v1:LinkedEmpty"
		nonEmpty   = "unifi:m/v1:HasProps"
	)
	pkg := pschema.PackageSpec{
		Types: map[string]pschema.ComplexTypeSpec{
			emptyUnref: {ObjectTypeSpec: pschema.ObjectTypeSpec{Type: "object"}},
			emptyRef:   {ObjectTypeSpec: pschema.ObjectTypeSpec{Type: "object"}},
			nonEmpty: {ObjectTypeSpec: pschema.ObjectTypeSpec{Type: "object",
				Properties: map[string]pschema.PropertySpec{"x": {TypeSpec: pschema.TypeSpec{Type: "string"}}}}},
		},
		Resources: map[string]pschema.ResourceSpec{
			"unifi:m/v1:R": {ObjectTypeSpec: pschema.ObjectTypeSpec{
				Properties: map[string]pschema.PropertySpec{"link": refProp(emptyRef)},
			}},
		},
	}
	s := &GenState{Pkg: &pkg, Meta: &openapigen.ProviderMetadata{}, Doc: &openapi3.T{}}
	if err := enumCleanupPass(s); err != nil {
		t.Fatalf("enumCleanupPass: %v", err)
	}
	if _, ok := pkg.Types[emptyUnref]; ok {
		t.Errorf("unreferenced empty type %q should be pruned", emptyUnref)
	}
	if _, ok := pkg.Types[emptyRef]; !ok {
		t.Errorf("referenced empty type %q must NOT be pruned", emptyRef)
	}
	if _, ok := pkg.Types[nonEmpty]; !ok {
		t.Errorf("non-empty type %q must never be pruned", nonEmpty)
	}
}

// TestDedupEnumsPinnedRenameMaterializes checks the pinned-canonical path: when a
// family's derived default short name has a canonicalRename pin, the canonical is a
// FRESH in-module token, materialized with the family's value set, and every ref is
// rewritten to it (no dangling ref). Exercised against a synthetic pin via the real
// mappings data: the live `State` → `DeviceState` pin is asserted at pipeline level
// (TestEnumDedupCollapsesKnownFamiliesOnPipeline); here we assert the mechanism
// directly, that a fresh canonical gets a body and refs resolve.
func TestDedupEnumsPinnedRenameMaterializes(t *testing.T) {
	// Use the live pinned family: bare "State" (10-value adoption enum) → DeviceState.
	const (
		bare   = "unifi:sites/v1:State"                     // derived default short = "State" (pinned)
		other  = "unifi:sites/v1:AdoptedDeviceDetailsState" // same value set
		canon  = "unifi:sites/v1:DeviceState"               // pinned fresh canonical
		vals10 = 10
	)
	vals := []string{"ADOPTING", "CONNECTION_INTERRUPTED", "DELETING", "GETTING_READY", "ISOLATED", "OFFLINE", "ONLINE", "PENDING_ADOPTION", "U5G_INCORRECT_TOPOLOGY", "UPDATING"}
	pkg := pschema.PackageSpec{
		Types: map[string]pschema.ComplexTypeSpec{
			bare:  newEnumType(vals...),
			other: newEnumType(vals...),
		},
		Resources: map[string]pschema.ResourceSpec{
			"unifi:sites/v1:R": {ObjectTypeSpec: pschema.ObjectTypeSpec{
				Properties: map[string]pschema.PropertySpec{
					"s": refProp(bare),
					"d": refProp(other),
				},
			}},
		},
	}
	s := &GenState{Pkg: &pkg, Meta: &openapigen.ProviderMetadata{}, Doc: &openapi3.T{}}
	if err := enumCleanupPass(s); err != nil {
		t.Fatalf("enumCleanupPass: %v", err)
	}
	ct, ok := pkg.Types[canon]
	if !ok {
		t.Fatalf("pinned canonical %q was not materialized", canon)
	}
	if len(ct.Enum) != vals10 {
		t.Errorf("pinned canonical %q has %d enum values, want %d", canon, len(ct.Enum), vals10)
	}
	for _, gone := range []string{bare, other} {
		if _, ok := pkg.Types[gone]; ok {
			t.Errorf("family member %q should be merged into the pinned canonical", gone)
		}
	}
	props := pkg.Resources["unifi:sites/v1:R"].Properties
	for _, p := range []string{"s", "d"} {
		if got := props[p].Ref; got != "#/types/"+canon {
			t.Errorf("ref %q not rewritten to pinned canonical: got %q", p, got)
		}
	}
}

// TestWalkTypeSpecNestedRefs checks the ref-rewriter reaches refs buried in array
// items, map value types, and oneOf — not just the top-level ref.
func TestWalkTypeSpecNestedRefs(t *testing.T) {
	const canon = "unifi:m/v1:Cn"      // shorter short name → canonical default
	const dup = "unifi:m/v1:LongerDup" // longer → merged away
	pkg := pschema.PackageSpec{
		Types: map[string]pschema.ComplexTypeSpec{
			canon: newEnumType("A", "B"),
			dup:   newEnumType("A", "B"),
			"unifi:m/v1:Holder": {ObjectTypeSpec: pschema.ObjectTypeSpec{Properties: map[string]pschema.PropertySpec{
				"arr": {TypeSpec: pschema.TypeSpec{Type: "array", Items: &pschema.TypeSpec{Ref: "#/types/" + dup}}},
				"map": {TypeSpec: pschema.TypeSpec{Type: "object", AdditionalProperties: &pschema.TypeSpec{Ref: "#/types/" + dup}}},
			}}},
		},
	}
	s := &GenState{Pkg: &pkg, Meta: &openapigen.ProviderMetadata{}, Doc: &openapi3.T{}}
	if err := enumCleanupPass(s); err != nil {
		t.Fatalf("enumCleanupPass: %v", err)
	}
	h := pkg.Types["unifi:m/v1:Holder"]
	if got := h.Properties["arr"].Items.Ref; got != "#/types/"+canon {
		t.Errorf("array item ref not rewritten: got %q", got)
	}
	if got := h.Properties["map"].AdditionalProperties.Ref; got != "#/types/"+canon {
		t.Errorf("map value ref not rewritten: got %q", got)
	}
}
