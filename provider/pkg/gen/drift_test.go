package gen

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
