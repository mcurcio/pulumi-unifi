package gen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
)

// TestNoUnacknowledgedBreakingDelta is the hard breaking-change forcing function
// (design §4.3). It compares the base-branch golden (path in CONTRACT_BASE_SCHEMA,
// extracted by CI via `git show origin/${BASE}:...`) against the CURRENT committed
// golden (cmd/pulumi-resource-unifi/schema.json — byte-golden test
// TestSchemaMatchesGolden already guarantees committed == regenerated, so parsing
// the committed file is equivalent and avoids a pipeline run) and detects the
// token-INVARIANT breaking classes marked ★ in §4.1: an added required input, a
// removed output property, a type narrowing (string→enum, enum value removal,
// array→scalar), an added replaceOnChanges, or a removed secret. Token
// add/remove/rename are already caught by the byte golden + tokens.txt and are
// deliberately NOT re-detected here.
//
// If ANY ★ class is present the test REQUIRES an acknowledgement — a non-empty
// `### Breaking` subsection under `## [Unreleased]` in CHANGELOG.md — and FAILS
// listing the detected items otherwise. No ★ class => pass regardless of the
// changelog.
//
// SKIPS when CONTRACT_BASE_SCHEMA is unset (local dev) or the base file is absent
// (first introduction). The required `contract` CI job always supplies a base.
func TestNoUnacknowledgedBreakingDelta(t *testing.T) {
	basePath := os.Getenv("CONTRACT_BASE_SCHEMA")
	if basePath == "" {
		t.Skip("CONTRACT_BASE_SCHEMA unset — local dev / first introduction; the required contract CI job supplies the base")
	}
	if _, err := os.Stat(basePath); err != nil {
		t.Skipf("base schema %q not present (first introduction) — nothing to diff against", basePath)
	}

	base := loadSchemaFile(t, basePath)
	// CURRENT surface = the committed golden (loadGoldenSchema, from
	// contract_lint_test.go). The byte-golden test guarantees it equals the
	// regenerated schema, so this is the frozen public surface.
	cur := loadGoldenSchema(t)

	items := detectBreakingDelta(base, cur)
	if len(items) == 0 {
		// No token-invariant breaking class — the changelog is irrelevant here.
		return
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].class != items[j].class {
			return items[i].class < items[j].class
		}
		return items[i].detail < items[j].detail
	})

	ack, ackPath := unreleasedBreakingAck(t)
	if ack {
		return
	}

	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "\n  - %s", it)
	}
	t.Errorf("%d token-invariant breaking change(s) detected vs base %s, but no acknowledgement was found in %s "+
		"(its `## [Unreleased]` section needs a non-empty `### Breaking` bullet). Add a bullet describing the "+
		"change (see api-contract.md §4.3), or revert it:%s",
		len(items), basePath, ackPath, b.String())
}

// breakingItem is one detected token-invariant breaking change (a ★ class from
// design §4.1) between the base golden and the current golden.
type breakingItem struct {
	class  string // e.g. "added-required-input", "removed-output", "removed-secret"
	detail string // human-readable location: "<token>.<prop>" etc.
}

func (b breakingItem) String() string { return b.class + ": " + b.detail }

// detectBreakingDelta compares base→cur for the ★ token-invariant breaking
// classes. It is deliberately CONSERVATIVE: the four flag/shape classes
// (added-required-input, removed-output, added-replaceOnChanges, removed-secret)
// are detected reliably from structural fields; the type-narrowing detectors are
// pragmatic and false-negative-leaning for subtle cases while reliably catching
// enum value removal (the key case). It never flags a token that is absent from
// the base (a new/removed token is caught by the byte golden + tokens.txt).
func detectBreakingDelta(base, cur pschema.PackageSpec) []breakingItem {
	var items []breakingItem

	// --- Resources ---------------------------------------------------------
	for tok, bres := range base.Resources {
		cres, ok := cur.Resources[tok]
		if !ok {
			continue // token removal: caught by the byte golden / tokens.txt
		}

		// (a) added required input: any name required in cur that was not
		// required in base — this covers both a brand-new required input and an
		// existing optional input becoming required (it was not in base's set).
		baseReq := namesSet(bres.RequiredInputs)
		for _, name := range cres.RequiredInputs {
			if !baseReq[name] {
				items = append(items, breakingItem{"added-required-input", tok + "." + name})
			}
		}

		// (b) removed output property (ObjectTypeSpec.Properties are the outputs).
		for name := range bres.Properties {
			if _, present := cres.Properties[name]; !present {
				items = append(items, breakingItem{"removed-output", tok + "." + name})
			}
		}

		// (c/d/e) per-property flag & type changes, on inputs and outputs.
		items = append(items, comparePropMaps(base, cur, tok+" input", bres.InputProperties, cres.InputProperties)...)
		items = append(items, comparePropMaps(base, cur, tok+" output", bres.Properties, cres.Properties)...)
	}

	// --- Functions ---------------------------------------------------------
	for tok, bfn := range base.Functions {
		cfn, ok := cur.Functions[tok]
		if !ok {
			continue
		}
		if bfn.Inputs != nil && cfn.Inputs != nil {
			// Function-input required growth is an added required input.
			baseReq := namesSet(bfn.Inputs.Required)
			for _, name := range cfn.Inputs.Required {
				if !baseReq[name] {
					items = append(items, breakingItem{"added-required-input", tok + " input." + name})
				}
			}
			items = append(items, comparePropMaps(base, cur, tok+" input", bfn.Inputs.Properties, cfn.Inputs.Properties)...)
		}
		if bfn.Outputs != nil && cfn.Outputs != nil {
			for name := range bfn.Outputs.Properties {
				if _, present := cfn.Outputs.Properties[name]; !present {
					items = append(items, breakingItem{"removed-output", tok + " output." + name})
				}
			}
			items = append(items, comparePropMaps(base, cur, tok+" output", bfn.Outputs.Properties, cfn.Outputs.Properties)...)
		}
	}

	// (c) enum value removal — compared at the #/types level, independent of
	// which property references the enum, so it is caught once per (type,value)
	// regardless of how many properties use it. This is the KEY type-narrowing
	// case called out in §4.1(c).
	for tok := range base.Types {
		bSet := enumValueSet(base, tok)
		if bSet == nil {
			continue // not an enum in the base
		}
		cSet := enumValueSet(cur, tok)
		if cSet == nil {
			continue // enum removed or is no longer an enum — a token-shape change,
			// caught by the byte golden; not re-classified here.
		}
		for v := range bSet {
			if !cSet[v] {
				items = append(items, breakingItem{"type-narrowed", "enum " + tok + " dropped value " + v})
			}
		}
	}

	// Nested #/types object properties. A shared object type is referenced by $ref
	// from resource/function properties, so removing, narrowing, un-securing, or
	// adding replaceOnChanges to one of ITS properties never appears in any
	// resource/function's OWN property map — the loops above are blind to it. Diff
	// each non-enum object type present in BOTH base and cur with the SAME machinery
	// (comparePropMaps) plus removed-property detection, keyed by <typeToken>.<prop>.
	for tok, bct := range base.Types {
		if len(bct.Enum) > 0 {
			continue // enum value removal handled above
		}
		cct, ok := cur.Types[tok]
		if !ok {
			continue // token removal: caught by the byte golden / tokens.txt
		}
		if len(cct.Enum) > 0 {
			continue // object -> enum reshape is a token-shape change, caught by the byte golden
		}
		// (b) removed property — an output of the shared object type disappears.
		for name := range bct.Properties {
			if _, present := cct.Properties[name]; !present {
				items = append(items, breakingItem{"removed-output", tok + "." + name})
			}
		}
		// (c/d/e) per-property flag & type changes on properties present in both.
		items = append(items, comparePropMaps(base, cur, tok, bct.Properties, cct.Properties)...)
	}

	return items
}

// comparePropMaps compares properties present in BOTH base and cur for the
// per-property ★ classes: removed secret, added replaceOnChanges, and the
// per-property type narrowings (string→enum, array→scalar). A property present
// in base but absent from cur is handled by the caller (removed-output for
// outputs; a removed input is not breaking).
func comparePropMaps(base, cur pschema.PackageSpec, owner string, bprops, cprops map[string]pschema.PropertySpec) []breakingItem {
	var items []breakingItem
	for name, bp := range bprops {
		cp, ok := cprops[name]
		if !ok {
			continue
		}
		path := owner + "." + name

		// (e) secret removal — leaks a previously-redacted value (also a security
		// regression). Reliable.
		if bp.Secret && !cp.Secret {
			items = append(items, breakingItem{"removed-secret", path})
		}

		// (d) replaceOnChanges added — silently converts an in-place update into
		// destroy/recreate. Reliable.
		if !bp.ReplaceOnChanges && cp.ReplaceOnChanges {
			items = append(items, breakingItem{"added-replaceOnChanges", path})
		}

		// (c) type narrowing (pragmatic, false-negative-leaning).
		items = append(items, detectNarrowing(cur, path, bp.TypeSpec, cp.TypeSpec)...)
	}
	return items
}

// detectNarrowing flags the two per-property narrowings that are cheap and
// unambiguous: a plain string tightened into an enum, and an array collapsed to
// a scalar. Enum VALUE removal is handled globally in detectBreakingDelta. This
// is deliberately conservative — it only fires on clearly-narrowing shapes to
// avoid false positives on additive/neutral changes.
func detectNarrowing(cur pschema.PackageSpec, path string, bt, ct pschema.TypeSpec) []breakingItem {
	var items []breakingItem

	// string → enum: base is a plain string; current is a $ref to a local enum
	// type. (A plain string accepts any value; an enum rejects all but a fixed
	// set, so this narrows the accepted domain.)
	if bt.Ref == "" && bt.Type == "string" {
		if tok := refTypeToken(ct.Ref); tok != "" && enumValueSet(cur, tok) != nil {
			items = append(items, breakingItem{"type-narrowed", path + " (string -> enum " + tok + ")"})
		}
	}

	// array → scalar: base is an array; current is a concrete scalar. Guarded to
	// concrete scalar types so an array→$ref (object/enum) reshape is not
	// mis-flagged.
	if bt.Type == "array" && isScalarType(ct.Type) {
		items = append(items, breakingItem{"type-narrowed", path + " (array -> " + ct.Type + ")"})
	}

	return items
}

// isScalarType reports whether t is one of the concrete primitive scalar types.
func isScalarType(t string) bool {
	switch t {
	case "boolean", "string", "integer", "number":
		return true
	default:
		return false
	}
}

// enumValueSet returns the set of stringified enum values for a #/types token in
// pkg, or nil if the token is not an enum type. Values are stringified with %v so
// string and numeric enums compare uniformly.
func enumValueSet(pkg pschema.PackageSpec, tok string) map[string]bool {
	ct, ok := pkg.Types[tok]
	if !ok || len(ct.Enum) == 0 {
		return nil
	}
	set := make(map[string]bool, len(ct.Enum))
	for _, ev := range ct.Enum {
		set[fmt.Sprintf("%v", ev.Value)] = true
	}
	return set
}

// refTypeToken (returns the in-document type token a `#/types/...` ref points at)
// is defined in pass_enum.go and reused here for the string→enum narrowing check.

// namesSet turns a slice of names into a set.
func namesSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// loadSchemaFile parses a schema.json file at an arbitrary path into a
// PackageSpec (used for the env-supplied base schema; the current surface is
// loaded via loadGoldenSchema).
func loadSchemaFile(t *testing.T, path string) pschema.PackageSpec {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read base schema %s: %v", path, err)
	}
	var pkg pschema.PackageSpec
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatalf("unmarshal base schema %s: %v", path, err)
	}
	return pkg
}

// unreleasedBreakingAck reports whether CHANGELOG.md's `## [Unreleased]` section
// contains a `### Breaking` subsection with at least one non-blank bullet, and
// returns the resolved changelog path for diagnostics. The parser is small and
// forgiving: it scans for the Unreleased H2, then within it (until the next H2)
// for a Breaking H3, then counts bullet lines until the next H3/H2. HTML comment
// lines and blank lines do not count as bullets, so an empty placeholder reads
// as "not acknowledged".
func unreleasedBreakingAck(t *testing.T) (bool, string) {
	t.Helper()
	path, ok := findRepoFile("CHANGELOG.md")
	if !ok {
		return false, "CHANGELOG.md (not found)"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	inUnreleased := false
	inBreaking := false
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "## "):
			inUnreleased = strings.Contains(strings.ToLower(line), "unreleased")
			inBreaking = false
		case strings.HasPrefix(line, "### "):
			// EXACT heading match (after the outer TrimSpace), case-insensitive on
			// the word only. A substring test would let `### Non-Breaking` (or any
			// other heading containing "breaking") satisfy the acknowledgement.
			inBreaking = inUnreleased && strings.EqualFold(line, "### Breaking")
		default:
			if inUnreleased && inBreaking && isBullet(line) {
				return true, path
			}
		}
	}
	return false, path
}

// isBullet reports whether a trimmed line is a Markdown list bullet with content
// after the marker (so a bare "-" or an HTML comment does not count).
func isBullet(line string) bool {
	for _, marker := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(line, marker) && strings.TrimSpace(line[len(marker):]) != "" {
			return true
		}
	}
	return false
}

// findRepoFile walks up from the test's working directory to locate a
// repo-root-relative file (e.g. CHANGELOG.md), mirroring findGolden but
// returning ok=false instead of failing so the caller can report a missing
// acknowledgement file as a normal (non-fatal) delta failure.
func findRepoFile(rel string) (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
