package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// Unknown stands for a value Pulumi does not know yet (a preview's computed
// input). It never reaches the wire.
type Unknown struct{}

// Path is a field path of wire names. Objects are walked into; lists, maps
// and scalars are leaves (design §8: "lists as leaves; objects merge field by
// field").
type Path []string

func (p Path) String() string { return strings.Join(p, ".") }

// root wraps a kind's fields as one object field.
func root(k *Kind) *Field { return &Field{Type: "object", Fields: k.Fields} }

// ToWire converts Pulumi inputs (property names) into a wire object (wire
// names). Unknown values become Unknown{}. Secrets are unwrapped.
func ToWire(k *Kind, pm resource.PropertyMap) (map[string]any, error) {
	v, err := toWire(root(k), resource.NewObjectProperty(pm), "")
	if err != nil {
		return nil, err
	}
	if v == nil {
		return map[string]any{}, nil
	}
	if _, ok := v.(Unknown); ok {
		return map[string]any{}, nil
	}
	return v.(map[string]any), nil
}

func toWire(f *Field, v resource.PropertyValue, at string) (any, error) {
	for v.IsSecret() {
		v = v.SecretValue().Element
	}
	if v.IsOutput() {
		o := v.OutputValue()
		if !o.Known {
			return Unknown{}, nil
		}
		v = o.Element
		for v.IsSecret() {
			v = v.SecretValue().Element
		}
	}
	if v.IsComputed() {
		return Unknown{}, nil
	}
	if v.IsNull() {
		return nil, nil
	}
	name := at
	if name == "" {
		name = "inputs"
	}
	switch f.Type {
	case "object":
		if !v.IsObject() {
			return nil, fmt.Errorf("%s: want an object, got %s", name, v.TypeString())
		}
		props := byProperty(f.Fields)
		out := map[string]any{}
		for _, key := range v.ObjectValue().StableKeys() {
			sub, ok := props[string(key)]
			if !ok {
				return nil, fmt.Errorf("%s: unknown property %q", name, key)
			}
			w, err := toWire(sub, v.ObjectValue()[key], join(at, string(key)))
			if err != nil {
				return nil, err
			}
			out[sub.Wire] = w
		}
		return out, nil
	case "map":
		if !v.IsObject() {
			return nil, fmt.Errorf("%s: want a map, got %s", name, v.TypeString())
		}
		out := map[string]any{}
		for _, key := range v.ObjectValue().StableKeys() {
			vf := f.Values
			if vf == nil {
				vf = &Field{Type: "any"}
			}
			w, err := toWire(vf, v.ObjectValue()[key], join(at, string(key)))
			if err != nil {
				return nil, err
			}
			out[string(key)] = w
		}
		return out, nil
	case "array":
		if !v.IsArray() {
			return nil, fmt.Errorf("%s: want a list, got %s", name, v.TypeString())
		}
		it := f.Items
		if it == nil {
			it = &Field{Type: "any"}
		}
		out := make([]any, 0, len(v.ArrayValue()))
		for i, e := range v.ArrayValue() {
			w, err := toWire(it, e, fmt.Sprintf("%s[%d]", name, i))
			if err != nil {
				return nil, err
			}
			out = append(out, w)
		}
		return out, nil
	case "int":
		if !v.IsNumber() {
			return nil, fmt.Errorf("%s: want an integer, got %s", name, v.TypeString())
		}
		n := v.NumberValue()
		if n != math.Trunc(n) {
			return nil, fmt.Errorf("%s: want an integer, got %v", name, n)
		}
		return int64(n), nil
	case "number":
		if !v.IsNumber() {
			return nil, fmt.Errorf("%s: want a number, got %s", name, v.TypeString())
		}
		return v.NumberValue(), nil
	case "bool":
		if !v.IsBool() {
			return nil, fmt.Errorf("%s: want a boolean, got %s", name, v.TypeString())
		}
		return v.BoolValue(), nil
	case "string", "uuid", "enum", "date", "datetime":
		if !v.IsString() {
			return nil, fmt.Errorf("%s: want a string, got %s", name, v.TypeString())
		}
		return v.StringValue(), nil
	default: // any
		return plain(v), nil
	}
}

func plain(v resource.PropertyValue) any {
	for v.IsSecret() {
		v = v.SecretValue().Element
	}
	switch {
	case v.IsComputed():
		return Unknown{}
	case v.IsOutput():
		if !v.OutputValue().Known {
			return Unknown{}
		}
		return plain(v.OutputValue().Element)
	case v.IsNull():
		return nil
	case v.IsObject():
		out := map[string]any{}
		for k, e := range v.ObjectValue() {
			out[string(k)] = plain(e)
		}
		return out
	case v.IsArray():
		out := make([]any, 0, len(v.ArrayValue()))
		for _, e := range v.ArrayValue() {
			out = append(out, plain(e))
		}
		return out
	default:
		return v.V
	}
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}

// FromWire converts a wire object into Pulumi properties. Only modelled
// fields are kept; keep decides per field whether it is included.
func FromWire(k *Kind, obj map[string]any, keep func(Path, *Field) bool) resource.PropertyMap {
	return fromWireObject(k.Fields, obj, nil, keep)
}

func fromWireObject(fs map[string]*Field, obj map[string]any, at Path, keep func(Path, *Field) bool) resource.PropertyMap {
	out := resource.PropertyMap{}
	for _, w := range sortedKeys(obj) {
		f, ok := fs[w]
		if !ok {
			continue
		}
		p := append(append(Path{}, at...), w)
		if keep != nil && !keep(p, f) {
			continue
		}
		out[resource.PropertyKey(f.Property)] = fromWire(f, obj[w], p, keep)
	}
	return out
}

func fromWire(f *Field, v any, at Path, keep func(Path, *Field) bool) resource.PropertyValue {
	if v == nil {
		return resource.NewNullProperty()
	}
	switch f.Type {
	case "object":
		if m, ok := v.(map[string]any); ok {
			return resource.NewObjectProperty(fromWireObject(f.Fields, m, at, keep))
		}
	case "array":
		if a, ok := v.([]any); ok {
			it := f.Items
			if it == nil {
				it = &Field{Type: "any"}
			}
			out := make([]resource.PropertyValue, 0, len(a))
			for _, e := range a {
				out = append(out, fromWire(it, e, nil, nil))
			}
			return resource.NewArrayProperty(out)
		}
	case "map":
		if m, ok := v.(map[string]any); ok {
			vf := f.Values
			if vf == nil {
				vf = &Field{Type: "any"}
			}
			out := resource.PropertyMap{}
			for _, key := range sortedKeys(m) {
				out[resource.PropertyKey(key)] = fromWire(vf, m[key], nil, nil)
			}
			return resource.NewObjectProperty(out)
		}
	}
	return resource.NewPropertyValue(Normalize(v))
}

// Normalize turns json.Number and Go integer types into float64 and walks
// lists and maps, so values from the wire and from Pulumi compare equal.
func Normalize(v any) any {
	switch t := v.(type) {
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case int32:
		return float64(t)
	case float32:
		return float64(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = Normalize(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = Normalize(e)
		}
		return out
	default:
		return v
	}
}

// Equal compares two wire values after normalization. Unknown is never equal.
func Equal(a, b any) bool {
	if hasUnknown(a) || hasUnknown(b) {
		return false
	}
	return reflect.DeepEqual(Normalize(a), Normalize(b))
}

func hasUnknown(v any) bool {
	switch t := v.(type) {
	case Unknown:
		return true
	case map[string]any:
		for _, e := range t {
			if hasUnknown(e) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if hasUnknown(e) {
				return true
			}
		}
	}
	return false
}

// LeafPaths lists the managed paths of a wire object: objects are walked into
// (by the kind's object fields), everything else is a leaf. Sorted.
func LeafPaths(k *Kind, obj map[string]any) []Path {
	var out []Path
	leafPaths(k.Fields, obj, nil, &out)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func leafPaths(fs map[string]*Field, obj map[string]any, at Path, out *[]Path) {
	for w, v := range obj {
		p := append(append(Path{}, at...), w)
		f := fs[w]
		if m, ok := v.(map[string]any); ok && f != nil && f.Type == "object" && len(m) > 0 {
			leafPaths(f.Fields, m, p, out)
			continue
		}
		*out = append(*out, p)
	}
}

// Get returns the value at a path and whether it is present.
func Get(obj map[string]any, p Path) (any, bool) {
	var cur any = obj
	for _, s := range p {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[s]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// Set writes a value at a path, creating (or replacing non-object)
// intermediate objects.
func Set(obj map[string]any, p Path, v any) {
	cur := obj
	for _, s := range p[:len(p)-1] {
		next, ok := cur[s].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[s] = next
		}
		cur = next
	}
	cur[p[len(p)-1]] = v
}

// FieldAt resolves a wire path to its field.
func FieldAt(k *Kind, p Path) *Field {
	fs := k.Fields
	var f *Field
	for _, s := range p {
		if fs == nil {
			return nil
		}
		f = fs[s]
		if f == nil {
			return nil
		}
		fs = f.Fields
	}
	return f
}

// DeepCopy copies a decoded JSON value.
func DeepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = DeepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = DeepCopy(e)
		}
		return out
	default:
		return v
	}
}
