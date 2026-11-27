package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// IDPrefix is the prefix of every stored private id (frame rule 3: a stored
// id names its API).
const IDPrefix = "private:"

// otherPrefixes are stored-id prefixes of the other bindings. Such an id is
// never handed to the private API.
var otherPrefixes = []string{"official:", "mac:"}

// Binding does the private API's wire calls for one kind. Values are decoded
// JSON (numbers as json.Number), with the response envelope removed.
type Binding interface {
	// List returns the kind's collection.
	List(ctx context.Context, k *Kind) ([]map[string]any, error)
	// Get returns one object by its controller id; found is false on 404.
	// Called only for kinds whose item path answers GET.
	Get(ctx context.Context, k *Kind, id string) (obj map[string]any, found bool, err error)
	// Create posts body (an object, or a one-element list for CreateList
	// kinds) and returns the decoded response.
	Create(ctx context.Context, k *Kind, body any) (any, error)
	// Update puts the full body on the item path and returns the decoded
	// response.
	Update(ctx context.Context, k *Kind, id string, body map[string]any) (any, error)
	// Delete deletes the item; found is false on 404.
	Delete(ctx context.Context, k *Kind, id string) (found bool, err error)
}

// ErrNotFound reports an object that is gone.
var ErrNotFound = errors.New("not found")

// Engine runs the private kinds' lifecycle.
type Engine struct {
	Surface *Surface
	Binding Binding
}

// Failure is one Check failure.
type Failure struct {
	Property string
	Reason   string
}

// ParseID strips the private prefix. Any other form is an error.
func ParseID(id string) (string, error) {
	if strings.HasPrefix(id, IDPrefix) && len(id) > len(IDPrefix) {
		return strings.TrimPrefix(id, IDPrefix), nil
	}
	return "", fmt.Errorf("stored id %q does not name the private API (want %s<id>)", id, IDPrefix)
}

// ---------------------------------------------------------------- Check

// Check validates the declared values (rule 1: only values that differ from
// the old inputs are validated, so a value read back is never rejected),
// required fields, and G-Q1: a managed path dropped since the old inputs must
// have a default to reset to.
func (e *Engine) Check(k *Kind, olds, news resource.PropertyMap) []Failure {
	var fails []Failure
	add := func(p, f string, a ...any) { fails = append(fails, Failure{Property: p, Reason: fmt.Sprintf(f, a...)}) }
	nw, err := ToWire(k, news)
	if err != nil {
		add("", "%v", err)
		return fails
	}
	ow, err := ToWire(k, olds)
	if err != nil {
		ow = map[string]any{} // old inputs that no longer convert are not the user's fault
	}
	checkObject(k.Fields, nw, ow, nil, add)
	// G-Q1: dropped managed paths need a default.
	for _, p := range Dropped(k, ow, nw) {
		f := FieldAt(k, p)
		if f == nil || f.ReadOnly {
			continue
		}
		if !f.HasDefault() {
			add(propPath(k, p), "%s was removed from the program, but it has no known default to reset it to (G-Q1). "+
				"Keep it declared, or record its default in kinds.yaml `defaults:`", propPath(k, p))
		}
	}
	sort.Slice(fails, func(i, j int) bool {
		if fails[i].Property != fails[j].Property {
			return fails[i].Property < fails[j].Property
		}
		return fails[i].Reason < fails[j].Reason
	})
	return fails
}

func checkObject(fs map[string]*Field, nw, ow map[string]any, at Path, add func(string, string, ...any)) {
	for _, w := range sortedKeys(fs) {
		f := fs[w]
		p := append(append(Path{}, at...), w)
		v, has := nw[w]
		if !has || v == nil {
			if f.Required && !f.ReadOnly {
				add(pathProps(fs, p, at), "%s is required", f.Property)
			}
			continue
		}
		if _, unk := v.(Unknown); unk {
			continue
		}
		old, hadOld := ow[w]
		if f.Type == "object" {
			m, _ := v.(map[string]any)
			om, _ := old.(map[string]any)
			if om == nil {
				om = map[string]any{}
			}
			checkObject(f.Fields, m, om, p, add)
			continue
		}
		if hadOld && Equal(old, v) {
			continue // rule 1: a value read back is never rejected
		}
		for _, r := range checkValue(f, v) {
			add(pathProps(fs, p, at), "%s %s", f.Property, r)
		}
	}
}

// pathProps names a path by its last property (Pulumi reports failures per
// top-level property; nested ones carry their dotted name in the reason).
func pathProps(fs map[string]*Field, p, at Path) string {
	if len(at) == 0 && fs[p[0]] != nil {
		return fs[p[0]].Property
	}
	return ""
}

func propPath(k *Kind, p Path) string {
	fs := k.Fields
	var out []string
	for _, s := range p {
		f := fs[s]
		if f == nil {
			out = append(out, s)
			continue
		}
		out = append(out, f.Property)
		fs = f.Fields
	}
	return strings.Join(out, ".")
}

var patternCache = map[string]*regexp.Regexp{}

func checkValue(f *Field, v any) []string {
	if v == nil {
		return nil
	}
	if _, unk := v.(Unknown); unk {
		return nil
	}
	var out []string
	c := f.Constraints
	switch f.Type {
	case "enum":
		s, _ := v.(string)
		if len(f.Enum) > 0 && !contains(f.Enum, s) {
			out = append(out, fmt.Sprintf("must be one of %s, got %q", strings.Join(f.Enum, ", "), s))
		}
	case "string", "uuid", "date", "datetime":
		s, _ := v.(string)
		if c != nil {
			n := int64(utf8.RuneCountInString(s))
			if c.NonEmpty && n == 0 {
				out = append(out, "must not be empty")
			}
			if c.MinLen != nil && n < *c.MinLen {
				out = append(out, fmt.Sprintf("must be at least %d characters", *c.MinLen))
			}
			if c.MaxLen != nil && n > *c.MaxLen {
				out = append(out, fmt.Sprintf("must be at most %d characters", *c.MaxLen))
			}
		}
		if f.Pattern != "" {
			re, ok := patternCache[f.Pattern]
			if !ok {
				re, _ = regexp.Compile("^(?:" + f.Pattern + ")$") // a Java-only pattern is not checked
				patternCache[f.Pattern] = re
			}
			if re != nil && !re.MatchString(s) {
				out = append(out, fmt.Sprintf("must match %s", f.Pattern))
			}
		}
	case "int", "number":
		n := toFloat(v)
		if c != nil {
			if c.Min != nil && n < *c.Min {
				out = append(out, fmt.Sprintf("must be at least %v", *c.Min))
			}
			if c.Max != nil && n > *c.Max {
				out = append(out, fmt.Sprintf("must be at most %v", *c.Max))
			}
		}
	case "array":
		a, _ := v.([]any)
		if c != nil {
			n := int64(len(a))
			if c.NonEmpty && n == 0 {
				out = append(out, "must not be empty")
			}
			if c.MinItems != nil && n < *c.MinItems {
				out = append(out, fmt.Sprintf("must have at least %d items", *c.MinItems))
			}
			if c.MaxItems != nil && n > *c.MaxItems {
				out = append(out, fmt.Sprintf("must have at most %d items", *c.MaxItems))
			}
		}
		if f.Items != nil {
			for i, e := range a {
				if f.Items.Type == "object" {
					if m, ok := e.(map[string]any); ok {
						checkObject(f.Items.Fields, m, map[string]any{}, Path{fmt.Sprintf("[%d]", i)}, func(_ string, r string, a ...any) {
							out = append(out, fmt.Sprintf("[%d]: ", i)+fmt.Sprintf(r, a...))
						})
					}
					continue
				}
				for _, r := range checkValue(f.Items, e) {
					out = append(out, fmt.Sprintf("[%d] %s", i, r))
				}
			}
		}
	}
	return out
}

func toFloat(v any) float64 {
	switch t := Normalize(v).(type) {
	case float64:
		return t
	default:
		return math.NaN()
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// Dropped lists the managed paths of the old inputs that the new inputs no
// longer carry (G-Q1). A path is not dropped when the new inputs set one of
// its ancestors to a leaf value (that value replaces the whole subtree).
func Dropped(k *Kind, olds, news map[string]any) []Path {
	var out []Path
	for _, p := range LeafPaths(k, olds) {
		if _, ok := Get(news, p); ok {
			continue
		}
		covered := false
		for i := 1; i < len(p); i++ {
			if v, ok := Get(news, p[:i]); ok {
				if _, isObj := v.(map[string]any); !isObj {
					covered = true
					break
				}
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------- Diff

// DiffResult is the outcome of Diff.
type DiffResult struct {
	Changed  []string // top-level property names that differ, sorted
	Replaces []string // the subset whose change needs a replace (createOnly)
	// Detailed maps each changed managed path, as a dotted property path
	// ("destination.portMatchingType"), to "add", "update" or "delete".
	Detailed map[string]string
}

// Diff compares old and new inputs over managed paths only: unmanaged fields
// never diff. A createOnly field's change needs a replace.
func (e *Engine) Diff(k *Kind, oldInputs, newInputs resource.PropertyMap) (DiffResult, error) {
	ow, err := ToWire(k, oldInputs)
	if err != nil {
		return DiffResult{}, err
	}
	nw, err := ToWire(k, newInputs)
	if err != nil {
		return DiffResult{}, err
	}
	res := DiffResult{Detailed: map[string]string{}}
	mark := func(p Path, kind string) {
		f := k.Fields[p[0]]
		if f == nil {
			return
		}
		res.Detailed[propPath(k, p)] = kind
		if !contains(res.Changed, f.Property) {
			res.Changed = append(res.Changed, f.Property)
		}
		if cf := FieldAt(k, p); cf != nil && cf.CreateOnly || f.CreateOnly {
			if !contains(res.Replaces, f.Property) {
				res.Replaces = append(res.Replaces, f.Property)
			}
		}
	}
	for _, p := range LeafPaths(k, nw) {
		nv, _ := Get(nw, p)
		ov, had := Get(ow, p)
		switch {
		case !had:
			mark(p, "add")
		case !Equal(ov, nv):
			mark(p, "update")
		}
	}
	for _, p := range Dropped(k, ow, nw) {
		mark(p, "delete")
	}
	sort.Strings(res.Changed)
	sort.Strings(res.Replaces)
	return res, nil
}

// ---------------------------------------------------------------- Create

// Create posts a body built only from the inputs and returns the stored id
// and the outputs.
func (e *Engine) Create(ctx context.Context, k *Kind, inputs resource.PropertyMap) (string, resource.PropertyMap, error) {
	body, err := ToWire(k, inputs)
	if err != nil {
		return "", nil, err
	}
	if hasUnknown(body) {
		return "", nil, errors.New("create: inputs hold unknown values")
	}
	var send any = body
	if k.CreateList {
		send = []any{body}
	}
	resp, err := e.Binding.Create(ctx, k, send)
	if err != nil {
		return "", nil, err
	}
	obj := firstObject(resp)
	id := idOf(k, obj)
	if id == "" {
		// The response does not carry the new object: find it by natural key.
		live, err := e.lookupByKey(ctx, k, body)
		if err != nil {
			return "", nil, fmt.Errorf("create succeeded but the new object could not be found: %w", err)
		}
		obj, id = live, idOf(k, live)
	} else if k.ItemGet || k.ListGet {
		// Read back, so the outputs are what the controller holds.
		if live, err := e.fetch(ctx, k, id); err == nil {
			obj = live
		}
	}
	return IDPrefix + id, e.outputs(k, obj, body), nil
}

func firstObject(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case []any:
		if len(t) > 0 {
			if m, ok := t[0].(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

func idOf(k *Kind, obj map[string]any) string {
	if obj == nil {
		return ""
	}
	switch v := obj[k.IDField].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(Normalize(v))
	}
}

// outputs: every returned modelled field except undeclared secrets; declared
// secrets and notReturned fields come from the inputs when the controller
// does not return them.
func (e *Engine) outputs(k *Kind, live, declared map[string]any) resource.PropertyMap {
	merged := DeepCopy(live).(map[string]any)
	if merged == nil {
		merged = map[string]any{}
	}
	for _, p := range LeafPaths(k, declared) {
		f := FieldAt(k, p)
		if f == nil {
			continue
		}
		if _, ok := Get(merged, p); !ok && (f.Secret || f.NotReturned) {
			v, _ := Get(declared, p)
			Set(merged, p, v)
		}
	}
	return FromWire(k, merged, func(p Path, f *Field) bool {
		if !f.Secret {
			return true
		}
		_, ok := Get(declared, p)
		return ok
	})
}

// ---------------------------------------------------------------- Read

// ReadResult is the outcome of Read.
type ReadResult struct {
	ID      string
	Outputs resource.PropertyMap
	Inputs  resource.PropertyMap
}

// Read refreshes one object. With old inputs, the new inputs are the live
// object projected onto their managed paths. With none (an import), id may be
// a stored id or a natural-key value, and the inputs are every writable,
// non-secret field the controller returns (Pulumi narrows them to the
// import's `properties`). A gone object returns (nil, nil).
func (e *Engine) Read(ctx context.Context, k *Kind, id string, oldInputs resource.PropertyMap, importing bool) (*ReadResult, error) {
	var live map[string]any
	if importing && !strings.HasPrefix(id, IDPrefix) {
		for _, pre := range otherPrefixes {
			if strings.HasPrefix(id, pre) {
				return nil, fmt.Errorf("import id %q names another API; %s is served by the private API (use %s<id> or its natural key %s)",
					id, k.Token, IDPrefix, strings.Join(k.NaturalKey, ","))
			}
		}
		key, err := parseNaturalKey(k, id)
		if err != nil {
			return nil, err
		}
		live, err = e.lookupByKey(ctx, k, key)
		if err != nil {
			return nil, err
		}
	} else {
		raw, err := ParseID(id)
		if err != nil {
			return nil, err
		}
		live, err = e.fetch(ctx, k, raw)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	res := &ReadResult{ID: IDPrefix + idOf(k, live)}
	if importing || oldInputs == nil {
		res.Inputs = FromWire(k, live, func(_ Path, f *Field) bool { return !f.ReadOnly && !f.Secret })
		res.Outputs = e.outputs(k, live, nil)
		return res, nil
	}
	ow, err := ToWire(k, oldInputs)
	if err != nil {
		return nil, err
	}
	proj := map[string]any{}
	for _, p := range LeafPaths(k, ow) {
		f := FieldAt(k, p)
		if v, ok := Get(live, p); ok && !(f != nil && f.NotReturned) {
			Set(proj, p, v)
		} else if f != nil && (f.NotReturned || f.Secret) {
			ov, _ := Get(ow, p)
			Set(proj, p, ov)
		}
		// else: absent on the controller, so it drops out of the inputs and
		// the next diff shows it (rule 1: every managed path round-trips).
	}
	res.Inputs = FromWire(k, proj, nil)
	res.Outputs = e.outputs(k, live, ow)
	return res, nil
}

// fetch reads one object by controller id: item GET, or list + filter.
func (e *Engine) fetch(ctx context.Context, k *Kind, id string) (map[string]any, error) {
	if k.ItemGet {
		obj, found, err := e.Binding.Get(ctx, k, id)
		if err != nil {
			return nil, err
		}
		if !found || obj == nil {
			return nil, ErrNotFound
		}
		return obj, nil
	}
	all, err := e.Binding.List(ctx, k)
	if err != nil {
		return nil, err
	}
	for _, o := range all {
		if idOf(k, o) == id {
			return o, nil
		}
	}
	return nil, ErrNotFound
}

// parseNaturalKey reads an import id: the bare value for a one-field key, or
// "field=value;field=value" (wire or property names).
func parseNaturalKey(k *Kind, id string) (map[string]any, error) {
	key := map[string]any{}
	if id == "" {
		return nil, errors.New("import id is empty")
	}
	names := map[string]string{}
	for _, w := range k.NaturalKey {
		names[w] = w
		if f := FieldAt(k, strings.Split(w, ".")); f != nil {
			names[f.Property] = w
		}
	}
	if len(k.NaturalKey) == 1 && !strings.Contains(id, "=") {
		Set(key, strings.Split(k.NaturalKey[0], "."), id)
		return key, nil
	}
	for _, part := range strings.Split(id, ";") {
		name, val, ok := strings.Cut(part, "=")
		w, known := names[name]
		if !ok || !known {
			if len(k.NaturalKey) == 1 {
				Set(key, strings.Split(k.NaturalKey[0], "."), id)
				return key, nil
			}
			return nil, fmt.Errorf("import id %q: want %s", id, keyHint(k))
		}
		Set(key, strings.Split(w, "."), val)
	}
	for _, w := range k.NaturalKey {
		if _, ok := Get(key, strings.Split(w, ".")); !ok {
			return nil, fmt.Errorf("import id %q misses %s: want %s", id, w, keyHint(k))
		}
	}
	return key, nil
}

func keyHint(k *Kind) string {
	var parts []string
	for _, w := range k.NaturalKey {
		parts = append(parts, w+"=<value>")
	}
	return IDPrefix + "<id> or " + strings.Join(parts, ";")
}

// lookupByKey lists the collection and returns the one object whose natural
// key matches; zero or several matches is an error naming the candidates.
func (e *Engine) lookupByKey(ctx context.Context, k *Kind, src map[string]any) (map[string]any, error) {
	all, err := e.Binding.List(ctx, k)
	if err != nil {
		return nil, err
	}
	var want []string
	for _, w := range k.NaturalKey {
		v, _ := Get(src, strings.Split(w, "."))
		want = append(want, keyString(v))
	}
	var hits []map[string]any
	var cands []string
	for _, o := range all {
		var got []string
		for _, w := range k.NaturalKey {
			v, _ := Get(o, strings.Split(w, "."))
			got = append(got, keyString(v))
		}
		cands = append(cands, strings.Join(got, ";"))
		if strings.Join(got, "\x00") == strings.Join(want, "\x00") {
			hits = append(hits, o)
		}
	}
	desc := describeKey(k, want)
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		sort.Strings(cands)
		return nil, fmt.Errorf("no %s has %s (candidates: %s)", k.Token, desc, listOrNone(cands))
	default:
		var ids []string
		for _, h := range hits {
			ids = append(ids, IDPrefix+idOf(k, h))
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("%d %s objects have %s: %s; import one by its id", len(hits), k.Token, desc, strings.Join(ids, ", "))
	}
}

func describeKey(k *Kind, vals []string) string {
	var parts []string
	for i, w := range k.NaturalKey {
		parts = append(parts, fmt.Sprintf("%s=%q", w, vals[i]))
	}
	return strings.Join(parts, ", ")
}

func listOrNone(ss []string) string {
	if len(ss) == 0 {
		return "none"
	}
	return strings.Join(ss, ", ")
}

func keyString(v any) string {
	switch t := Normalize(v).(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

// ---------------------------------------------------------------- Update

// Update (rule 2, G-Q1): fresh read, reset every dropped managed path to its
// default, deep-merge the new inputs' managed paths onto it, full PUT.
// Unmanaged fields keep their live values; read-only fields are not sent.
func (e *Engine) Update(ctx context.Context, k *Kind, id string, oldInputs, newInputs resource.PropertyMap) (resource.PropertyMap, error) {
	raw, err := ParseID(id)
	if err != nil {
		return nil, err
	}
	ow, err := ToWire(k, oldInputs)
	if err != nil {
		return nil, err
	}
	nw, err := ToWire(k, newInputs)
	if err != nil {
		return nil, err
	}
	if hasUnknown(nw) {
		return nil, errors.New("update: inputs hold unknown values")
	}
	live, err := e.fetch(ctx, k, raw)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%s %s is gone from the controller; run `pulumi refresh`", k.Token, id)
		}
		return nil, err
	}
	body, err := MergeBody(k, live, ow, nw)
	if err != nil {
		return nil, err
	}
	resp, err := e.Binding.Update(ctx, k, raw, body)
	if err != nil {
		return nil, err
	}
	obj := firstObject(resp)
	if idOf(k, obj) != raw {
		if fresh, err := e.fetch(ctx, k, raw); err == nil {
			obj = fresh
		} else {
			obj = body
		}
	}
	return e.outputs(k, obj, nw), nil
}

// MergeBody builds the PUT body: the live object without read-only fields,
// every dropped managed path set to its default, then the new inputs' managed
// paths on top.
func MergeBody(k *Kind, live, olds, news map[string]any) (map[string]any, error) {
	body := DeepCopy(live).(map[string]any)
	stripReadOnly(k.Fields, body)
	if v, ok := live[k.IDField]; ok {
		body[k.IDField] = v // the id travels with the object
	}
	for _, p := range Dropped(k, olds, news) {
		f := FieldAt(k, p)
		if f == nil || f.ReadOnly {
			continue
		}
		if !f.HasDefault() {
			return nil, fmt.Errorf("%s was removed from the program, but it has no known default to reset it to (G-Q1)", propPath(k, p))
		}
		d, err := f.DefaultValue()
		if err != nil {
			return nil, err
		}
		Set(body, p, d)
	}
	for _, p := range LeafPaths(k, news) {
		v, _ := Get(news, p)
		Set(body, p, DeepCopy(v))
	}
	return body, nil
}

func stripReadOnly(fs map[string]*Field, obj map[string]any) {
	for w, v := range obj {
		f := fs[w]
		if f == nil {
			continue // unmodelled: kept as the controller holds it
		}
		if f.ReadOnly {
			delete(obj, w)
			continue
		}
		if m, ok := v.(map[string]any); ok && f.Type == "object" {
			stripReadOnly(f.Fields, m)
		}
	}
}

// ---------------------------------------------------------------- Delete

// Delete deletes the object; one already gone counts as deleted.
func (e *Engine) Delete(ctx context.Context, k *Kind, id string) error {
	raw, err := ParseID(id)
	if err != nil {
		return err
	}
	_, err = e.Binding.Delete(ctx, k, raw)
	return err
}
