package uos

// Assembling the kind.Model: every CRUD-shaped v2 collection (from the route
// table + DTO models, a port of dto.py --crud) and every classic field-file
// kind.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
)

// ExtractorVersion is recorded in the model. Bump it whenever a change to the
// extractor changes its output for the same installer.
const ExtractorVersion = "1"

// V2Prefix is the controller-side route prefix of v2 site routes, served
// under /v2/api/site/{site}/.
const V2Prefix = "/api/site/{siteName}/"

var reTrailingParam = regexp.MustCompile(`\{[^}]+\}$`)

// CrudKind is one CRUD-shaped collection found in the route table, with its
// body models (the intermediate form; kept for the parity oracle).
type CrudKind struct {
	Collection string
	Create     string // create path (full)
	Item       string // item path (full), "" if none
	Verbs      string
	ListGet    bool
	ItemGet    bool
	Request    *tmodel // create body
	Response   *tmodel // item (or create) response body, list unwrapped
}

// Extractor runs route and DTO extraction over one class index.
type Extractor struct {
	x      *Index
	d      *dto
	routes []Route
	first  map[[2]string]*Route
}

// NewExtractor extracts the route table from the class index.
func NewExtractor(x *Index) (*Extractor, error) {
	rs, err := Routes(x)
	if err != nil {
		return nil, err
	}
	e := &Extractor{x: x, d: newDTO(x), routes: rs, first: map[[2]string]*Route{}}
	for i := range rs {
		k := [2]string{rs[i].Verb, rs[i].Path}
		if _, ok := e.first[k]; !ok {
			e.first[k] = &rs[i]
		}
	}
	return e, nil
}

// Routes returns the route table.
func (e *Extractor) Routes() []Route { return e.routes }

// route returns the first handler for verb+path (route-table order).
func (e *Extractor) route(verb, path string) *Route { return e.first[[2]string{verb, path}] }

// BodyModel models one route's request body, or nil.
func (e *Extractor) BodyModel(verb, path string) *tmodel {
	r := e.route(verb, path)
	if r == nil {
		return nil
	}
	t, ok := e.d.bodyType(r)
	if !ok {
		return nil
	}
	return e.d.model(t, 0, map[string]bool{})
}

func (e *Extractor) returnModel(verb, path string) *tmodel {
	r := e.route(verb, path)
	if r == nil {
		return nil
	}
	t, ok := e.d.returnType(r)
	if !ok {
		return nil
	}
	return e.d.model(t, 0, map[string]bool{})
}

// Crud finds every CRUD-shaped collection under prefix: a create (POST on
// the collection, or on <collection>/create) and an item path with PUT or
// DELETE.
func (e *Extractor) Crud(prefix string) []CrudKind {
	verbs := map[string]map[string]bool{}
	var order []string // paths in first-appearance (route-table) order
	for _, r := range e.routes {
		if verbs[r.Path] == nil {
			verbs[r.Path] = map[string]bool{}
			order = append(order, r.Path)
		}
		verbs[r.Path][r.Verb] = true
	}
	paths := append([]string(nil), order...)
	sort.Strings(paths)
	var out []CrudKind
	for _, p := range paths {
		vs := verbs[p]
		if !strings.HasPrefix(p, prefix) || reTrailingParam.MatchString(p) {
			continue
		}
		var items []string
		reItem := regexp.MustCompile("^" + regexp.QuoteMeta(p) + `/\{[^}]+\}$`)
		for _, q := range order {
			if reItem.MatchString(q) {
				items = append(items, q)
			}
		}
		iv := map[string]bool{}
		for _, q := range items {
			for v := range verbs[q] {
				iv[v] = true
			}
		}
		create := ""
		if vs["POST"] {
			create = p
		} else if verbs[p+"/create"]["POST"] {
			create = p + "/create"
		}
		if create == "" || !(iv["PUT"] || iv["DELETE"]) {
			continue
		}
		item := ""
		if len(items) > 0 {
			item = items[0]
		}
		ck := CrudKind{
			Collection: p[len(prefix):], Create: create, Item: item,
			ListGet: vs["GET"], ItemGet: iv["GET"],
		}
		flag := func(c byte, ok bool) byte {
			if ok {
				return c
			}
			return '-'
		}
		ck.Verbs = string([]byte{'C', flag('R', vs["GET"] || iv["GET"]), flag('U', iv["PUT"]), flag('D', iv["DELETE"])})
		ck.Request = e.BodyModel("POST", create)
		var resp *tmodel
		if item != "" && iv["GET"] {
			resp = e.returnModel("GET", item)
		}
		if resp == nil {
			resp = e.returnModel("POST", create)
		}
		if resp != nil && resp.Type == "array" {
			resp = resp.Items
		}
		ck.Response = resp
		out = append(out, ck)
	}
	return out
}

// V2Kinds converts the CRUD collections into kinds.
func V2Kinds(cks []CrudKind) []*kind.Kind {
	var out []*kind.Kind
	for _, ck := range cks {
		k := &kind.Kind{
			Name:       ck.Collection,
			API:        kind.APIV2,
			Collection: ck.Collection,
			CreatePath: strings.TrimPrefix(ck.Create, V2Prefix),
			Verbs:      ck.Verbs,
			ListGet:    ck.ListGet,
			ItemGet:    ck.ItemGet,
			Envelope:   kind.EnvelopeBare,
			Fields:     map[string]*kind.Field{},
		}
		if ck.Item != "" {
			k.ItemPath = strings.TrimPrefix(ck.Item, V2Prefix)
		}
		req := ck.Request
		if req != nil && req.Type == "array" {
			k.CreateList = true
			req = req.Items
		}
		if req != nil && req.Type == "object" {
			k.Fields = convertFields(req.Fields)
			k.Polymorph = convertPolymorph(req)
		}
		if ck.Response != nil && ck.Response.Type == "object" {
			addResponseOnly(k.Fields, convertFields(ck.Response.Fields))
		}
		k.IDField = "_id"
		if _, ok := k.Fields["_id"]; !ok {
			if _, ok := k.Fields["id"]; ok {
				k.IDField = "id"
			}
		}
		out = append(out, k)
	}
	return out
}

// addResponseOnly merges response fields into the request fields: a field
// only the response has is read-only; objects and lists of objects recurse.
func addResponseOnly(dst, resp map[string]*kind.Field) {
	for name, rf := range resp {
		df, ok := dst[name]
		if !ok {
			setReadOnly(rf)
			dst[name] = rf
			continue
		}
		a, b := df, rf
		for a.Items != nil && b.Items != nil {
			a, b = a.Items, b.Items
		}
		if a.Fields != nil && b.Fields != nil {
			addResponseOnly(a.Fields, b.Fields)
		}
	}
}

func setReadOnly(f *kind.Field) {
	f.ReadOnly = true
	if f.Items != nil {
		setReadOnly(f.Items)
	}
	for _, c := range f.Fields {
		setReadOnly(c)
	}
	if f.Polymorph != nil {
		for _, v := range f.Polymorph.Variants {
			for _, c := range v {
				setReadOnly(c)
			}
		}
	}
}

func convertFields(fs []tfield) map[string]*kind.Field {
	out := make(map[string]*kind.Field, len(fs))
	for _, tf := range fs {
		f := convert(tf.M)
		f.WireName = tf.Name
		out[tf.Name] = f
	}
	return out
}

func convertPolymorph(m *tmodel) *kind.Polymorph {
	if len(m.Variants) == 0 {
		return nil
	}
	p := &kind.Polymorph{Prop: m.Disc, Variants: map[string]map[string]*kind.Field{}}
	for _, v := range m.Variants {
		fs := map[string]*kind.Field{}
		if v.M != nil && v.M.Type == "object" {
			fs = convertFields(v.M.Fields)
		}
		p.Variants[v.Name] = fs
	}
	return p
}

func convert(m *tmodel) *kind.Field {
	f := &kind.Field{Type: m.Type, Source: kind.SourceBytecode}
	if m.Type == "ref" {
		f.Type = kind.TypeAny
	}
	f.Enum = m.Values
	if m.Items != nil {
		f.Items = convert(m.Items)
	}
	if m.MapValues != nil {
		f.Values = convert(m.MapValues)
	}
	if m.Type == "object" {
		f.Fields = convertFields(m.Fields)
		f.Polymorph = convertPolymorph(m)
	}
	f.Pattern = m.Pattern
	f.Required, f.Optional, f.ReadOnly, f.WriteOnly = m.Required, m.Optional, m.ReadOnly, m.WriteOnly
	if m.HasDefault {
		if m.DefaultComputed {
			f.DefaultComputed = true
		} else {
			b, err := json.Marshal(m.Default)
			if err == nil {
				f.Default = b
			} else {
				f.DefaultComputed = true
			}
		}
	}
	c := &kind.Constraints{NonEmpty: m.NonEmpty, Min: m.Min, Max: m.Max, Custom: m.Custom}
	if m.Len != nil {
		c.Len = &kind.Range{Min: m.Len.Min, Max: m.Len.Max}
	}
	if m.Size != nil {
		c.Size = &kind.Range{Min: m.Size.Min, Max: m.Size.Max}
	}
	if c.Len != nil || c.Size != nil || c.Min != nil || c.Max != nil || c.NonEmpty || len(c.Custom) > 0 {
		f.Constraints = c
	}
	return f
}

// BuildModel assembles the whole model from an extracted installer.
func BuildModel(ex *Extracted, uosVersion string) (*kind.Model, error) {
	idx := NewIndex(NewZipClasses(ex.Deps))
	e, err := NewExtractor(idx)
	if err != nil {
		return nil, fmt.Errorf("route table: %w", err)
	}
	kinds := V2Kinds(e.Crud(V2Prefix))
	_, files, err := FieldFiles(ex.Deps)
	if err != nil {
		return nil, err
	}
	classic, err := ClassicKinds(files)
	if err != nil {
		return nil, err
	}
	kinds = append(kinds, classic...)
	sens := Sensitive{}
	if b, err := readZipEntry(ex.Deps, SensitiveMetaPath); err == nil {
		if sens, err = ParseSensitive(b); err != nil {
			return nil, err
		}
	}
	for _, k := range kinds {
		MarkSecrets(k, sens)
	}
	m := &kind.Model{
		Source: kind.Source{
			UOSVersion:        uosVersion,
			NetworkVersion:    ex.NetworkVersion(),
			InstallerSHA256:   ex.InstallerSHA256,
			IntegrationSHA256: ex.IntegrationSHA256(),
			ExtractorVersion:  ExtractorVersion,
		},
		Kinds: kinds,
	}
	m.Sort()
	return m, nil
}
