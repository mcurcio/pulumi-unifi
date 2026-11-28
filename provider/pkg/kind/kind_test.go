package kind

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func sample() *Model {
	return &Model{
		Source: Source{UOSVersion: "1", NetworkVersion: "2", InstallerSHA256: "abc", ExtractorVersion: "1"},
		Kinds: []*Kind{
			{Name: "zeta", API: APIV2, Collection: "zeta", Fields: map[string]*Field{
				"b": {WireName: "b", Type: TypeString, Source: SourceBytecode},
				"a": {WireName: "a", Type: TypeObject, Source: SourceBytecode, Fields: map[string]*Field{
					"y": {WireName: "y", Type: TypeInt, Default: json.RawMessage("5"), Source: SourceBytecode},
				}},
				"l": {WireName: "l", Type: TypeArray, Source: SourceBytecode, Items: &Field{Type: TypeObject, Source: SourceBytecode,
					Fields: map[string]*Field{"n": {WireName: "n", Type: TypeString, Source: SourceBytecode}}}},
			}},
			{Name: "Alpha", API: APIClassic, Collection: "rest/alpha", Fields: map[string]*Field{}},
			{Name: "alpha", API: APIV2, Collection: "alpha", Fields: map[string]*Field{},
				Polymorph: &Polymorph{Prop: "t", Variants: map[string]map[string]*Field{
					"B": {"vb": {WireName: "vb", Type: TypeBool, Source: SourceBytecode}},
					"A": {"va": {WireName: "va", Type: TypeBool, Source: SourceBytecode}},
				}}},
		},
	}
}

func TestCanonicalSortsAndRoundTrips(t *testing.T) {
	m := sample()
	b, err := Canonical(m)
	if err != nil {
		t.Fatal(err)
	}
	// Kinds sorted by API then name; the input order is left alone.
	if m.Kinds[0].Name != "zeta" {
		t.Error("Canonical reordered the caller's slice")
	}
	s := string(b)
	if !(strings.Index(s, `"rest/alpha"`) < strings.Index(s, `"collection": "alpha"`) && strings.Index(s, `"collection": "alpha"`) < strings.Index(s, `"zeta"`)) {
		t.Errorf("kinds not in canonical order:\n%s", s)
	}
	if !strings.HasSuffix(s, "}\n") || strings.Index(s, `"a"`) > strings.Index(s, `"b"`) {
		t.Error("keys not sorted / no trailing newline")
	}
	m2, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := Canonical(m2)
	if !bytes.Equal(b, b2) {
		t.Error("Parse(Canonical(m)) does not round-trip")
	}
	d1, _ := Digest(m)
	d2, _ := Digest(m2)
	if d1 != d2 || len(d1) != 64 {
		t.Errorf("digests %s %s", d1, d2)
	}
	if _, err := Parse([]byte("{")); err == nil {
		t.Error("bad JSON parsed")
	}
}

func TestCanonicalJSONKeepsNumbers(t *testing.T) {
	b, err := CanonicalJSON([]byte(`{"z": 1.50, "a": [3, {"y": 1e3, "b": "<&>"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": [\n    3,\n    {\n      \"b\": \"<&>\",\n      \"y\": 1e3\n    }\n  ],\n  \"z\": 1.50\n}\n"
	if string(b) != want {
		t.Errorf("CanonicalJSON =\n%s\nwant\n%s", b, want)
	}
}

func TestWalkAndFind(t *testing.T) {
	m := sample()
	var paths []string
	m.Find(APIV2, "zeta").Walk(func(p string, _ *Field) { paths = append(paths, p) })
	if got := strings.Join(paths, " "); got != "a a.y b l l[].n" {
		t.Errorf("Walk = %s", got)
	}
	paths = nil
	m.Find(APIV2, "alpha").Walk(func(p string, _ *Field) { paths = append(paths, p) })
	if got := strings.Join(paths, " "); got != "va vb" {
		t.Errorf("Walk with variants = %s", got)
	}
	if m.Find(APIClassic, "zeta") != nil || m.Find(APIClassic, "Alpha") == nil {
		t.Error("Find keys on API and name")
	}
}
