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

// TestNoUbiquitiArtifacts fails if a tracked file looks like Ubiquiti's: a
// class file or jar (other than our own synthetic fixtures, which sit next to
// their sources), anything under a fields/ directory, or a JSON file shaped
// like api/fields (an object whose leaves are all validation strings). See
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

func ubiquitiArtifact(root, f, fixtures string) string {
	lower := strings.ToLower(f)
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
		if err == nil && looksLikeFieldFile(b) {
			return "shaped like an api/fields file (an object of validation strings)"
		}
	}
	return ""
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
		"README.md": false,
	} {
		pin, _ := FindRepoFile("uos/pin.env")
		root := filepath.Dir(filepath.Dir(pin))
		if got := ubiquitiArtifact(root, f, "provider/pkg/uos/testdata/classes/") != ""; got != bad {
			t.Errorf("ubiquitiArtifact(%s) = %v, want %v", f, got, bad)
		}
	}
}
