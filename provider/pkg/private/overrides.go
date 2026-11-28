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
	ListPath         []ListPath  `yaml:"listPath"`
	ImplicitRequired []FieldList `yaml:"implicitRequired"`
	// CreateDefaults are values sent on create only, when the program does
	// not set them, and never managed afterwards (private-binding.md §S2.7).
	CreateDefaults []CreateDefault `yaml:"createDefaults"`
	// OutputOnly fields are owned by the server: outputs, never inputs, and
	// kept at their live value in a PUT body (not stripped like read-only).
	OutputOnly  []FieldList `yaml:"outputOnly"`
	Rules       []Rule      `yaml:"rules"`
	Defaults    []Default   `yaml:"defaults"`
	CreateOnly  []FieldList `yaml:"createOnly"`
	NotReturned []FieldList `yaml:"notReturned"`
	NotSecret   []FieldList `yaml:"notSecret"`
	Rename      []Rename    `yaml:"rename"`
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
	AllowPolymorph bool `yaml:"allowPolymorph,omitempty"`
	// Shape is collection (the default) or singleton: one document per site
	// that create adopts and delete releases (private-binding.md §S2.8).
	Shape string `yaml:"shape,omitempty"`
	// Verbs states the verbs a classic kind answers ("CRUD", "-RU-"), which the
	// model leaves unknown for classic kinds; it needs measured evidence.
	Verbs string `yaml:"verbs,omitempty"`
	// Variant pins one value of a discriminator field: the G-Q4 variant
	// exception (private-binding.md §S2.7). Only it lets an official twin be
	// exposed, and only for a value no official token serves.
	Variant *VariantPin `yaml:"variant,omitempty"`
	// ServedFrom is the first Network version that accepts the kind; the kind
	// is generated only when the pin's Network version is at least this.
	// Empty: built and tested, not served.
	ServedFrom string `yaml:"servedFrom,omitempty"`
	Source     string `yaml:"source"`
}

// Shapes of an exposed kind.
const (
	ShapeCollection = "collection"
	ShapeSingleton  = "singleton"
)

// ShapeName is the shape with the default applied.
func (e Expose) ShapeName() string {
	if e.Shape == "" {
		return ShapeCollection
	}
	return e.Shape
}

// VariantPin is one discriminator value.
type VariantPin struct {
	Field string `yaml:"field"`
	Value string `yaml:"value"`
}

// VariantSet is the discriminator values the official API serves.
type VariantSet struct {
	Field  string   `yaml:"field"`
	Values []string `yaml:"values"`
}

// CreateDefault is a set of values sent on create only.
type CreateDefault struct {
	Ref    `yaml:",inline"`
	Fields map[string]yaml.Node `yaml:"fields"`
	Source string               `yaml:"source"`
}

// Twin is a kind an official resource already serves.
type Twin struct {
	Ref      `yaml:",inline"`
	Official []string `yaml:"official"`
	// OfficialVariants lists the discriminator values the official tokens
	// serve, when the kind is discriminated; required to expose a variant.
	OfficialVariants *VariantSet `yaml:"officialVariants,omitempty"`
	Source           string      `yaml:"source"`
}

// ItemGet overrides whether the item path answers GET.
type ItemGet struct {
	Ref     `yaml:",inline"`
	ItemGet bool   `yaml:"itemGet"`
	Source  string `yaml:"source"`
}

// ListPath names the collection GET of a kind whose list lives elsewhere
// than its create path (relative to the API's site base).
type ListPath struct {
	Ref    `yaml:",inline"`
	Path   string `yaml:"path"`
	Source string `yaml:"source"`
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
	reToken   = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	reVerbs   = regexp.MustCompile(`^[C-][R-][U-][D-]$`)
	reVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
	reRoute   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(/[a-z0-9][a-z0-9-]*)*$`)
	rePath    = regexp.MustCompile(`^[A-Za-z0-9_]+(\[\])*(\.[A-Za-z0-9_]+(\[\])*)*$`)
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
		switch e.ShapeName() {
		case ShapeCollection:
			checkPaths("expose", i, e.Ref, e.NaturalKey...)
		case ShapeSingleton:
			if len(e.NaturalKey) != 0 {
				add("expose[%d] %s: a singleton has no natural key (it is keyed by its site)", i, e.Ref)
			}
			if e.Variant != nil {
				add("expose[%d] %s: a singleton cannot pin a variant", i, e.Ref)
			}
		default:
			add("expose[%d] %s: shape %q: want collection or singleton", i, e.Ref, e.Shape)
		}
		if e.Verbs != "" && !reVerbs.MatchString(e.Verbs) {
			add("expose[%d] %s: verbs %q: want CRUD with '-' for a missing verb", i, e.Ref, e.Verbs)
		}
		if e.Variant != nil && (e.Variant.Field == "" || e.Variant.Value == "") {
			add("expose[%d] %s: variant needs field and value", i, e.Ref)
		}
		if e.ServedFrom != "" && !reVersion.MatchString(e.ServedFrom) {
			add("expose[%d] %s: servedFrom %q is not a version", i, e.Ref, e.ServedFrom)
		}
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
		if t.OfficialVariants != nil && (t.OfficialVariants.Field == "" || len(t.OfficialVariants.Values) == 0) {
			add("officialTwins[%d] %s: officialVariants needs field and values", i, t.Ref)
		}
		if !exposed[t.Ref.String()] {
			continue
		}
		// The G-Q4 variant exception: an exposed twin must pin a variant no
		// official token serves.
		for _, e := range o.Expose {
			if e.Ref.String() != t.Ref.String() {
				continue
			}
			switch {
			case e.Variant == nil:
				add("officialTwins[%d] %s: is exposed, but an official resource already serves it (whole kinds, G-Q4; "+
					"only a variant the official API does not serve may be exposed, with variant:)", i, t.Ref)
			case t.OfficialVariants == nil:
				add("officialTwins[%d] %s: exposes variant %s=%s but lists no officialVariants", i, t.Ref, e.Variant.Field, e.Variant.Value)
			case t.OfficialVariants.Field != e.Variant.Field:
				add("officialTwins[%d] %s: officialVariants field %s differs from the exposed variant field %s", i, t.Ref, t.OfficialVariants.Field, e.Variant.Field)
			default:
				for _, v := range t.OfficialVariants.Values {
					if v == e.Variant.Value {
						add("officialTwins[%d] %s: variant %s=%s is served by the official API; it moves there (G-Q4 exception ends)", i, t.Ref, e.Variant.Field, v)
					}
				}
			}
		}
	}
	for i, e := range o.Expose {
		if e.Variant != nil && !twins[e.Ref.String()] {
			add("expose[%d] %s: variant: is only for an official twin (the G-Q4 variant exception)", i, e.Ref)
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
	seenList := map[string]bool{}
	for i, l := range o.ListPath {
		checkRef("listPath", i, l.Ref, l.Source)
		if !reRoute.MatchString(l.Path) {
			add("listPath[%d] %s: bad path %q", i, l.Ref, l.Path)
		}
		if seenList[l.Ref.String()] {
			add("listPath[%d] %s: listed twice", i, l.Ref)
		}
		seenList[l.Ref.String()] = true
	}
	for sec, ls := range map[string][]FieldList{"implicitRequired": o.ImplicitRequired, "createOnly": o.CreateOnly,
		"notReturned": o.NotReturned, "notSecret": o.NotSecret, "outputOnly": o.OutputOnly} {
		for i, l := range ls {
			checkRef(sec, i, l.Ref, l.Source)
			checkPaths(sec, i, l.Ref, l.Fields...)
		}
	}
	for i, c := range o.CreateDefaults {
		checkRef("createDefaults", i, c.Ref, c.Source)
		names := make([]string, 0, len(c.Fields))
		for f, v := range c.Fields {
			names = append(names, f)
			if v.Kind == 0 || v.Tag == "!!null" {
				add("createDefaults[%d] %s: field %s has no value", i, c.Ref, f)
			}
		}
		sort.Strings(names)
		checkPaths("createDefaults", i, c.Ref, names...)
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
		k := m.Find(e.APIName(), e.Kind)
		if k != nil && k.Polymorph != nil && !e.AllowPolymorph {
			add("expose: kind %s has a polymorphic body; opt in with allowPolymorph", e.Ref)
		}
		if k != nil && e.Variant != nil {
			f := FieldAt(k, e.Variant.Field)
			switch {
			case f == nil:
				add("expose: variant field %s.%s is not in the model (Network %s)", e.Ref, e.Variant.Field, m.Source.NetworkVersion)
			case f.Type != kind.TypeEnum || !containsStr(f.Enum, e.Variant.Value):
				add("expose: variant %s.%s=%s is not a value of the model's enum %v (Network %s)", e.Ref, e.Variant.Field, e.Variant.Value, f.Enum, m.Source.NetworkVersion)
			}
		}
	}
	for _, t := range o.OfficialTwins {
		if t.OfficialVariants == nil {
			continue
		}
		if k := m.Find(t.APIName(), t.Kind); k != nil {
			f := FieldAt(k, t.OfficialVariants.Field)
			if f == nil {
				add("officialTwins: variant field %s.%s is not in the model (Network %s)", t.Ref, t.OfficialVariants.Field, m.Source.NetworkVersion)
				continue
			}
			for _, v := range t.OfficialVariants.Values {
				if !containsStr(f.Enum, v) {
					add("officialTwins: %s.%s=%s is not a value of the model's enum %v", t.Ref, t.OfficialVariants.Field, v, f.Enum)
				}
			}
		}
	}
	for _, c := range o.CreateDefaults {
		names := make([]string, 0, len(c.Fields))
		for f := range c.Fields {
			names = append(names, f)
		}
		sort.Strings(names)
		paths("createDefaults", c.Ref, names...)
	}
	for _, t := range o.OfficialTwins {
		find("officialTwins", t.Ref)
	}
	for _, g := range o.ItemGet {
		find("itemGet", g.Ref)
	}
	for _, l := range o.ListPath {
		find("listPath", l.Ref)
	}
	for sec, ls := range map[string][]FieldList{"implicitRequired": o.ImplicitRequired, "createOnly": o.CreateOnly,
		"notReturned": o.NotReturned, "notSecret": o.NotSecret, "outputOnly": o.OutputOnly} {
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

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
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
