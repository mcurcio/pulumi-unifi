// Package engine is the private API's resource engine
// (docs/design/private-binding.md §8): rules 1-3 and G-Q1 of the frame, Check,
// Diff and import by natural key, over the generated surface (private.json).
// It knows nothing about HTTP: a Binding does the wire calls.
package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	core "github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
)

// TokenPrefix is the module every private token lives in (design §5).
const TokenPrefix = "unifi:network:"

// Surface is private.json: the merged result of the extracted model and the
// overrides for the exposed kinds. The generator writes it; the engine reads
// only this (design §3). It is a gitignored build artifact.
type Surface struct {
	// Source records the pins the surface was built from (empty when built
	// without a model).
	Source SurfaceSource `json:"source"`
	Kinds  []*Kind       `json:"kinds"`
	// Singletons are the engine-served official singletons (mappings.yaml
	// `singletons:`), present at PRIVATE=0 too. Slice 2 step 2 renames this
	// file surface.json and folds them into Kinds (private-binding.md §S2.2).
	Singletons []*core.Singleton `json:"singletons,omitempty"`
}

// SurfaceSource is the pin the surface was generated for.
type SurfaceSource struct {
	UOSVersion     string `json:"uosVersion,omitempty"`
	NetworkVersion string `json:"networkVersion,omitempty"`
}

// Kind is one exposed private kind, as the engine and the binding need it.
type Kind struct {
	Token string `json:"token"` // unifi:network:<Name>
	Kind  string `json:"kind"`  // the model's kind name ("qos-rules")
	API   string `json:"api"`   // v2 (classic later)
	// Paths are relative to the API's site base (v2: /v2/api/site/{site}/).
	// ItemPath holds "{id}".
	Collection string `json:"collection"`
	ListPath   string `json:"listPath"`
	ItemPath   string `json:"itemPath"`
	CreatePath string `json:"createPath"`
	ItemGet    bool   `json:"itemGet"`
	ListGet    bool   `json:"listGet"`
	CreateList bool   `json:"createList,omitempty"`
	IDField    string `json:"idField"`
	Envelope   string `json:"envelope"`
	// NaturalKey lists wire paths (rule 3).
	NaturalKey []string          `json:"naturalKey"`
	Fields     map[string]*Field `json:"fields"` // by wire name; the id field is not here
}

// Field is one field of an exposed kind.
type Field struct {
	Wire     string `json:"wire,omitempty"`
	Property string `json:"property,omitempty"`
	// Type is the model type (string|int|number|bool|uuid|enum|array|object|
	// map|date|datetime|any).
	Type        string            `json:"type"`
	Enum        []string          `json:"enum,omitempty"`
	Items       *Field            `json:"items,omitempty"`
	Values      *Field            `json:"values,omitempty"`
	Fields      map[string]*Field `json:"fields,omitempty"`
	Pattern     string            `json:"pattern,omitempty"`
	Constraints *Constraints      `json:"constraints,omitempty"`
	Required    bool              `json:"required,omitempty"`
	// Default is the value a dropped field resets to (G-Q1): the model's
	// decoded default, or an overrides `defaults:` entry. Nullable fields
	// (Java Optional<>) reset to null when they have no other default.
	Default     json.RawMessage `json:"default,omitempty"`
	Nullable    bool            `json:"nullable,omitempty"`
	ReadOnly    bool            `json:"readOnly,omitempty"`
	Secret      bool            `json:"secret,omitempty"`
	CreateOnly  bool            `json:"createOnly,omitempty"`
	NotReturned bool            `json:"notReturned,omitempty"`
}

// Constraints are the checkable validation facts.
type Constraints struct {
	MinLen   *int64   `json:"minLen,omitempty"`
	MaxLen   *int64   `json:"maxLen,omitempty"`
	MinItems *int64   `json:"minItems,omitempty"`
	MaxItems *int64   `json:"maxItems,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	NonEmpty bool     `json:"nonEmpty,omitempty"`
}

// HasDefault says whether a dropped field has a value to reset to.
func (f *Field) HasDefault() bool { return len(f.Default) > 0 || f.Nullable }

// DefaultValue is the reset value (nil for a nullable field without one).
func (f *Field) DefaultValue() (any, error) {
	if len(f.Default) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(f.Default, &v); err != nil {
		return nil, fmt.Errorf("default of %s: %w", f.Wire, err)
	}
	return v, nil
}

// ParseSurface reads private.json and indexes it.
func ParseSurface(b []byte) (*Surface, error) {
	s := &Surface{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("parsing private.json: %w", err)
	}
	for _, k := range s.Kinds {
		if !strings.HasPrefix(k.Token, TokenPrefix) {
			return nil, fmt.Errorf("private.json: token %q is not in %s", k.Token, TokenPrefix)
		}
		if k.IDField == "" || k.ItemPath == "" || !strings.Contains(k.ItemPath, "{id}") {
			return nil, fmt.Errorf("private.json: %s has no id field or item path", k.Token)
		}
		fillWire(k.Fields)
	}
	for _, sg := range s.Singletons {
		if sg.Token == "" || sg.Path == "" || sg.Property == "" || sg.API != "official" || sg.Merge != core.MergeLeadingBlock {
			return nil, fmt.Errorf("private.json: singleton %+v is incomplete or not an official leadingBlock singleton", *sg)
		}
	}
	return s, nil
}

// FindSingleton returns the singleton with this token, or nil.
func (s *Surface) FindSingleton(token string) *core.Singleton {
	for _, sg := range s.Singletons {
		if sg.Token == token {
			return sg
		}
	}
	return nil
}

func fillWire(fs map[string]*Field) {
	for w, f := range fs {
		if f.Wire == "" {
			f.Wire = w
		}
		fillWire(f.Fields)
		for it := f.Items; it != nil; it = it.Items {
			fillWire(it.Fields)
		}
		if f.Values != nil {
			fillWire(f.Values.Fields)
		}
	}
}

// Find returns the kind with this token, or nil.
func (s *Surface) Find(token string) *Kind {
	for _, k := range s.Kinds {
		if k.Token == token {
			return k
		}
	}
	return nil
}

// Tokens lists the surface's tokens, sorted.
func (s *Surface) Tokens() []string {
	out := make([]string, 0, len(s.Kinds))
	for _, k := range s.Kinds {
		out = append(out, k.Token)
	}
	sort.Strings(out)
	return out
}

// byProperty indexes fields by Pulumi property name.
func byProperty(fs map[string]*Field) map[string]*Field {
	out := make(map[string]*Field, len(fs))
	for _, f := range fs {
		out[f.Property] = f
	}
	return out
}

// sortedKeys returns a map's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
