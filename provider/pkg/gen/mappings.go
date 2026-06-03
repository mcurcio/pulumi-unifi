package gen

import (
	_ "embed"
	"fmt"
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
	ExcludedPaths   []string            `yaml:"excludedPaths"`
	Resources       resourceMappings    `yaml:"resources"`
	Functions       functionMappings    `yaml:"functions"`
	Descriptions    descriptionMappings `yaml:"descriptions"`
	Enums           enumMappings        `yaml:"enums"`
	ImmutableFields []string            `yaml:"immutableFields"`
}

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
	return nil
}

// mappingExcludedPaths returns the editorial exclusion list (replaces the former
// Go-literal excludedPaths). Callers must not mutate the returned slice.
func mappingExcludedPaths() []string {
	return loadMappings().ExcludedPaths
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
