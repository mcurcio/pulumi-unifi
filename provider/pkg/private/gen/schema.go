package gen

import (
	"fmt"
	"sort"
	"strings"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
)

// resource builds the Pulumi resource spec of one kind.
func (g *generator) resource(e private.Expose, k *engine.Kind) (pschema.ResourceSpec, error) {
	keys := make([]string, 0, len(k.NaturalKey))
	for _, p := range k.NaturalKey {
		keys = append(keys, "`"+propPath(k, p)+"`")
	}
	desc := fmt.Sprintf("A `%s` object of the UniFi Network application, managed through its private API.\n\n"+
		"Import it by its natural key (%s) or by its stored id (`private:<id>`). "+
		"A managed property removed from the program is reset to its default on the next update.", k.Collection, strings.Join(keys, ", "))
	res := pschema.ResourceSpec{
		ObjectTypeSpec:  pschema.ObjectTypeSpec{Type: "object", Description: desc, Properties: map[string]pschema.PropertySpec{}},
		InputProperties: map[string]pschema.PropertySpec{},
	}
	for _, w := range sortedKeys(k.Fields) {
		f := k.Fields[w]
		ps, err := g.prop(e.Token, f, f.Wire)
		if err != nil {
			return res, err
		}
		res.Properties[f.Property] = ps
		if f.ReadOnly {
			continue
		}
		in := ps
		if f.CreateOnly {
			in.ReplaceOnChanges = true
		}
		res.InputProperties[f.Property] = in
		if f.Required {
			res.RequiredInputs = append(res.RequiredInputs, f.Property)
		}
	}
	sort.Strings(res.RequiredInputs)
	return res, nil
}

func propPath(k *engine.Kind, wirePath string) string {
	fs := k.Fields
	var out []string
	for _, s := range strings.Split(wirePath, ".") {
		f := fs[s]
		if f == nil {
			return wirePath
		}
		out = append(out, f.Property)
		fs = f.Fields
	}
	return strings.Join(out, ".")
}

// prop builds one property spec; object fields become named types
// unifi:network:<Token><PathPascal>.
func (g *generator) prop(tok string, f *engine.Field, path string) (pschema.PropertySpec, error) {
	ts, err := g.typeSpec(tok, f, path)
	if err != nil {
		return pschema.PropertySpec{}, err
	}
	ps := pschema.PropertySpec{TypeSpec: ts, Description: describe(f), Secret: f.Secret}
	return ps, nil
}

func (g *generator) typeSpec(tok string, f *engine.Field, path string) (pschema.TypeSpec, error) {
	switch f.Type {
	case kind.TypeString, kind.TypeUUID, kind.TypeEnum, kind.TypeDate, kind.TypeDateTime:
		return pschema.TypeSpec{Type: "string"}, nil
	case kind.TypeInt:
		return pschema.TypeSpec{Type: "integer"}, nil
	case kind.TypeNumber:
		return pschema.TypeSpec{Type: "number"}, nil
	case kind.TypeBool:
		return pschema.TypeSpec{Type: "boolean"}, nil
	case kind.TypeArray:
		it := f.Items
		if it == nil {
			it = &engine.Field{Type: kind.TypeAny}
		}
		its, err := g.typeSpec(tok, it, path)
		if err != nil {
			return pschema.TypeSpec{}, err
		}
		return pschema.TypeSpec{Type: "array", Items: &its}, nil
	case kind.TypeMap:
		vf := f.Values
		if vf == nil {
			vf = &engine.Field{Type: kind.TypeAny}
		}
		vs, err := g.typeSpec(tok, vf, path)
		if err != nil {
			return pschema.TypeSpec{}, err
		}
		return pschema.TypeSpec{Type: "object", AdditionalProperties: &vs}, nil
	case kind.TypeObject:
		name := engine.TokenPrefix + tok + typeSuffix(path)
		if _, done := g.types[name]; done {
			return pschema.TypeSpec{}, fmt.Errorf("type %s is generated twice (rename a field)", name)
		}
		ot := pschema.ObjectTypeSpec{Type: "object", Properties: map[string]pschema.PropertySpec{}}
		for _, w := range sortedKeys(f.Fields) {
			sub := f.Fields[w]
			ps, err := g.prop(tok, sub, path+"."+w)
			if err != nil {
				return pschema.TypeSpec{}, err
			}
			ot.Properties[sub.Property] = ps
			if sub.Required {
				ot.Required = append(ot.Required, sub.Property)
			}
		}
		sort.Strings(ot.Required)
		g.types[name] = pschema.ComplexTypeSpec{ObjectTypeSpec: ot}
		return pschema.TypeSpec{Ref: "#/types/" + name}, nil
	default: // any
		return pschema.TypeSpec{Ref: "pulumi.json#/Any"}, nil
	}
}

// typeSuffix is the Pascal form of a wire path ("source.ports" -> SourcePorts).
func typeSuffix(path string) string {
	var b strings.Builder
	for _, s := range strings.Split(path, ".") {
		b.WriteString(pascal(s))
	}
	return b.String()
}

func describe(f *engine.Field) string {
	var parts []string
	if f.Type == kind.TypeEnum && len(f.Enum) > 0 {
		parts = append(parts, "One of "+quoteList(f.Enum)+".")
	}
	if f.Items != nil && f.Items.Type == kind.TypeEnum && len(f.Items.Enum) > 0 {
		parts = append(parts, "Items are one of "+quoteList(f.Items.Enum)+".")
	}
	switch f.Type {
	case kind.TypeDate:
		parts = append(parts, "A date (YYYY-MM-DD).")
	case kind.TypeDateTime:
		parts = append(parts, "A date and time.")
	}
	if len(f.Default) > 0 {
		parts = append(parts, fmt.Sprintf("Removing it resets it to `%s`.", string(f.Default)))
	} else if f.Nullable {
		parts = append(parts, "Removing it clears it.")
	}
	if f.ReadOnly {
		parts = append(parts, "Set by the controller.")
	}
	if f.CreateOnly {
		parts = append(parts, "Changing it replaces the object.")
	}
	return strings.Join(parts, " ")
}

func quoteList(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = "`" + s + "`"
	}
	return strings.Join(q, ", ")
}
