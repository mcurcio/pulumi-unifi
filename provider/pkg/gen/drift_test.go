package gen

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/cloudy-sky-software/pulumi-provider-framework/openapi"
)

// goldenTokensPath is the committed token-set golden: the sorted union of every
// resource and function token the pipeline emits, one per line. It is the
// single highest-leverage spec-bump guard — a bump that drifts a token, leaks a
// junk resource, collides variant tokens, or under-covers a newly-writable
// entity changes this set, and TestTokenSetMatchesGolden turns that silent
// change into a loud, reviewable diff.
const goldenTokensPath = "testdata/tokens.txt"

// pipelineTokens returns the sorted union of resource + function tokens from a
// full in-process pipeline run. Each token is prefixed with its kind
// (resource:/function:) so a token that flips kind across a bump is also caught.
func pipelineTokens(t *testing.T) []string {
	t.Helper()
	pkg, _ := runPipelineTyped(t)
	toks := make([]string, 0, len(pkg.Resources)+len(pkg.Functions))
	for tok := range pkg.Resources {
		toks = append(toks, "resource:"+tok)
	}
	for tok := range pkg.Functions {
		toks = append(toks, "function:"+tok)
	}
	sort.Strings(toks)
	return toks
}

// TestTokenSetMatchesGolden regenerates the resource + function token set and
// diffs it against the committed golden. Updating the golden is a deliberate,
// reviewed step: re-run with `UPDATE_GOLDEN=1 go test ./pkg/gen/` (or
// `make test`) to rebase it after an intended token change.
func TestTokenSetMatchesGolden(t *testing.T) {
	got := strings.Join(pipelineTokens(t), "\n") + "\n"

	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(goldenTokensPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenTokensPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden %s (%d tokens)", goldenTokensPath, len(pipelineTokens(t)))
		return
	}

	wantBytes, err := os.ReadFile(goldenTokensPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (regenerate with UPDATE_GOLDEN=1)", goldenTokensPath, err)
	}
	want := string(wantBytes)

	if got == want {
		return
	}

	// Produce a readable added/removed diff rather than dumping both blobs.
	gotSet := lineSet(got)
	wantSet := lineSet(want)
	var added, removed []string
	for l := range gotSet {
		if !wantSet[l] {
			added = append(added, l)
		}
	}
	for l := range wantSet {
		if !gotSet[l] {
			removed = append(removed, l)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	t.Errorf("token set drifted from %s (rebase with UPDATE_GOLDEN=1 after review)\n  added (%d):\n%s\n  removed (%d):\n%s",
		goldenTokensPath, len(added), indent(added), len(removed), indent(removed))
}

// fixedDoc returns the sanitized + FixOpenAPIDoc'd spec — the exact document
// pulschema consumes, and the one ExcludedPaths is resolved against.
func fixedDoc(t *testing.T) *openapi3.T {
	t.Helper()
	specBytes, err := os.ReadFile(findSpec(t))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	sanitized, err := SanitizeSpecBytes(specBytes)
	if err != nil {
		t.Fatalf("SanitizeSpecBytes: %v", err)
	}
	doc := openapi.GetOpenAPISpec(sanitized)
	if err := FixOpenAPIDoc(doc); err != nil {
		t.Fatalf("FixOpenAPIDoc: %v", err)
	}
	return doc
}

// TestExcludedPathsResolve is the dead-entry guard (A-M0.3): every excludedPaths
// entry must still match a path in the spec pulschema sees. A removed or renamed
// path in a spec bump would otherwise leave a stale exclusion that silently
// stops dropping anything (re-leaking a junk resource).
func TestExcludedPathsResolve(t *testing.T) {
	doc := fixedDoc(t)
	for _, p := range excludedPaths {
		if doc.Paths.Find(p) == nil {
			t.Errorf("excludedPaths entry %q no longer matches any spec path (dead exclusion — re-check on spec bump)", p)
		}
	}
}

// TestNoDuplicateShortTokenNames is the collision guard (A-M0.3): the resource
// and function token sets must have no duplicate short names (the segment after
// the last ":"). pulschema's per-variant split can collide bare names like
// Standard/Mac/Ipv4 across entities on a future bump; a collision means two
// different schema entries fight over one SDK class name.
func TestNoDuplicateShortTokenNames(t *testing.T) {
	pkg, _ := runPipelineTyped(t)

	check := func(kind string, toks map[string]bool) {
		seen := map[string]string{} // short name -> first full token
		for tok := range toks {
			short := tok
			if i := strings.LastIndex(tok, ":"); i >= 0 {
				short = tok[i+1:]
			}
			if prior, dup := seen[short]; dup {
				t.Errorf("duplicate %s short name %q: %q and %q", kind, short, prior, tok)
			} else {
				seen[short] = tok
			}
		}
	}

	resources := map[string]bool{}
	for tok := range pkg.Resources {
		resources[tok] = true
	}
	functions := map[string]bool{}
	for tok := range pkg.Functions {
		functions[tok] = true
	}
	check("resource", resources)
	check("function", functions)
}

func lineSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out[l] = true
		}
	}
	return out
}

func indent(lines []string) string {
	if len(lines) == 0 {
		return "    (none)"
	}
	return "    " + strings.Join(lines, "\n    ")
}
