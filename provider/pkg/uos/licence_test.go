package uos

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoUbiquitiArtifacts fails if a tracked (or unignored) file looks like
// Ubiquiti's or like bulk text extracted from it: a class file or jar (other
// than our own synthetic fixtures, which sit next to their sources), anything
// under a fields/ directory, a JSON file shaped like api/fields (an object
// whose leaves are all validation strings), an extracted model (kind.Model:
// a top-level kinds list with pinned-source provenance), a generated private
// schema or surface (any JSON naming a unifi:network: token), an archive or
// ELF binary (the installer, image.tar), or any file over maxFileBytes. See
// docs/design/private-binding.md §9.
func TestNoUbiquitiArtifacts(t *testing.T) {
	pin, err := FindRepoFile("uos/pin.env")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(pin))
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v (this gate needs git)", err)
	}
	const fixtures = "provider/pkg/uos/testdata/classes/"
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if f == "" {
			continue
		}
		if msg := ubiquitiArtifact(root, f, fixtures); msg != "" {
			t.Errorf("%s: %s", f, msg)
		}
	}
}

// fixtureModel is our own synthetic model, the one kind.Model-shaped file
// allowed in the tree.
const fixtureModel = "provider/pkg/uos/testdata/fixture-model.json"

// maxFileBytes: no source file in this repo comes near it; an installer,
// image or extracted dump does.
const maxFileBytes = 4 << 20

// tier2Seed is the Tier-2 controller-state seed, committed on main before
// the private binding (a Git LFS object; see test/e2e/README.md). It is the
// one archive allowed in the tree; this gate does not re-decide it.
const tier2Seed = "test/e2e/unifi-seed.tgz"

func ubiquitiArtifact(root, f, fixtures string) string {
	lower := strings.ToLower(f)
	if f == tier2Seed {
		return ""
	}
	if msg := binaryArtifact(filepath.Join(root, f), lower); msg != "" {
		return msg
	}
	switch {
	case strings.HasSuffix(lower, ".jar") || strings.HasSuffix(lower, ".war"):
		return "a jar must never be committed"
	case strings.HasSuffix(lower, ".class"):
		if !strings.HasPrefix(f, fixtures) {
			return "a class file outside the synthetic fixtures"
		}
		// Our own fixtures: each class (or inner class) has its source.
		rel := strings.TrimSuffix(strings.TrimPrefix(f, fixtures), ".class")
		if i := strings.IndexByte(rel, '$'); i >= 0 {
			rel = rel[:i]
		}
		src := filepath.Join(root, "provider/pkg/uos/testdata/java/fixture", rel+".java")
		if _, err := os.Stat(src); err != nil {
			return "a fixture class with no self-written source next to it"
		}
	case strings.Contains("/"+lower, "/fields/"):
		return "a fields/ directory (api/fields is Ubiquiti's)"
	case strings.HasSuffix(lower, ".json"):
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			break
		}
		if looksLikeFieldFile(b) {
			return "shaped like an api/fields file (an object of validation strings)"
		}
		if f != fixtureModel && looksLikeModel(b) {
			return "shaped like an extracted model (kinds + pinned-source provenance)"
		}
		if bytes.Contains(b, []byte(`"unifi:network:`)) {
			return "names a unifi:network: token (a generated private schema or surface)"
		}
	}
	return ""
}

// binaryArtifact flags archives, ELF binaries and oversized files.
func binaryArtifact(path, lower string) string {
	for _, ext := range []string{".tar", ".tar.gz", ".tgz", ".tar.xz", ".tar.zst"} {
		if strings.HasSuffix(lower, ext) {
			return "an archive (an image or installer) must never be committed"
		}
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	if fi.Size() > maxFileBytes {
		return "larger than 4 MiB (an installer, image or extracted dump?)"
	}
	if fh, err := os.Open(path); err == nil {
		head := make([]byte, 4)
		n, _ := fh.Read(head)
		fh.Close()
		if n == 4 && bytes.Equal(head, []byte("\x7fELF")) {
			return "an ELF binary (the installer or a built plugin) must never be committed"
		}
	}
	return ""
}

// looksLikeModel: a JSON object with a top-level kinds list that is either
// non-empty or carries pinned-source provenance (networkVersion or
// integrationSha256), i.e. kind.Model. An empty surface.json ({"kinds": [],
// "source": {}}) is not flagged.
func looksLikeModel(b []byte) bool {
	var v struct {
		Kinds  *[]json.RawMessage `json:"kinds"`
		Source map[string]any     `json:"source"`
	}
	if json.Unmarshal(b, &v) != nil || v.Kinds == nil {
		return false
	}
	if len(*v.Kinds) > 0 {
		return true
	}
	for _, k := range []string{"networkVersion", "integrationSha256"} {
		if s, _ := v.Source[k].(string); s != "" {
			return true
		}
	}
	return false
}

// looksLikeFieldFile: a JSON object whose leaves are all strings (through
// nested objects and lists), with at least five leaves and at least one
// "true|false" rule.
func looksLikeFieldFile(b []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(b))
	var v any
	if dec.Decode(&v) != nil {
		return false
	}
	if _, ok := v.(map[string]any); !ok {
		return false
	}
	leaves, bools, ok := 0, 0, true
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		case string:
			leaves++
			if t == "true|false" || t == "false|true" {
				bools++
			}
		default:
			ok = false
		}
	}
	walk(v)
	return ok && leaves >= 5 && bools >= 1
}

func TestLooksLikeFieldFile(t *testing.T) {
	yes := `{"a": "true|false", "b": ".{1,3}", "c": ["x"], "d": {"e": "", "f": "1|2"}}`
	for s, want := range map[string]bool{
		yes:                   true,
		`{"a": "true|false"}`: false, // too small
		`{"a": "true|false", "b": "", "c": "", "d": "", "e": 1}`: false, // non-string leaf
		`[1, 2]`: false,
		`nope`:   false,
	} {
		if got := looksLikeFieldFile([]byte(s)); got != want {
			t.Errorf("looksLikeFieldFile(%s) = %v, want %v", s, got, want)
		}
	}
	for f, bad := range map[string]bool{
		"x/ace.jar": true, "a/fields/Foo.json": true, "Foo.class": true,
		"provider/pkg/uos/testdata/classes/com/ubnt/fixture/Nope.class":    true,
		"provider/pkg/uos/testdata/classes/com/ubnt/fixture/Mode.class":    false,
		"provider/pkg/uos/testdata/classes/com/ubnt/fixture/Plain$1.class": false,
		"README.md":   false,
		"x/image.tar": true, "x/installer.tar.gz": true,
	} {
		pin, _ := FindRepoFile("uos/pin.env")
		root := filepath.Dir(filepath.Dir(pin))
		if got := ubiquitiArtifact(root, f, "provider/pkg/uos/testdata/classes/") != ""; got != bad {
			t.Errorf("ubiquitiArtifact(%s) = %v, want %v", f, got, bad)
		}
	}
}

func TestExtractedArtifactShapes(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("model.json", []byte(`{"kinds":[{"kind":"x","secret":true,"itemGet":false}],"source":{"networkVersion":"1"}}`))
	write("prov.json", []byte(`{"kinds":[],"source":{"integrationSha256":"ab"}}`))
	write("surface.json", []byte(`{"kinds":[],"source":{}}`))
	write("schema.json", []byte(`{"resources":{"unifi:network:QosRule":{}}}`))
	write("official.json", []byte(`{"resources":{"unifi:sites/v1:Gateway":{}}}`))
	write("installer", append([]byte("\x7fELF"), 1, 2, 3))
	write("big.bin", make([]byte, maxFileBytes+1))
	write("main.go", []byte("package main\n"))
	for f, bad := range map[string]bool{
		"model.json": true, "prov.json": true, "surface.json": false,
		"schema.json": true, "official.json": false,
		"installer": true, "big.bin": true, "main.go": false,
	} {
		if got := ubiquitiArtifact(dir, f, "none/") != ""; got != bad {
			t.Errorf("ubiquitiArtifact(%s) = %v, want %v", f, got, bad)
		}
	}
	// The synthetic fixture model is the one exemption.
	pin, _ := FindRepoFile("uos/pin.env")
	root := filepath.Dir(filepath.Dir(pin))
	if msg := ubiquitiArtifact(root, fixtureModel, "none/"); msg != "" {
		t.Errorf("fixture model flagged: %s", msg)
	}
}
