package gen

import (
	_ "embed"
	"fmt"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
)

// mappings.go is the loader for the editorial api→pulumi mapping layer. The
// mapping decisions that stabilize the public Pulumi surface across UniFi spec
// versions live in mappings.yaml (DATA, not code — see
// docs/reviews/MAPPING-LAYER.md); this file parses that data once and the gen
// passes (a generic, spec-version-agnostic engine) read it through the accessors
// below. There are no naming/const/plural/exclusion literals embedded in Go —
// the only editorial artifact is mappings.yaml.

// mappingsYAML is the embedded editorial mapping file. It is read at codegen/build
// time (the gen binary), so embedding it makes the engine independent of the
// process working directory — the same reason the plugin embeds its three
// generated artifacts.
//
//go:embed mappings.yaml
var mappingsYAML []byte

// resourceMappings is the resource-naming editorial layer.
type resourceMappings struct {
	// EntityPrefixes maps a discriminated entity's collection (create) path to the
	// PascalCase prefix its variant resource tokens get.
	EntityPrefixes map[string]string `yaml:"entityPrefixes"`
}

// functionMappings is the function-token normalization editorial layer.
type functionMappings struct {
	// AcronymFixups normalizes all-caps acronym fragments (VPN → Vpn).
	AcronymFixups map[string]string `yaml:"acronymFixups"`
	// IrregularSingulars repairs broken trailing-"s" singulars (Countrie → Country).
	IrregularSingulars map[string]string `yaml:"irregularSingulars"`
	// ExplicitRenames settles get/list near-duplicate pairs a rule cannot derive.
	ExplicitRenames map[string]string `yaml:"explicitRenames"`
}

// descriptionMappings holds the by-exception description overrides (the engine
// synthesizes a default for every token; these pin the cases where that default
// is too weak), keyed by final token short name.
type descriptionMappings struct {
	// Inputs pins descriptions for well-known input properties shared across many
	// resources (e.g. siteId), keyed by SDK property name.
	Inputs    map[string]string `yaml:"inputs"`
	Resources map[string]string `yaml:"resources"`
	Functions map[string]string `yaml:"functions"`
}

// enumMappings is the enum-dedup editorial layer. The engine merges
// structurally-identical enums and derives a default canonical name (the shortest
// member); CanonicalRename pins the exceptions where that default is too generic,
// keyed by the derived default short name → the chosen short name.
type enumMappings struct {
	CanonicalRename map[string]string `yaml:"canonicalRename"`
}

// mappingsFile is the parsed shape of mappings.yaml: the whole editorial layer.
type mappingsFile struct {
	ExcludedPaths    []string            `yaml:"excludedPaths"`
	ExcludeResources []string            `yaml:"excludeResources"`
	Resources        resourceMappings    `yaml:"resources"`
	Functions        functionMappings    `yaml:"functions"`
	Descriptions     descriptionMappings `yaml:"descriptions"`
	Enums            enumMappings        `yaml:"enums"`
	ImmutableFields  []string            `yaml:"immutableFields"`
	// Aliases maps a live resource token to its former tokens (pass_aliases.go).
	Aliases map[string][]string `yaml:"aliases"`
	// OutputOnly lists "<token>.<property>" inputs kept only as outputs
	// (pass_output_only.go).
	OutputOnly []string `yaml:"outputOnly"`
	// Singletons are the site-scoped documents the engine serves
	// (pass_singletons.go).
	Singletons []Singleton `yaml:"singletons"`
}

// Singleton is one mappings.yaml `singletons:` entry: a site-scoped document
// managed as one resource per site, served by the engine through the official
// binding rather than by the framework (docs/design/private-binding.md §S2.5).
type Singleton struct {
	// Path is the spec path (GET + PUT), e.g. /v1/sites/{siteId}/acl-rules/ordering.
	Path string `yaml:"path" json:"path"`
	// Token is the resource token the gen pass emits into the golden.
	Token string `yaml:"token" json:"token"`
	// Property is the managed list property of the PUT body (SDK name).
	Property string `yaml:"property" json:"property"`
	// Merge is how the managed list meets the live one; only leadingBlock today.
	Merge string `yaml:"merge" json:"merge"`
}

// MergeLeadingBlock is the only singleton merge strategy: the managed ids form
// the leading block of the live order, in the declared order.
const MergeLeadingBlock = "leadingBlock"

var (
	loadedMappings   *mappingsFile
	loadMappingsOnce sync.Once
)

// loadMappings parses the embedded mappings.yaml exactly once. A parse error or a
// structurally empty file is a build-time defect (the embedded data is part of the
// source), so it fails loudly via contract.Failf — the codegen-path idiom.
func loadMappings() *mappingsFile {
	loadMappingsOnce.Do(func() {
		var m mappingsFile
		if err := yaml.Unmarshal(mappingsYAML, &m); err != nil {
			contract.Failf("loading mappings.yaml: %v", err)
		}
		if err := m.validate(); err != nil {
			contract.Failf("mappings.yaml: %v", err)
		}
		loadedMappings = &m
	})
	return loadedMappings
}

// validate sanity-checks the loaded mappings: the editorial file must actually
// carry content (an empty/garbled embed should fail loud, not silently emit an
// un-pinned surface).
func (m *mappingsFile) validate() error {
	if len(m.ExcludedPaths) == 0 {
		return fmt.Errorf("excludedPaths is empty (mappings.yaml not loaded?)")
	}
	if len(m.Resources.EntityPrefixes) == 0 {
		return fmt.Errorf("resources.entityPrefixes is empty (mappings.yaml not loaded?)")
	}
	return m.validateSingletons()
}

// validateSingletons checks every singletons: entry: complete, a supported
// merge, its path also excluded from pulschema, and no token listed twice.
func (m *mappingsFile) validateSingletons() error {
	excluded := map[string]bool{}
	for _, p := range m.ExcludedPaths {
		excluded[p] = true
	}
	seen := map[string]bool{}
	for _, sg := range m.Singletons {
		if sg.Path == "" || sg.Token == "" || sg.Property == "" {
			return fmt.Errorf("singletons: entry %+v needs path, token and property", sg)
		}
		if sg.Merge != MergeLeadingBlock {
			return fmt.Errorf("singletons: %s: merge %q is not supported (want %s)", sg.Token, sg.Merge, MergeLeadingBlock)
		}
		if !excluded[sg.Path] {
			return fmt.Errorf("singletons: %s: path %s must also be in excludedPaths (the framework must not see it)", sg.Token, sg.Path)
		}
		if seen[sg.Token] {
			return fmt.Errorf("singletons: token %s is listed twice", sg.Token)
		}
		seen[sg.Token] = true
	}
	return nil
}

// mappingExcludedPaths returns the editorial exclusion list (replaces the former
// Go-literal excludedPaths). Callers must not mutate the returned slice.
func mappingExcludedPaths() []string {
	return loadMappings().ExcludedPaths
}

// mappingExcludeResources returns the editorial resource-token exclusion list
// (D-M3.3): action/batch RPC tokens dropped from the managed resource set while
// their GET-derived data sources are preserved (path-level exclusion cannot do
// this — see pass_exclude_resources.go). Callers must not mutate the returned slice.
func mappingExcludeResources() []string {
	return loadMappings().ExcludeResources
}

// entityPrefix returns the resource-token prefix pinned for a discriminated
// entity's collection path, and whether one is mapped.
func entityPrefix(collPath string) (string, bool) {
	p, ok := loadMappings().Resources.EntityPrefixes[collPath]
	return p, ok
}

// acronymFixup returns the PascalCase form pinned for an all-caps acronym
// fragment (keyed by its upper-cased form), and whether one is mapped.
func acronymFixup(upper string) (string, bool) {
	v, ok := loadMappings().Functions.AcronymFixups[upper]
	return v, ok
}

// irregularSingularMap returns the broken-singular → correct-singular suffix
// table. Callers iterate it over sorted keys for determinism.
func irregularSingularMap() map[string]string {
	return loadMappings().Functions.IrregularSingulars
}

// explicitFunctionRename returns the pinned replacement for a function short name
// that a mechanical rule cannot derive, and whether one is mapped.
func explicitFunctionRename(short string) (string, bool) {
	v, ok := loadMappings().Functions.ExplicitRenames[short]
	return v, ok
}

// resourceDescriptionOverride returns the pinned description for a resource token
// short name (where the synthesized default is too weak), and whether one exists.
func resourceDescriptionOverride(short string) (string, bool) {
	v, ok := loadMappings().Descriptions.Resources[short]
	return v, ok
}

// functionDescriptionOverride returns the pinned description for a function token
// short name, and whether one exists.
func functionDescriptionOverride(short string) (string, bool) {
	v, ok := loadMappings().Descriptions.Functions[short]
	return v, ok
}

// inputDescription returns the pinned description for a well-known input property
// (keyed by SDK property name), and whether one exists. Used for properties
// synthesized from a path parameter (e.g. siteId) that carry no spec description.
func inputDescription(prop string) (string, bool) {
	v, ok := loadMappings().Descriptions.Inputs[prop]
	return v, ok
}

// enumCanonicalRename returns the pinned canonical short name for a merged enum
// family whose engine-derived default short name is defaultShort, and whether a
// pin exists. Absent → the engine keeps its derived default.
func enumCanonicalRename(defaultShort string) (string, bool) {
	v, ok := loadMappings().Enums.CanonicalRename[defaultShort]
	return v, ok
}

// immutableFieldPins returns the by-exception immutable input-property names
// (pass_replace_on_changes.go marks any resource that actually carries one).
// Callers must not mutate the returned slice.
func immutableFieldPins() []string {
	return loadMappings().ImmutableFields
}

// aliasPins returns the live-token -> former-tokens table (pass_aliases.go).
func aliasPins() map[string][]string {
	return loadMappings().Aliases
}

// outputOnlyPins returns the "<token>.<property>" inputs kept only as outputs
// (pass_output_only.go). Callers must not mutate the returned slice.
func outputOnlyPins() []string {
	return loadMappings().OutputOnly
}

// Singletons returns the engine-served singleton entries, sorted by token. The
// gen pass builds their schema; the private generator writes them into the
// plugin's surface so the dispatcher can route them.
func Singletons() []Singleton {
	out := append([]Singleton(nil), loadMappings().Singletons...)
	sort.Slice(out, func(i, j int) bool { return out[i].Token < out[j].Token })
	return out
}

// singletonTokens is the set of resource tokens served outside the framework.
func singletonTokens() map[string]bool {
	out := map[string]bool{}
	for _, sg := range loadMappings().Singletons {
		out[sg.Token] = true
	}
	return out
}
