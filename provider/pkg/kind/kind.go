// Package kind is the one Kind/Field model of the UniFi Network app's private
// API (docs/design/private-binding.md §3; frame §2). The extractor
// (provider/pkg/uos) writes it, the private generator and engine read it.
//
// The serialized model (build/uos/model.json) is a build artifact made from
// Ubiquiti's installer: it is gitignored and never committed (design §9).
// Canonical() is its only serializer, so the same inputs always give the same
// bytes (sorted keys, sorted kinds, no timestamps, no host paths).
package kind

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// API names the controller API that serves a kind.
const (
	APIV2      = "v2"      // /v2/api/site/{site}/<collection>
	APIClassic = "classic" // /api/s/{site}/rest/<kind> (and set/setting/<section>)
)

// Envelope is the wire shape of a response body.
const (
	EnvelopeBare     = "bare"      // the object (or a list of them) as is
	EnvelopeMetaData = "meta-data" // {"meta": {...}, "data": [...]}
)

// Field types. The design's public set is string|int|number|bool|uuid|enum|
// array|object|map; the extractor also keeps date, datetime and any (from the
// Java type) so the generator can choose how to surface them.
const (
	TypeString   = "string"
	TypeInt      = "int"
	TypeNumber   = "number"
	TypeBool     = "bool"
	TypeUUID     = "uuid"
	TypeEnum     = "enum"
	TypeArray    = "array"
	TypeObject   = "object"
	TypeMap      = "map"
	TypeDate     = "date"
	TypeDateTime = "datetime"
	TypeAny      = "any"
)

// Field sources.
const (
	SourceBytecode = "bytecode" // Spring/Jackson/Jakarta annotations in the Network app classes
	SourceFields   = "fields"   // api/fields/*.json
)

// Model is the whole extracted model for one pinned installer.
type Model struct {
	Source Source  `json:"source"`
	Kinds  []*Kind `json:"kinds"`
}

// Source records which pinned inputs produced the model. No paths, no times.
type Source struct {
	UOSVersion      string `json:"uosVersion"`
	NetworkVersion  string `json:"networkVersion"`
	InstallerSHA256 string `json:"installerSha256"`
	// IntegrationSHA256 is the SHA-256 of the image's own official spec
	// (webapps/ROOT/api-docs/integration.json): the pin tie to openapi/pin.env.
	IntegrationSHA256 string `json:"integrationSha256"`
	ExtractorVersion  string `json:"extractorVersion"`
}

// Kind is one controller object kind.
type Kind struct {
	// Name is unique within an API: the collection for v2 kinds
	// ("qos-rules"), the field-file name for classic kinds ("NetworkConf",
	// "Setting.mgmt").
	Name string `json:"name"`
	API  string `json:"api"`
	// Collection, ItemPath and CreatePath are relative to the API's site base
	// (v2: /v2/api/site/{site}/; classic: /api/s/{site}/).
	Collection string `json:"collection"`
	ItemPath   string `json:"itemPath,omitempty"`
	CreatePath string `json:"createPath,omitempty"`
	// Verbs is "CRUD" with '-' for a missing verb; empty when unknown (classic).
	Verbs string `json:"verbs"`
	// ListGet: the collection answers GET. ItemGet: the item path answers GET;
	// false means read by listing and filtering (measured 405s are recorded in
	// the overrides file too).
	ListGet bool `json:"listGet"`
	ItemGet bool `json:"itemGet"`
	// CreateList: the create body is a JSON list of objects.
	CreateList bool   `json:"createList,omitempty"`
	IDField    string `json:"idField"`
	Envelope   string `json:"envelope"`
	// Polymorph is set when the body itself is a tagged union.
	Polymorph *Polymorph        `json:"polymorph,omitempty"`
	Fields    map[string]*Field `json:"fields"`
}

// Polymorph describes a Jackson tagged union (@JsonTypeInfo + @JsonSubTypes).
type Polymorph struct {
	Prop     string                       `json:"prop"`
	Variants map[string]map[string]*Field `json:"variants"`
}

// Constraints are the validation facts a field states.
type Constraints struct {
	Len      *Range   `json:"len,omitempty"`  // @Length (strings)
	Size     *Range   `json:"size,omitempty"` // @Size (strings or collections)
	Min      *float64 `json:"min,omitempty"`  // @Min / @DecimalMin
	Max      *float64 `json:"max,omitempty"`  // @Max / @DecimalMax
	NonEmpty bool     `json:"nonEmpty,omitempty"`
	Custom   []string `json:"custom,omitempty"` // custom validators' messages, sorted
}

// Range is an inclusive length/size bound; a nil end is open.
type Range struct {
	Min *int64 `json:"min,omitempty"`
	Max *int64 `json:"max,omitempty"`
}

// Field is the public facts of one field.
type Field struct {
	WireName string            `json:"wireName,omitempty"`
	Type     string            `json:"type"`
	Enum     []string          `json:"enum,omitempty"`
	Items    *Field            `json:"items,omitempty"`
	Values   *Field            `json:"values,omitempty"` // map values
	Fields   map[string]*Field `json:"fields,omitempty"`
	// Polymorph is set on an object field that is itself a tagged union.
	Polymorph   *Polymorph   `json:"polymorph,omitempty"`
	Pattern     string       `json:"pattern,omitempty"`
	Constraints *Constraints `json:"constraints,omitempty"`
	Required    bool         `json:"required,omitempty"`
	// Optional: the Java type is Optional<> (the field may be null).
	Optional bool `json:"optional,omitempty"`
	// Default is the decoded @Value.Default constant (JSON), when there is one.
	Default json.RawMessage `json:"default,omitempty"`
	// DefaultComputed: a default exists but is computed in code.
	DefaultComputed bool   `json:"defaultComputed,omitempty"`
	ReadOnly        bool   `json:"readOnly,omitempty"`
	WriteOnly       bool   `json:"writeOnly,omitempty"`
	Secret          bool   `json:"secret,omitempty"`
	Source          string `json:"source"`
}

// Sort puts the kinds in canonical order (API, then name).
func (m *Model) Sort() {
	sort.Slice(m.Kinds, func(i, j int) bool {
		if m.Kinds[i].API != m.Kinds[j].API {
			return m.Kinds[i].API < m.Kinds[j].API
		}
		return m.Kinds[i].Name < m.Kinds[j].Name
	})
}

// Find returns the kind with this API and name, or nil.
func (m *Model) Find(api, name string) *Kind {
	for _, k := range m.Kinds {
		if k.API == api && k.Name == name {
			return k
		}
	}
	return nil
}

// Canonical serializes the model canonically: kinds sorted, every object's
// keys sorted, two-space indent, trailing newline. Equal models give equal bytes.
func Canonical(m *Model) ([]byte, error) {
	cp := *m
	cp.Kinds = append([]*Kind(nil), m.Kinds...)
	cp.Sort()
	raw, err := json.Marshal(&cp)
	if err != nil {
		return nil, err
	}
	return CanonicalJSON(raw)
}

// CanonicalJSON re-encodes any JSON document with sorted keys, numbers kept
// verbatim, and no HTML escaping.
func CanonicalJSON(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil { // map keys are sorted by encoding/json
		return nil, err
	}
	return buf.Bytes(), nil
}

// Digest is the hex SHA-256 of the canonical serialization.
func Digest(m *Model) (string, error) {
	b, err := Canonical(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Parse reads a serialized model.
func Parse(b []byte) (*Model, error) {
	var m Model
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parsing kind model: %w", err)
	}
	return &m, nil
}

// Walk calls fn for every field of the kind (base fields, nested fields, list
// items' fields and polymorphic variants' fields) with its dotted path. List
// items add "[]" to the path ("rules[].name"). Order is deterministic.
func (k *Kind) Walk(fn func(path string, f *Field)) {
	walkFields(k.Fields, "", fn)
	if k.Polymorph != nil {
		walkVariants(k.Polymorph, "", fn)
	}
}

func walkFields(fs map[string]*Field, pre string, fn func(string, *Field)) {
	keys := make([]string, 0, len(fs))
	for k := range fs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		walkField(fs[k], pre+k, fn)
	}
}

func walkField(f *Field, path string, fn func(string, *Field)) {
	fn(path, f)
	for it, p := f.Items, path+"[]"; it != nil; it, p = it.Items, p+"[]" {
		if it.Fields != nil {
			walkFields(it.Fields, p+".", fn)
		}
		if it.Polymorph != nil {
			walkVariants(it.Polymorph, p+".", fn)
		}
	}
	if f.Fields != nil {
		walkFields(f.Fields, path+".", fn)
	}
	if f.Polymorph != nil {
		walkVariants(f.Polymorph, path+".", fn)
	}
}

func walkVariants(p *Polymorph, pre string, fn func(string, *Field)) {
	names := make([]string, 0, len(p.Variants))
	for n := range p.Variants {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		walkFields(p.Variants[n], pre, fn)
	}
}
