// Package engine is the one resource engine of the engine-served kinds
// (docs/design/private-binding.md §S2.2, L3): rules 1-3 and G-Q1 of the frame,
// Check, Diff, import by natural key, the stored id, and the Kind facts
// (collection or singleton shape, the pinned variant, the merge strategy of a
// field), over the generated surface (surface.json). It serves the private
// kinds (v2, classic) and the official engine-served kinds (AclRuleOrdering)
// with one lifecycle. It knows nothing about HTTP, paths, envelopes or which
// API it talks to: a Binding per Kind.API does the wire calls, and the
// stored-id prefix comes from Binding.API().
package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PrivateTokenPrefix is the module every private (v2, classic) token lives in
// (design §5). Official engine-served kinds keep their official module.
const PrivateTokenPrefix = "unifi:network:"

// The APIs a kind can be served by (Kind.API); each has its own Binding.
const (
	APIOfficial = "official" // the Integration API (/proxy/network/integration/v1/...)
	APIV2       = "v2"       // the Network app's v2 API (/v2/api/site/{site}/...)
	APIClassic  = "classic"  // the Network app's classic API (/api/s/{site}/...)
)

// Surface is surface.json: every kind the engine serves, each tagged with the
// API that serves it. At PRIVATE=0 it holds only the official engine-served
// kinds (from the public spec and mappings.yaml `singletons:`); at PRIVATE=1
// the private kinds (the extracted model merged with the overrides) are
// added. The generator writes it; the engine reads only this (design §3). It
// is a gitignored build artifact.
type Surface struct {
	// Source records the pins the private kinds were built from (empty when
	// built without a model).
	Source SurfaceSource `json:"source"`
	Kinds  []*Kind       `json:"kinds"`
}

// SurfaceSource is the pin the surface was generated for.
type SurfaceSource struct {
	UOSVersion     string `json:"uosVersion,omitempty"`
	NetworkVersion string `json:"networkVersion,omitempty"`
}

// Kind is one engine-served kind, as the engine and its binding need it.
type Kind struct {
	Token string `json:"token"` // unifi:network:<Name>, or an official token
	Kind  string `json:"kind"`  // the model's kind name ("qos-rules"); empty for an official kind
	API   string `json:"api"`   // APIOfficial, APIV2 or APIClassic: the binding that serves it
	// Paths are relative to the API's site base (v2: /v2/api/site/{site}/;
	// official: the Integration API root, with {siteId}). ItemPath holds
	// "{id}" ("{siteId}" for a kind keyed by its site).
	Collection string `json:"collection"`
	ListPath   string `json:"listPath"`
	ItemPath   string `json:"itemPath"`
	CreatePath string `json:"createPath"`
	ItemGet    bool   `json:"itemGet"`
	ListGet    bool   `json:"listGet"`
	CreateList bool   `json:"createList,omitempty"`
	IDField    string `json:"idField"`
	Envelope   string `json:"envelope"`
	// NaturalKey lists wire paths (rule 3). Empty for a singleton.
	NaturalKey []string `json:"naturalKey"`
	// Shape is "collection" (empty) or "singleton": one document per site,
	// that Create adopts and Delete releases (private-binding.md §S2.5,
	// §S2.8).
	Shape string `json:"shape,omitempty"`
	// KeyedBySite marks a singleton whose document is named by its site, not
	// by an id field (AclRuleOrdering): read and written on ItemPath with the
	// site's id, stored as <api>:<site id>, imported by the site's name or
	// id, and given a siteId input (replace on change). Without it a
	// singleton is the one document ListPath answers, named by IDField (a
	// classic settings section).
	KeyedBySite bool `json:"keyedBySite,omitempty"`
	// SingletonKey is a singleton's import id besides its stored id (the
	// settings section name, "ntp").
	SingletonKey string `json:"singletonKey,omitempty"`
	// Variant pins one discriminator value (the G-Q4 variant exception,
	// §S2.7): sent on create and update, kept in lists and natural-key
	// lookups, checked on every read. The discriminator is not a Field.
	Variant *Variant `json:"variant,omitempty"`
	// CreateDefaults are wire values sent on create when the inputs lack
	// them, never managed afterwards (§S2.7, `acl_index`).
	CreateDefaults map[string]json.RawMessage `json:"createDefaults,omitempty"`
	// OfficialTokens names the official tokens that serve the kind's other
	// variants, for the changed-variant error.
	OfficialTokens []string          `json:"officialTokens,omitempty"`
	Fields         map[string]*Field `json:"fields"` // by wire name; the id field is not here
}

// Shapes of a kind.
const (
	ShapeCollection = "collection"
	ShapeSingleton  = "singleton"
)

// IsSingleton says whether the kind is one document per site.
func (k *Kind) IsSingleton() bool { return k.Shape == ShapeSingleton }

// Variant is a pinned discriminator value.
type Variant struct {
	Field string `json:"field"`
	Value string `json:"value"`
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
	// OutputOnly: owned by the server; an output, never an input, and kept at
	// its live value in a PUT body (unlike ReadOnly, which is not sent).
	OutputOnly bool `json:"outputOnly,omitempty"`
	// Merge is how the declared value meets the live one on write: empty
	// (MergeReplace, the declared value replaces the live one) or
	// MergeLeadingBlock (a list of ids that forms the leading block of the
	// live order; private-binding.md §S2.5).
	Merge string `json:"merge,omitempty"`
}

// Merge strategies of a field.
const (
	MergeReplace      = "replace"
	MergeLeadingBlock = "leadingBlock"
)

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

// ParseSurface reads surface.json and indexes it.
func ParseSurface(b []byte) (*Surface, error) {
	s := &Surface{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("parsing surface.json: %w", err)
	}
	for _, k := range s.Kinds {
		if err := checkKind(k); err != nil {
			return nil, fmt.Errorf("surface.json: %w", err)
		}
		fillWire(k.Fields)
	}
	return s, nil
}

// checkKind checks the facts the engine relies on.
func checkKind(k *Kind) error {
	switch k.API {
	case APIV2, APIClassic:
		if !strings.HasPrefix(k.Token, PrivateTokenPrefix) {
			return fmt.Errorf("token %q is not in %s", k.Token, PrivateTokenPrefix)
		}
	case APIOfficial:
		if strings.HasPrefix(k.Token, PrivateTokenPrefix) {
			return fmt.Errorf("official kind %q is in the private module %s", k.Token, PrivateTokenPrefix)
		}
	default:
		return fmt.Errorf("%s has api %q (want %s, %s or %s)", k.Token, k.API, APIOfficial, APIV2, APIClassic)
	}
	for _, f := range k.Fields {
		switch f.Merge {
		case "", MergeReplace:
		case MergeLeadingBlock:
			if !k.KeyedBySite {
				return fmt.Errorf("%s.%s: a leadingBlock field needs a singleton keyed by its site", k.Token, f.Property)
			}
		default:
			return fmt.Errorf("%s.%s has merge %q", k.Token, f.Property, f.Merge)
		}
	}
	switch k.Shape {
	case "", ShapeCollection:
		if k.KeyedBySite {
			return fmt.Errorf("%s: only a singleton is keyed by its site", k.Token)
		}
	case ShapeSingleton:
		if len(k.NaturalKey) != 0 || k.Variant != nil {
			return fmt.Errorf("singleton %s has a natural key or a variant", k.Token)
		}
		if k.KeyedBySite {
			// The one shape built so far: one leadingBlock list of ids.
			lb := k.LeadingBlock()
			if !k.ItemGet || !strings.Contains(k.ItemPath, "{siteId}") || k.IDField != "" || len(k.Fields) != 1 || lb == nil ||
				lb.Type != "array" || lb.Items == nil || lb.Items.Type != "string" {
				return fmt.Errorf("singleton %s keyed by its site needs an item GET on a {siteId} path, no id field, "+
					"and exactly one field: a leadingBlock list of string ids", k.Token)
			}
			return nil
		}
		if !k.ListGet {
			return fmt.Errorf("singleton %s needs a list path", k.Token)
		}
	default:
		return fmt.Errorf("%s has shape %q", k.Token, k.Shape)
	}
	if k.IDField == "" || k.ItemPath == "" || !strings.Contains(k.ItemPath, "{id}") {
		return fmt.Errorf("%s has no id field or item path", k.Token)
	}
	if k.Variant != nil && (k.Variant.Field == "" || k.Variant.Value == "" || k.Fields[k.Variant.Field] != nil) {
		return fmt.Errorf("%s variant %+v is incomplete or still a field", k.Token, *k.Variant)
	}
	return nil
}

// LeadingBlock returns the kind's leadingBlock field, or nil.
func (k *Kind) LeadingBlock() *Field {
	for _, w := range sortedKeys(k.Fields) {
		if k.Fields[w].Merge == MergeLeadingBlock {
			return k.Fields[w]
		}
	}
	return nil
}

// IsPrivate says whether a private API (v2, classic) serves the kind.
func (k *Kind) IsPrivate() bool { return k.API == APIV2 || k.API == APIClassic }

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

// Private lists the private (v2, classic) kinds, in surface order.
func (s *Surface) Private() []*Kind {
	var out []*Kind
	for _, k := range s.Kinds {
		if k.IsPrivate() {
			out = append(out, k)
		}
	}
	return out
}

// PrivateTokens lists the private kinds' tokens, sorted.
func (s *Surface) PrivateTokens() []string {
	var out []string
	for _, k := range s.Private() {
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
