// Package private holds the private-API binding's committed editorial facts
// (kinds.yaml) and, in later slices, its generator, engine and HTTP binding
// (docs/design/private-binding.md).
package private

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
)

//go:embed kinds.yaml
var kindsYAML []byte

// KindsYAML returns the embedded overrides file.
func KindsYAML() []byte { return kindsYAML }

// Overrides is kinds.yaml.
type Overrides struct {
	Version          int         `yaml:"version"`
	Expose           []Expose    `yaml:"expose"`
	OfficialTwins    []Twin      `yaml:"officialTwins"`
	ItemGet          []ItemGet   `yaml:"itemGet"`
	ImplicitRequired []FieldList `yaml:"implicitRequired"`
	Rules            []Rule      `yaml:"rules"`
	Defaults         []Default   `yaml:"defaults"`
	CreateOnly       []FieldList `yaml:"createOnly"`
	NotReturned      []FieldList `yaml:"notReturned"`
	NotSecret        []FieldList `yaml:"notSecret"`
	Rename           []Rename    `yaml:"rename"`
}

// Ref names a kind of the model.
type Ref struct {
	Kind string `yaml:"kind"`
	API  string `yaml:"api,omitempty"` // v2 (default) or classic
}

// APIName is the ref's API with the default applied.
func (r Ref) APIName() string {
	if r.API == "" {
		return kind.APIV2
	}
	return r.API
}

func (r Ref) String() string { return r.APIName() + ":" + r.Kind }

// Expose allowlists one kind.
type Expose struct {
	Ref        `yaml:",inline"`
	Token      string   `yaml:"token"`
	NaturalKey []string `yaml:"naturalKey"`
	// AllowPolymorph opts a body-level tagged union in (none in slice 1).
	AllowPolymorph bool   `yaml:"allowPolymorph,omitempty"`
	Source         string `yaml:"source"`
}

// Twin is a kind an official resource already serves.
type Twin struct {
	Ref      `yaml:",inline"`
	Official []string `yaml:"official"`
	Source   string   `yaml:"source"`
}

// ItemGet overrides whether the item path answers GET.
type ItemGet struct {
	Ref     `yaml:",inline"`
	ItemGet bool   `yaml:"itemGet"`
	Source  string `yaml:"source"`
}

// FieldList names fields of one kind.
type FieldList struct {
	Ref    `yaml:",inline"`
	Fields []string `yaml:"fields"`
	Source string   `yaml:"source"`
}

// Rule is a cross-field check run in Check.
type Rule struct {
	Ref     `yaml:",inline"`
	Fields  []string `yaml:"fields"`
	Message string   `yaml:"message"`
	Source  string   `yaml:"source"`
}

// Default is the value a dropped field resets to (G-Q1).
type Default struct {
	Ref    `yaml:",inline"`
	Field  string    `yaml:"field"`
	Value  yaml.Node `yaml:"value"`
	Source string    `yaml:"source"`
}

// Rename gives a field another Pulumi property name.
type Rename struct {
	Ref      `yaml:",inline"`
	Field    string `yaml:"field"`
	Property string `yaml:"property"`
	Source   string `yaml:"source"`
}

// Load parses the embedded kinds.yaml and validates it on its own.
func Load() (*Overrides, error) { return Parse(kindsYAML) }

// Parse parses an overrides file strictly (unknown keys are errors) and
// validates its structure.
func Parse(b []byte) (*Overrides, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var o Overrides
	if err := dec.Decode(&o); err != nil {
		return nil, fmt.Errorf("kinds.yaml: %w", err)
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return &o, nil
}

var (
	reToken = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	rePath  = regexp.MustCompile(`^[A-Za-z0-9_]+(\[\])*(\.[A-Za-z0-9_]+(\[\])*)*$`)
)

// Validate checks the file's own structure (no model needed).
func (o *Overrides) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if o.Version != 1 {
		add("version: want 1, got %d", o.Version)
	}
	checkRef := func(sec string, i int, r Ref, source string) {
		if r.Kind == "" {
			add("%s[%d]: kind is empty", sec, i)
		}
		if r.API != "" && r.API != kind.APIV2 && r.API != kind.APIClassic {
			add("%s[%d] %s: api must be v2 or classic", sec, i, r.Kind)
		}
		if strings.TrimSpace(source) == "" {
			add("%s[%d] %s: source is empty (every override carries its evidence)", sec, i, r)
		}
	}
	checkPaths := func(sec string, i int, r Ref, paths ...string) {
		if len(paths) == 0 {
			add("%s[%d] %s: no fields", sec, i, r)
		}
		for _, p := range paths {
			if !rePath.MatchString(p) {
				add("%s[%d] %s: bad field path %q", sec, i, r, p)
			}
		}
	}
	tokens, exposed := map[string]string{}, map[string]bool{}
	for i, e := range o.Expose {
		checkRef("expose", i, e.Ref, e.Source)
		if !reToken.MatchString(e.Token) {
			add("expose[%d] %s: token %q is not PascalCase", i, e.Ref, e.Token)
		}
		if prev, dup := tokens[e.Token]; dup {
			add("expose[%d] %s: token %s already used by %s", i, e.Ref, e.Token, prev)
		}
		tokens[e.Token] = e.Ref.String()
		if exposed[e.Ref.String()] {
			add("expose[%d] %s: exposed twice", i, e.Ref)
		}
		exposed[e.Ref.String()] = true
		checkPaths("expose", i, e.Ref, e.NaturalKey...)
	}
	twins := map[string]bool{}
	for i, t := range o.OfficialTwins {
		checkRef("officialTwins", i, t.Ref, t.Source)
		if len(t.Official) == 0 {
			add("officialTwins[%d] %s: names no official token", i, t.Ref)
		}
		if twins[t.Ref.String()] {
			add("officialTwins[%d] %s: listed twice", i, t.Ref)
		}
		twins[t.Ref.String()] = true
		if exposed[t.Ref.String()] {
			add("officialTwins[%d] %s: is exposed, but an official resource already serves it (whole kinds, G-Q4)", i, t.Ref)
		}
	}
	seen := map[string]bool{}
	for i, g := range o.ItemGet {
		checkRef("itemGet", i, g.Ref, g.Source)
		if seen[g.Ref.String()] {
			add("itemGet[%d] %s: listed twice", i, g.Ref)
		}
		seen[g.Ref.String()] = true
	}
	for sec, ls := range map[string][]FieldList{"implicitRequired": o.ImplicitRequired, "createOnly": o.CreateOnly,
		"notReturned": o.NotReturned, "notSecret": o.NotSecret} {
		for i, l := range ls {
			checkRef(sec, i, l.Ref, l.Source)
			checkPaths(sec, i, l.Ref, l.Fields...)
		}
	}
	for i, r := range o.Rules {
		checkRef("rules", i, r.Ref, r.Source)
		checkPaths("rules", i, r.Ref, r.Fields...)
		if r.Message == "" {
			add("rules[%d] %s: message is empty", i, r.Ref)
		}
	}
	for i, d := range o.Defaults {
		checkRef("defaults", i, d.Ref, d.Source)
		checkPaths("defaults", i, d.Ref, d.Field)
		if d.Value.Kind == 0 {
			add("defaults[%d] %s: value is missing", i, d.Ref)
		}
	}
	for i, r := range o.Rename {
		checkRef("rename", i, r.Ref, r.Source)
		checkPaths("rename", i, r.Ref, r.Field)
		if r.Property == "" {
			add("rename[%d] %s: property is empty", i, r.Ref)
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return errors.New("kinds.yaml:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

// Resolve checks every name in the overrides against the model: each kind
// must exist and each field path must resolve. Any miss is an error (loud on
// purpose: a version bump that renames something must not pass silently).
func (o *Overrides) Resolve(m *kind.Model) error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	find := func(sec string, r Ref) *kind.Kind {
		k := m.Find(r.APIName(), r.Kind)
		if k == nil {
			add("%s: kind %s is not in the model (Network %s)", sec, r, m.Source.NetworkVersion)
		}
		return k
	}
	paths := func(sec string, r Ref, fs ...string) {
		k := find(sec, r)
		if k == nil {
			return
		}
		for _, f := range fs {
			if FieldAt(k, f) == nil {
				add("%s: field %s.%s is not in the model (Network %s)", sec, r, f, m.Source.NetworkVersion)
			}
		}
	}
	for _, e := range o.Expose {
		paths("expose", e.Ref, e.NaturalKey...)
		if k := m.Find(e.APIName(), e.Kind); k != nil && k.Polymorph != nil && !e.AllowPolymorph {
			add("expose: kind %s has a polymorphic body; opt in with allowPolymorph", e.Ref)
		}
	}
	for _, t := range o.OfficialTwins {
		find("officialTwins", t.Ref)
	}
	for _, g := range o.ItemGet {
		find("itemGet", g.Ref)
	}
	for sec, ls := range map[string][]FieldList{"implicitRequired": o.ImplicitRequired, "createOnly": o.CreateOnly,
		"notReturned": o.NotReturned, "notSecret": o.NotSecret} {
		for _, l := range ls {
			paths(sec, l.Ref, l.Fields...)
		}
	}
	for _, r := range o.Rules {
		paths("rules", r.Ref, r.Fields...)
	}
	for _, d := range o.Defaults {
		paths("defaults", d.Ref, d.Field)
	}
	for _, r := range o.Rename {
		paths("rename", r.Ref, r.Field)
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return errors.New("kinds.yaml does not match the model:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

// FieldAt resolves a dotted field path ("schedule.mode", "monitors[].name")
// in a kind, looking through polymorphic variants. It returns nil when the
// path does not resolve.
func FieldAt(k *kind.Kind, path string) *kind.Field {
	segs := strings.Split(path, ".")
	return lookup(k.Fields, k.Polymorph, segs)
}

func lookup(fs map[string]*kind.Field, poly *kind.Polymorph, segs []string) *kind.Field {
	name := strings.TrimRight(segs[0], "[]")
	depth := (len(segs[0]) - len(name)) / 2
	cands := []*kind.Field{fs[name]}
	if poly != nil {
		vs := make([]string, 0, len(poly.Variants))
		for v := range poly.Variants {
			vs = append(vs, v)
		}
		sort.Strings(vs)
		for _, v := range vs {
			cands = append(cands, poly.Variants[v][name])
		}
	}
	for _, f := range cands {
		for i := 0; f != nil && i < depth; i++ {
			f = f.Items
		}
		if f == nil {
			continue
		}
		if len(segs) == 1 {
			return f
		}
		if r := lookup(f.Fields, f.Polymorph, segs[1:]); r != nil {
			return r
		}
	}
	return nil
}
