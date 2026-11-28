// Package gen generates the private surface (docs/design/private-binding.md
// §5): the extracted model + kinds.yaml + the official golden schema →
// schema.merged.json (the golden plus the private resources, types and the
// networkBaseUrl config key) and private.json (the engine's kind table).
//
// Without a model (PRIVATE=0, the default and CI) the merged schema is the
// golden byte for byte and private.json holds no kinds. Both outputs are
// gitignored build artifacts.
package gen

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	core "github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
	officialgen "github.com/mcurcio/pulumi-unifi/provider/pkg/gen"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
)

// Package-level names of the private fragment.
const (
	// ConfigBaseURL is the private fragment's one config key (design §7).
	ConfigBaseURL = "networkBaseUrl"
	// EnvBaseURL is its environment fallback.
	EnvBaseURL = "UNIFI_NETWORK_BASE_URL"
	module     = "network"
)

// reserved property names (Pulumi) that a field may not take unrenamed.
var reserved = map[string]bool{"id": true, "urn": true}

// Output is the generator's result.
type Output struct {
	Schema  []byte // schema.merged.json
	Surface []byte // private.json
	// Tokens lists the private tokens ("resource:unifi:network:QosRule").
	Tokens []string
}

// EmptySurface is private.json without a model: no private kinds, only the
// engine-served official singletons (mappings.yaml `singletons:`).
func EmptySurface() []byte {
	b, err := marshalSurface(&engine.Surface{Kinds: []*engine.Kind{}, Singletons: singletons()})
	if err != nil {
		panic(err) // static data: a failure is a build defect
	}
	return b
}

// singletons lists the official singletons the engine serves, sorted by token.
func singletons() []*core.Singleton {
	var out []*core.Singleton
	for _, sg := range officialgen.Singletons() {
		out = append(out, &core.Singleton{Token: sg.Token, API: "official", Path: sg.Path, Property: sg.Property, Merge: sg.Merge})
	}
	return out
}

func marshalSurface(s *engine.Surface) ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return kind.CanonicalJSON(raw)
}

// Generate builds the outputs. A nil model returns the golden verbatim.
func Generate(m *kind.Model, o *private.Overrides, golden []byte) (*Output, error) {
	if m == nil {
		return &Output{Schema: append([]byte(nil), golden...), Surface: EmptySurface()}, nil
	}
	if err := o.Resolve(m); err != nil {
		return nil, err
	}
	var spec pschema.PackageSpec
	if err := json.Unmarshal(golden, &spec); err != nil {
		return nil, fmt.Errorf("parsing the golden schema: %w", err)
	}
	g := &generator{m: m, o: o, spec: &spec, types: map[string]pschema.ComplexTypeSpec{}}
	surf := &engine.Surface{Source: engine.SurfaceSource{UOSVersion: m.Source.UOSVersion, NetworkVersion: m.Source.NetworkVersion},
		Singletons: singletons()}
	exp := append([]private.Expose(nil), o.Expose...)
	sort.Slice(exp, func(i, j int) bool { return exp[i].Token < exp[j].Token })
	var errs []string
	for _, e := range exp {
		k, err := g.kind(e)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		surf.Kinds = append(surf.Kinds, k)
	}
	if len(errs) > 0 {
		return nil, errors.New("private gen:\n  " + strings.Join(errs, "\n  "))
	}
	for name, t := range g.types {
		if _, clash := spec.Types[name]; clash {
			return nil, fmt.Errorf("private gen: type %s already exists in the official schema", name)
		}
		if spec.Types == nil {
			spec.Types = map[string]pschema.ComplexTypeSpec{}
		}
		spec.Types[name] = t
	}
	addConfig(&spec)
	sb, err := officialgen.MarshalSchemaJSON(spec)
	if err != nil {
		return nil, err
	}
	pb, err := marshalSurface(surf)
	if err != nil {
		return nil, err
	}
	out := &Output{Schema: sb, Surface: pb}
	for _, k := range surf.Kinds {
		out.Tokens = append(out.Tokens, "resource:"+k.Token)
	}
	sort.Strings(out.Tokens)
	return out, nil
}

func addConfig(spec *pschema.PackageSpec) {
	desc := "Base URL of the UniFi Network application for the resources in the `network` module (served by its private API). " +
		"Defaults to `https://<apiHost>/proxy/network` (the UniFi OS edge). To reach the Network application directly, " +
		"use e.g. `http://127.0.0.1:8081`."
	if spec.Config.Variables == nil {
		spec.Config.Variables = map[string]pschema.PropertySpec{}
	}
	spec.Config.Variables[ConfigBaseURL] = pschema.PropertySpec{TypeSpec: pschema.TypeSpec{Type: "string"}, Description: desc}
	if spec.Provider.InputProperties == nil {
		spec.Provider.InputProperties = map[string]pschema.PropertySpec{}
	}
	spec.Provider.InputProperties[ConfigBaseURL] = pschema.PropertySpec{
		TypeSpec: pschema.TypeSpec{Type: "string"}, Description: desc,
		DefaultInfo: &pschema.DefaultSpec{Environment: []string{EnvBaseURL}},
	}
}

type generator struct {
	m     *kind.Model
	o     *private.Overrides
	spec  *pschema.PackageSpec
	types map[string]pschema.ComplexTypeSpec
}

type facts struct {
	createOnly, notReturned, notSecret, implicitRequired map[string]bool
	defaults                                             map[string]json.RawMessage
	rename                                               map[string]string
}

func (g *generator) facts(r private.Ref) (*facts, error) {
	f := &facts{createOnly: map[string]bool{}, notReturned: map[string]bool{}, notSecret: map[string]bool{},
		implicitRequired: map[string]bool{}, defaults: map[string]json.RawMessage{}, rename: map[string]string{}}
	for dst, ls := range map[*map[string]bool][]private.FieldList{&f.createOnly: g.o.CreateOnly, &f.notReturned: g.o.NotReturned,
		&f.notSecret: g.o.NotSecret, &f.implicitRequired: g.o.ImplicitRequired} {
		for _, l := range ls {
			if l.Ref.String() == r.String() {
				for _, p := range l.Fields {
					(*dst)[p] = true
				}
			}
		}
	}
	for _, d := range g.o.Defaults {
		if d.Ref.String() != r.String() {
			continue
		}
		var v any
		if err := d.Value.Decode(&v); err != nil {
			return nil, fmt.Errorf("defaults %s.%s: %w", r, d.Field, err)
		}
		b, err := json.Marshal(normalizeYAML(v))
		if err != nil {
			return nil, fmt.Errorf("defaults %s.%s: %w", r, d.Field, err)
		}
		f.defaults[d.Field] = b
	}
	for _, rn := range g.o.Rename {
		if rn.Ref.String() == r.String() {
			f.rename[rn.Field] = rn.Property
		}
	}
	return f, nil
}

func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range t {
			out[k] = normalizeYAML(e)
		}
		return out
	case []any:
		for i := range t {
			t[i] = normalizeYAML(t[i])
		}
		return t
	default:
		return v
	}
}

func (g *generator) kind(e private.Expose) (*engine.Kind, error) {
	mk := g.m.Find(e.APIName(), e.Kind)
	if mk == nil {
		return nil, fmt.Errorf("%s: not in the model", e.Ref)
	}
	if mk.API != kind.APIV2 {
		return nil, fmt.Errorf("%s: only v2 kinds are served in slice 1", e.Ref)
	}
	if mk.Polymorph != nil && !e.AllowPolymorph {
		return nil, fmt.Errorf("%s: polymorphic body (opt in with allowPolymorph)", e.Ref)
	}
	for _, v := range "CUD" {
		if !strings.ContainsRune(mk.Verbs, v) {
			return nil, fmt.Errorf("%s: verbs %q lack %c; a resource needs create, update and delete", e.Ref, mk.Verbs, v)
		}
	}
	for _, t := range g.o.OfficialTwins {
		if t.Ref.String() == e.Ref.String() {
			return nil, fmt.Errorf("%s: an official resource serves it (%s); whole kinds (G-Q4)", e.Ref, strings.Join(t.Official, ", "))
		}
	}
	token := engine.TokenPrefix + e.Token
	if _, clash := g.spec.Resources[token]; clash {
		return nil, fmt.Errorf("%s: token %s already exists in the official schema", e.Ref, token)
	}
	ff, err := g.facts(e.Ref)
	if err != nil {
		return nil, err
	}
	k := &engine.Kind{
		Token: token, Kind: mk.Name, API: mk.API,
		Collection: mk.Collection, ListPath: mk.Collection, ItemPath: mk.ItemPath, CreatePath: mk.CreatePath,
		ItemGet: mk.ItemGet, ListGet: mk.ListGet, CreateList: mk.CreateList,
		IDField: mk.IDField, Envelope: mk.Envelope, NaturalKey: append([]string(nil), e.NaturalKey...),
	}
	if k.CreatePath == "" {
		k.CreatePath = k.Collection
	}
	for _, ig := range g.o.ItemGet {
		if ig.Ref.String() == e.Ref.String() {
			k.ItemGet = ig.ItemGet
		}
	}
	for _, lp := range g.o.ListPath {
		if lp.Ref.String() == e.Ref.String() {
			k.ListPath, k.ListGet = lp.Path, true
		}
	}
	if !k.ItemGet && !k.ListGet {
		return nil, fmt.Errorf("%s: neither the item nor the collection answers GET; it cannot be read", e.Ref)
	}
	if k.ItemPath == "" || k.IDField == "" {
		return nil, fmt.Errorf("%s: no item path or id field", e.Ref)
	}
	nk := map[string]bool{}
	for _, p := range e.NaturalKey {
		nk[p] = true
	}
	fields, err := g.fields(e.Token, mk.Fields, "", ff, nk, true, mk.IDField)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.Ref, err)
	}
	k.Fields = fields
	for _, p := range e.NaturalKey {
		f := engine.FieldAt(k, strings.Split(p, "."))
		if f == nil || f.ReadOnly || f.Secret {
			return nil, fmt.Errorf("%s: natural key %s is missing, read-only or secret", e.Ref, p)
		}
	}
	res, err := g.resource(e, k)
	if err != nil {
		return nil, err
	}
	g.spec.Resources[token] = res
	return k, nil
}

// fields converts a model field set (plus nested polymorphic variants,
// flattened) into engine fields.
func (g *generator) fields(tok string, fs map[string]*kind.Field, pre string, ff *facts, nk map[string]bool, top bool, idField string) (map[string]*engine.Field, error) {
	out := map[string]*engine.Field{}
	props := map[string]string{}
	for _, w := range sortedKeys(fs) {
		if top && w == idField {
			continue // the id is the resource id, not a property
		}
		mf := fs[w]
		path := pre + w
		if !top && mf.ReadOnly {
			continue // nested read-only fields are left out of the shared types
		}
		f, err := g.field(tok, mf, w, path, ff, nk)
		if err != nil {
			return nil, err
		}
		if prev, dup := props[f.Property]; dup {
			return nil, fmt.Errorf("fields %s and %s both map to property %q (add a rename)", prev, path, f.Property)
		}
		props[f.Property] = path
		out[w] = f
	}
	return out, nil
}

var reProp = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

func (g *generator) field(tok string, mf *kind.Field, wire, path string, ff *facts, nk map[string]bool) (*engine.Field, error) {
	prop := lowerCamel(wire)
	if r, ok := ff.rename[path]; ok {
		prop = r
	}
	if reserved[prop] {
		return nil, fmt.Errorf("field %s maps to the reserved property %q: add a kinds.yaml rename", path, prop)
	}
	if !reProp.MatchString(prop) {
		return nil, fmt.Errorf("field %s maps to property %q, which is not lowerCamel: add a kinds.yaml rename", path, prop)
	}
	f := &engine.Field{
		Wire: wire, Property: prop, Type: mf.Type, Enum: mf.Enum, Pattern: mf.Pattern,
		ReadOnly: mf.ReadOnly, Secret: (mf.Secret || mf.WriteOnly) && !ff.notSecret[path],
		CreateOnly: ff.createOnly[path], NotReturned: ff.notReturned[path] || mf.WriteOnly,
		Nullable: mf.Optional,
	}
	f.Default = mf.Default
	if d, ok := ff.defaults[path]; ok {
		f.Default = d
	}
	f.Required = (mf.Required && len(f.Default) == 0 || ff.implicitRequired[path] || nk[path]) && !mf.ReadOnly
	f.Constraints = constraints(mf)
	switch mf.Type {
	case kind.TypeObject:
		sub := mf.Fields
		if mf.Polymorph != nil {
			merged, err := flatten(mf)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", path, err)
			}
			sub = merged
		}
		fs, err := g.fields(tok, sub, path+".", ff, nk, false, "")
		if err != nil {
			return nil, err
		}
		f.Fields = fs
	case kind.TypeArray:
		if mf.Items == nil {
			f.Items = &engine.Field{Type: kind.TypeAny}
			break
		}
		it, err := g.field(tok, mf.Items, wire, path+"[]", ff, nk)
		if err != nil {
			return nil, err
		}
		it.Wire, it.Property, it.Required, it.Default, it.Nullable = "", "", false, nil, false
		f.Items = it
	case kind.TypeMap:
		if mf.Values != nil {
			vf, err := g.field(tok, mf.Values, wire, path+"{}", ff, nk)
			if err != nil {
				return nil, err
			}
			vf.Wire, vf.Property, vf.Required, vf.Default, vf.Nullable = "", "", false, nil, false
			f.Values = vf
		}
	}
	return f, nil
}

// flatten merges an object field's base fields with every variant's fields
// (a nested tagged union, e.g. a rule's `destination`). Variant fields are
// optional; a field two variants define differently is an error.
func flatten(mf *kind.Field) (map[string]*kind.Field, error) {
	out := map[string]*kind.Field{}
	for n, f := range mf.Fields {
		out[n] = f
	}
	vs := sortedKeys(mf.Polymorph.Variants)
	owner := map[string]string{}
	for _, v := range vs {
		for _, n := range sortedKeys(mf.Polymorph.Variants[v]) {
			f := *mf.Polymorph.Variants[v][n]
			f.Required = false // only some variants carry it
			if prev, ok := out[n]; ok {
				a, _ := json.Marshal(stripReq(prev))
				b, _ := json.Marshal(stripReq(&f))
				if string(a) != string(b) {
					return nil, fmt.Errorf("variants %s and %s define %s differently", owner[n], v, n)
				}
				continue
			}
			out[n] = &f
			owner[n] = v
		}
	}
	return out, nil
}

func stripReq(f *kind.Field) *kind.Field {
	c := *f
	c.Required = false
	return &c
}

func constraints(mf *kind.Field) *engine.Constraints {
	c := mf.Constraints
	if c == nil {
		return nil
	}
	out := &engine.Constraints{Min: c.Min, Max: c.Max, NonEmpty: c.NonEmpty}
	pick := func(r *kind.Range) {
		if r == nil {
			return
		}
		if mf.Type == kind.TypeArray {
			out.MinItems, out.MaxItems = r.Min, r.Max
		} else {
			out.MinLen, out.MaxLen = r.Min, r.Max
		}
	}
	pick(c.Size)
	if c.Len != nil {
		out.MinLen, out.MaxLen = c.Len.Min, c.Len.Max
	}
	if *out == (engine.Constraints{}) {
		return nil
	}
	return out
}

// lowerCamel turns a wire name into a property name: download_limit_kbps ->
// downloadLimitKbps.
func lowerCamel(w string) string {
	parts := strings.FieldsFunc(w, func(r rune) bool { return r == '_' || r == '-' || r == '.' || r == ' ' })
	var b strings.Builder
	for i, p := range parts {
		if i == 0 {
			b.WriteString(strings.ToLower(p[:1]) + p[1:])
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

func pascal(s string) string {
	c := lowerCamel(s)
	if c == "" {
		return c
	}
	r := []rune(c)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
