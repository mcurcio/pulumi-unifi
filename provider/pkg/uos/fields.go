package uos

// Classic /api/s/{site}/rest kinds from api/fields/*.json: each file maps a
// field name to one validation string (a regex or a pipe list), a nested
// object, or a one-element list. Setting.json holds one object per settings
// section. The files state no defaults, required, read-only or secret flags.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
)

// ClassicKinds turns field files (base name -> JSON) into classic kinds.
func ClassicKinds(files map[string][]byte) ([]*kind.Kind, error) {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []*kind.Kind
	for _, n := range names {
		obj, err := decodeObject(files[n])
		if err != nil {
			return nil, fmt.Errorf("api/fields/%s.json: %w", n, err)
		}
		if n == "Setting" {
			secs := make([]string, 0, len(obj))
			for s := range obj {
				secs = append(secs, s)
			}
			sort.Strings(secs)
			for _, s := range secs {
				sec, ok := obj[s].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("api/fields/Setting.json: section %q is not an object", s)
				}
				out = append(out, classicKind("Setting."+s, "rest/setting/"+s, sec))
			}
			continue
		}
		out = append(out, classicKind(n, "rest/"+strings.ToLower(n), obj))
	}
	return out, nil
}

func decodeObject(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("top level is not an object")
	}
	return m, nil
}

func classicKind(name, coll string, obj map[string]any) *kind.Kind {
	return &kind.Kind{
		Name:       name,
		API:        kind.APIClassic,
		Collection: coll,
		ItemPath:   coll + "/{id}",
		// Verbs, listGet and itemGet are not stated by the field files; they
		// stay unknown until a write probe (slice 2).
		IDField:  "_id",
		Envelope: kind.EnvelopeMetaData,
		Fields:   classicFields(obj),
	}
}

func classicFields(obj map[string]any) map[string]*kind.Field {
	out := make(map[string]*kind.Field, len(obj))
	for k, v := range obj {
		f := classicField(v)
		f.WireName = k
		out[k] = f
	}
	return out
}

func classicField(v any) *kind.Field {
	switch t := v.(type) {
	case map[string]any:
		return &kind.Field{Type: kind.TypeObject, Fields: classicFields(t), Source: kind.SourceFields}
	case []any:
		var it *kind.Field
		switch len(t) {
		case 0:
			it = &kind.Field{Type: kind.TypeString, Source: kind.SourceFields}
		case 1:
			it = classicField(t[0])
		default:
			it = &kind.Field{Type: kind.TypeAny, Source: kind.SourceFields}
		}
		return &kind.Field{Type: kind.TypeArray, Items: it, Source: kind.SourceFields}
	case string:
		return inferValidation(t)
	}
	return &kind.Field{Type: kind.TypeAny, Source: kind.SourceFields}
}

var (
	reEnumToken = regexp.MustCompile(`^[A-Za-z0-9_.:+/-]*$`)
	reIntOnly   = regexp.MustCompile(`^-?[0-9]+$`)
	// a numeric rule: only digits, digit classes, quantifiers, anchors,
	// grouping and alternation; "\." makes it a decimal.
	reNumericRule = regexp.MustCompile(`^(?:\^|\$|\(|\)|\||\?|\*|\+|-|\[[0-9\-]*\]|\\d|[0-9]|\{[0-9,]*\}|\\\.|\[\.\])+$`)
)

// inferValidation infers a field's type from its validation string:
// "true|false" is a bool; a pipe list of plain tokens is an enum; a rule made
// only of digits is an int (number when it allows a decimal point); anything
// else is a string whose rule is kept as its pattern.
func inferValidation(rule string) *kind.Field {
	f := &kind.Field{Type: kind.TypeString, Source: kind.SourceFields}
	if rule == "" {
		return f
	}
	alts := strings.Split(rule, "|")
	var vals []string
	allTokens, allInts, emptyOK := true, true, false
	for _, a := range alts {
		if a == "^$" || a == "" {
			emptyOK = true
			continue
		}
		if !reEnumToken.MatchString(a) {
			allTokens = false
			break
		}
		if !reIntOnly.MatchString(a) {
			allInts = false
		}
		vals = append(vals, a)
	}
	if allTokens && len(vals) > 0 {
		set := map[string]bool{}
		for _, v := range vals {
			set[v] = true
		}
		if len(set) == 2 && set["true"] && set["false"] {
			f.Type = kind.TypeBool
			return f
		}
		if len(vals) >= 2 || !allInts {
			if allInts {
				f.Type = kind.TypeInt
			} else {
				f.Type = kind.TypeEnum
			}
			if emptyOK {
				vals = append([]string{""}, vals...)
			}
			f.Enum = vals
			return f
		}
	}
	if reNumericRule.MatchString(rule) && strings.ContainsAny(rule, "0123456789d") {
		if strings.Contains(rule, `\.`) || strings.Contains(rule, "[.]") {
			if !strings.Contains(rule, `\.){3}`) { // an IPv4 address rule is a string
				f.Type = kind.TypeNumber
				f.Pattern = rule
				return f
			}
		} else {
			f.Type = kind.TypeInt
			f.Pattern = rule
			return f
		}
	}
	f.Pattern = rule
	return f
}
