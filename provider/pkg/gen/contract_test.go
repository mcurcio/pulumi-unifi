package gen

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

// goldenSchemaPath is the committed public-surface golden, located by walking up
// from the test working dir to the repo root (same idiom as findSpec in
// drift_test.go). It IS the contract; the pipeline must reproduce it
// byte-for-byte. The path is relative to the provider/ module dir, which the
// walk resolves regardless of where `go test` is invoked from.
const goldenSchemaPath = "cmd/pulumi-resource-unifi/schema.json"

// goldenMetadataPath is the committed CRUD/name-map golden — the internal
// contract the runtime framework consumes. Like schema.json it is frozen
// byte-for-byte and owned by `make schema` (the entrypoint writer); the pipeline
// must reproduce it exactly via the shared gen.MarshalMetadataJSON serializer.
const goldenMetadataPath = "cmd/pulumi-resource-unifi/metadata.json"

// findGolden walks up from the test's working directory to locate a committed
// golden artifact by its provider-relative path, mirroring findSpec. It is
// independent of where `go test` is invoked from. A missing golden is always a
// hard failure (unlike the fetched spec, the golden is committed and must be
// present).
func findGolden(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("committed golden %s not found walking up from working dir (it is owned by `make schema`)", rel)
		}
		dir = parent
	}
}

// TestSchemaMatchesGolden regenerates schema.json in-process from the pinned
// spec (runPipeline -> gen.MarshalSchemaJSON, the EXACT bytes the entrypoint
// writes, via the shared serializer) and asserts byte-equality with the
// committed golden. A spec bump not followed by `make schema` + commit fails
// here. READ-ONLY: there is no UPDATE_GOLDEN branch — the golden is owned by
// `make generate_schema` (the entrypoint writer). On mismatch it emits a
// bounded added/removed-line diff (mirroring drift_test.go), never a 223 KB
// blob dump.
func TestSchemaMatchesGolden(t *testing.T) {
	specBytes, err := os.ReadFile(findSpec(t))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}

	got, _, _ := runPipeline(t, specBytes)

	wantBytes, err := os.ReadFile(findGolden(t, goldenSchemaPath))
	if err != nil {
		t.Fatalf("read golden %s: %v (rebase with `make schema`)", goldenSchemaPath, err)
	}

	if string(got) == string(wantBytes) {
		return
	}

	// Produce a bounded added/removed line diff rather than dumping both 223 KB
	// blobs. The companion TestTokenSetMatchesGolden gives the human-readable
	// "which resources changed" summary; this shows the raw line-level drift.
	gotSet := lineSet(string(got))
	wantSet := lineSet(string(wantBytes))
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
	t.Errorf("schema.json drifted from committed golden %s (rebase with `make schema` after review)\n  added (%d):\n%s\n  removed (%d):\n%s",
		goldenSchemaPath, len(added), indent(boundedLines(added)), len(removed), indent(boundedLines(removed)))
}

// TestMetadataMatchesGolden regenerates metadata.json in-process from the pinned
// spec (runPipeline -> gen.MarshalMetadataJSON, the EXACT bytes the entrypoint
// writes, via the shared serializer) and asserts byte-equality with the
// committed golden. A spec bump not followed by `make schema` + commit fails
// here. READ-ONLY: there is no UPDATE_GOLDEN branch — the golden is owned by
// `make generate_schema` (the entrypoint writer). On mismatch it emits a bounded
// added/removed-line diff, mirroring TestSchemaMatchesGolden.
func TestMetadataMatchesGolden(t *testing.T) {
	specBytes, err := os.ReadFile(findSpec(t))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}

	_, got, _ := runPipeline(t, specBytes)

	wantBytes, err := os.ReadFile(findGolden(t, goldenMetadataPath))
	if err != nil {
		t.Fatalf("read golden %s: %v (rebase with `make schema`)", goldenMetadataPath, err)
	}

	if string(got) == string(wantBytes) {
		return
	}

	// Bounded added/removed line diff rather than dumping both blobs.
	gotSet := lineSet(string(got))
	wantSet := lineSet(string(wantBytes))
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
	t.Errorf("metadata.json drifted from committed golden %s (rebase with `make schema` after review)\n  added (%d):\n%s\n  removed (%d):\n%s",
		goldenMetadataPath, len(added), indent(boundedLines(added)), len(removed), indent(boundedLines(removed)))
}

// boundedLines caps a diff slice so a large surface change cannot dump the whole
// 223 KB artifact into the test log; the count in the header still reports the
// true total.
func boundedLines(lines []string) []string {
	const maxLines = 40
	if len(lines) <= maxLines {
		return lines
	}
	out := append([]string{}, lines[:maxLines]...)
	return append(out, "... ("+strconv.Itoa(len(lines)-maxLines)+" more)")
}
