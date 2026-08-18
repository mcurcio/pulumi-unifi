package gen

import (
	"fmt"
	"os"
	"sort"
	"testing"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
	"gopkg.in/yaml.v3"
)

// standardsInventoryPath is the committed machine-checked contract inventory,
// repo-root-relative (findGolden walks up to the repo root, so a docs/-rooted
// path resolves the same way as a provider/-rooted golden).
const standardsInventoryPath = "docs/api-standards.yaml"

// standardsInventory is the parsed shape of docs/api-standards.yaml — the SINGLE
// DECLARED SOURCE for the allowed module set and the secret/immutable guarantee
// lists. Both TestContractLint (the convention linter) and
// TestStandardsInventoryMatchesGolden (the doc-truth bijection) read their
// expected sets from here, so there is one declared source consumed by both, not
// two hand-maintained copies.
type standardsInventory struct {
	Version      int                 `yaml:"version"`
	Modules      []string            `yaml:"modules"`
	TokenGrammar string              `yaml:"tokenGrammar"`
	Guarantees   standardsGuarantees `yaml:"guarantees"`
}

// standardsGuarantees carries the machine-checkable claim lists. Each is bound to
// the frozen golden by the predicate helpers below (shared with the linter).
type standardsGuarantees struct {
	// ImmutableInputs: every resource that HAS an input of this name must mark it
	// replaceOnChanges in the golden.
	ImmutableInputs []string `yaml:"immutableInputs"`
	// SecretConfig: every named config variable must be secret:true in the golden.
	SecretConfig []string `yaml:"secretConfig"`
	// SecretProperties: every <typeToken>.<prop> must be secret:true on that
	// #/types entry in the golden; cross-checked against the pipeline source
	// (pass_secret_fields.secretTypeProperties).
	SecretProperties map[string][]string `yaml:"secretProperties"`
}

// loadStandardsInventory reads and parses the committed docs/api-standards.yaml.
// Like the golden it is a committed artifact and must be present; a parse error or
// an empty inventory is a hard failure.
func loadStandardsInventory(t *testing.T) standardsInventory {
	t.Helper()
	b, err := os.ReadFile(findGolden(t, standardsInventoryPath))
	if err != nil {
		t.Fatalf("read standards inventory %s: %v", standardsInventoryPath, err)
	}
	var inv standardsInventory
	if err := yaml.Unmarshal(b, &inv); err != nil {
		t.Fatalf("unmarshal standards inventory %s: %v", standardsInventoryPath, err)
	}
	if inv.Version == 0 {
		t.Fatalf("%s: version is unset (0) — the inventory did not load", standardsInventoryPath)
	}
	if len(inv.Modules) == 0 {
		t.Fatalf("%s: modules: is empty — the inventory did not load", standardsInventoryPath)
	}
	return inv
}

// moduleSet returns the declared <module>/<version> set.
func (inv standardsInventory) moduleSet() map[string]bool {
	set := make(map[string]bool, len(inv.Modules))
	for _, m := range inv.Modules {
		set[m] = true
	}
	return set
}

// modulesInGolden returns the set of <module>/<version> segments used by every
// resource and function token in the golden.
func modulesInGolden(pkg pschema.PackageSpec) map[string]bool {
	set := map[string]bool{}
	for tok := range pkg.Resources {
		if mv := tokenModuleVersion(tok); mv != "" {
			set[mv] = true
		}
	}
	for tok := range pkg.Functions {
		if mv := tokenModuleVersion(tok); mv != "" {
			set[mv] = true
		}
	}
	return set
}

// --- Shared claim predicates (ONE implementation, used by BOTH the linter and the
// bijection) ---------------------------------------------------------------------

// secretConfigViolations returns, for each config name claimed secret, a message
// if it is missing from the golden config or is not secret:true there.
func secretConfigViolations(pkg pschema.PackageSpec, names []string) []string {
	var out []string
	for _, name := range names {
		cfg, ok := pkg.Config.Variables[name]
		if !ok {
			out = append(out, fmt.Sprintf("config %q is claimed secret but is missing from the golden config", name))
			continue
		}
		if !cfg.Secret {
			out = append(out, fmt.Sprintf("config %q must be secret:true in the golden (security regression if unflagged)", name))
		}
	}
	sort.Strings(out)
	return out
}

// secretTypePropertyViolations returns, for each claimed <typeToken>.<prop>, a
// message if the type is absent, the property is absent, or the property is not
// secret:true on that #/types entry in the golden.
func secretTypePropertyViolations(pkg pschema.PackageSpec, claims map[string][]string) []string {
	var out []string
	for _, typeTok := range sortedKeys(claims) {
		ct, ok := pkg.Types[typeTok]
		if !ok {
			out = append(out, fmt.Sprintf("secret type %q not present in golden #/types", typeTok))
			continue
		}
		for _, prop := range claims[typeTok] {
			ps, ok := ct.Properties[prop]
			if !ok {
				out = append(out, fmt.Sprintf("secret property %q missing on golden type %q", prop, typeTok))
				continue
			}
			if !ps.Secret {
				out = append(out, fmt.Sprintf("property %q on type %q must be secret:true in the golden (leaks a credential otherwise)", prop, typeTok))
			}
		}
	}
	sort.Strings(out)
	return out
}

// immutableInputViolations returns, for each claimed immutable input name, a
// message per resource that carries that input but does NOT mark it
// replaceOnChanges in the golden.
func immutableInputViolations(pkg pschema.PackageSpec, names []string) []string {
	var out []string
	for _, name := range names {
		for _, tok := range sortedKeys(pkg.Resources) {
			res := pkg.Resources[tok]
			ps, has := res.InputProperties[name]
			if !has {
				continue
			}
			if !ps.ReplaceOnChanges {
				out = append(out, fmt.Sprintf("immutable input %q on resource %q must be replaceOnChanges (in-place update would be rejected/dropped)", name, tok))
			}
		}
	}
	return out
}

// secretPropertiesPipelineMismatch cross-checks the yaml-declared secretProperties
// against the pipeline source (pass_secret_fields.secretTypeProperties) in BOTH
// directions, so the declared flag, the shipped flag, and the pipeline agree. A
// declaration the pipeline does not set, or a pipeline secret the declaration
// omits, is reported.
func secretPropertiesPipelineMismatch(declared, pipeline map[string][]string) []string {
	d := propsToSet(declared)
	p := propsToSet(pipeline)
	var out []string
	for tok, dprops := range d {
		pprops, ok := p[tok]
		if !ok {
			out = append(out, fmt.Sprintf("%s declares secret type %q which pass_secret_fields.secretTypeProperties does not carry", standardsInventoryPath, tok))
			continue
		}
		for prop := range dprops {
			if !pprops[prop] {
				out = append(out, fmt.Sprintf("%s declares %q.%q secret which the pipeline (secretTypeProperties) does not", standardsInventoryPath, tok, prop))
			}
		}
	}
	for tok, pprops := range p {
		dprops, ok := d[tok]
		if !ok {
			out = append(out, fmt.Sprintf("pipeline (secretTypeProperties) marks type %q secret but %s does not declare it", tok, standardsInventoryPath))
			continue
		}
		for prop := range pprops {
			if !dprops[prop] {
				out = append(out, fmt.Sprintf("pipeline marks %q.%q secret but %s does not declare it", tok, prop, standardsInventoryPath))
			}
		}
	}
	sort.Strings(out)
	return out
}

// propsToSet turns a type->props map into type->set-of-props.
func propsToSet(m map[string][]string) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(m))
	for tok, props := range m {
		set := make(map[string]bool, len(props))
		for _, p := range props {
			set[p] = true
		}
		out[tok] = set
	}
	return out
}

// TestStandardsInventoryMatchesGolden binds docs/api-standards.yaml to the frozen
// schema.json golden (doc-truth, design §3.6):
//
//	(1) MODULE BIJECTION — the set of modules used by the golden's tokens equals
//	    EXACTLY the inventory's modules: list. An undeclared module (in the golden,
//	    not the yaml) AND a dead declaration (in the yaml, not the golden) both fail.
//	    This is MODULE granularity, deliberately (§4.3); the per-token census truth
//	    rests on the byte golden, not this yaml.
//	(2) CLAIM ASSERTIONS — every guarantees.immutableInputs / secretConfig /
//	    secretProperties entry is realized in the golden, delegating the
//	    per-property predicate to the SAME helpers the linter uses (one
//	    implementation). secretProperties is additionally cross-checked against
//	    pass_secret_fields.secretTypeProperties so the declaration, the shipped
//	    flag, and the pipeline source all agree.
func TestStandardsInventoryMatchesGolden(t *testing.T) {
	pkg := loadGoldenSchema(t)
	inv := loadStandardsInventory(t)

	// (1) Module bijection.
	t.Run("ModuleBijection", func(t *testing.T) {
		declared := inv.moduleSet()
		golden := modulesInGolden(pkg)
		for mv := range golden {
			if !declared[mv] {
				t.Errorf("module %q appears in the golden's tokens but is not declared in %s modules: (an undeclared module is a reviewed event)", mv, standardsInventoryPath)
			}
		}
		for mv := range declared {
			if !golden[mv] {
				t.Errorf("module %q is declared in %s but NO golden token uses it (a dead declaration — remove it or the surface regressed)", mv, standardsInventoryPath)
			}
		}
	})

	// (2) Claim assertions — realized in the golden.
	t.Run("ImmutableInputsRealized", func(t *testing.T) {
		for _, v := range immutableInputViolations(pkg, inv.Guarantees.ImmutableInputs) {
			t.Error(v)
		}
	})
	t.Run("SecretConfigRealized", func(t *testing.T) {
		for _, v := range secretConfigViolations(pkg, inv.Guarantees.SecretConfig) {
			t.Error(v)
		}
	})
	t.Run("SecretPropertiesRealized", func(t *testing.T) {
		for _, v := range secretTypePropertyViolations(pkg, inv.Guarantees.SecretProperties) {
			t.Error(v)
		}
	})

	// (2b) secretProperties cross-check against the pipeline source, so the
	// declaration cannot drift from what pass_secret_fields actually marks.
	t.Run("SecretPropertiesMatchPipeline", func(t *testing.T) {
		for _, v := range secretPropertiesPipelineMismatch(inv.Guarantees.SecretProperties, secretTypeProperties) {
			t.Error(v)
		}
	})
}
