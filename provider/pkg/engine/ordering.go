// Package engine is the domain layer of the engine-served official kinds
// (docs/design/private-binding.md §S2.2, L3). Today it holds the ordered
// singleton: one document per site whose managed list owns the leading block of
// a live order (AclRuleOrdering, §S2.5). It never sees HTTP, paths or which API
// it talks to: an OrderBinding does the wire calls, and the stored-id prefix
// comes from OrderBinding.API().
//
// Slice 2 step 2 moves the private engine (provider/pkg/private/engine) here and
// expresses the singleton as a Kind fact (Kind.Shape = singleton, Field.Merge =
// leadingBlock); until then this package serves the singletons on its own
// (private-binding.md §S2.14).
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// Merge strategies of a singleton's managed list.
const MergeLeadingBlock = "leadingBlock"

// Unlisted policies (the `unlisted` input).
const (
	UnlistedLast   = "LAST"
	UnlistedReject = "REJECT"
)

// Property names the leadingBlock merge adds to every singleton's schema
// (provider/pkg/gen/pass_singletons.go generates the same names).
const (
	PropUnlisted       = "unlisted"
	PropEffectiveOrder = "effectiveOrder"
	PropSiteID         = "siteId"
)

// Singleton is one engine-served singleton, as the surface records it.
type Singleton struct {
	Token string `json:"token"`
	// API names the binding that serves it ("official").
	API string `json:"api"`
	// Path is the API path with {siteId} (official: /v1/sites/{siteId}/...).
	Path string `json:"path"`
	// Property is the managed list (the same name on the wire and in the SDK).
	Property string `json:"property"`
	Merge    string `json:"merge"`
}

// ErrIncomplete is the binding's outcome for a PUT the server refuses because
// it does not list every live entry (a rule was created elsewhere between the
// GET and the PUT).
var ErrIncomplete = errors.New("the order does not list every live entry")

// ErrGone is the binding's outcome for a site that is gone.
var ErrGone = errors.New("gone")

// OrderBinding does the wire calls for an ordered singleton.
type OrderBinding interface {
	// API is the stored-id prefix of what it serves ("official").
	API() string
	// SiteID turns the configured site (a name or an id) into the form the
	// API addresses sites by.
	SiteID(ctx context.Context, site string) (string, error)
	// GetOrder returns the live order.
	GetOrder(ctx context.Context, s *Singleton, siteID string) ([]string, error)
	// PutOrder replaces the live order and returns the order the server holds.
	PutOrder(ctx context.Context, s *Singleton, siteID string, ids []string) ([]string, error)
}

// Ordering runs the lifecycle of ordered singletons.
type Ordering struct {
	Binding OrderBinding
	// Site is the provider-level site (config siteId), used when the
	// resource does not set its own.
	Site func() string
	// Warn reports something the user should see (Delete releases).
	Warn func(msg string)
}

// Failure is one Check failure.
type Failure struct {
	Property string
	Reason   string
}

// otherAPIs are stored-id prefixes that never name an entry of this order.
var otherAPIs = []string{"private:", "official:", "mac:"}

// Check validates the inputs: the managed ids are strings without duplicates
// and without a stored-id prefix (a private kind's object is ordered by its
// official id, its aclRuleId output), and unlisted is LAST or REJECT. Unknown
// values (rules not created yet) pass.
func (o *Ordering) Check(s *Singleton, news resource.PropertyMap) []Failure {
	var fails []Failure
	add := func(p, f string, a ...any) { fails = append(fails, Failure{Property: p, Reason: fmt.Sprintf(f, a...)}) }
	v, ok := news[resource.PropertyKey(s.Property)]
	switch {
	case !ok || v.IsNull():
		add(s.Property, "%s is required", s.Property)
	case v.IsComputed() || v.IsOutput() && !v.OutputValue().Known:
	case !unwrap(v).IsArray():
		add(s.Property, "%s must be a list of ids", s.Property)
	default:
		seen := map[string]bool{}
		for i, e := range unwrap(v).ArrayValue() {
			e = unwrap(e)
			if e.IsComputed() || e.IsOutput() && !e.OutputValue().Known {
				continue
			}
			if !e.IsString() || e.StringValue() == "" {
				add(s.Property, "%s[%d] must be a non-empty id", s.Property, i)
				continue
			}
			id := e.StringValue()
			for _, p := range otherAPIs {
				if strings.HasPrefix(id, p) {
					add(s.Property, "%s[%d] %q is a stored resource id, not an entry id; for a private kind pass its aclRuleId output", s.Property, i, id)
				}
			}
			if seen[id] {
				add(s.Property, "%s lists %q twice", s.Property, id)
			}
			seen[id] = true
		}
	}
	if u, ok := news[PropUnlisted]; ok && !u.IsNull() && !u.IsComputed() {
		if uv := unwrap(u); !uv.IsString() || (uv.StringValue() != UnlistedLast && uv.StringValue() != UnlistedReject) {
			add(PropUnlisted, "unlisted must be %s or %s", UnlistedLast, UnlistedReject)
		}
	}
	return fails
}

// Compose is the PUT body of the leadingBlock merge: the managed ids in the
// declared order, then every other live id in live relative order. With
// REJECT, a live id the program does not list is an error naming all of them.
// A managed id that is not live is kept: the server's answer names it.
func Compose(managed, live []string, unlisted string) ([]string, error) {
	in := map[string]bool{}
	for _, id := range managed {
		in[id] = true
	}
	out := append([]string(nil), managed...)
	var rest []string
	for _, id := range live {
		if !in[id] {
			rest = append(rest, id)
		}
	}
	if unlisted == UnlistedReject && len(rest) > 0 {
		return nil, fmt.Errorf("unlisted is REJECT, but the site holds entries the program does not list: %s", strings.Join(rest, ", "))
	}
	return append(out, rest...), nil
}

// Project is the managed list as read back: the first n live ids, n being the
// number the program manages (rule 1 for a list whose position is managed).
// Drift shows when an unmanaged entry moves ahead of a managed one, when the
// managed ones are reordered, or when a managed one is gone.
func Project(live []string, n int) []string {
	if n > len(live) {
		n = len(live)
	}
	return append([]string{}, live[:n]...)
}

// Created is a create or update result.
type Created struct {
	ID      string
	Outputs resource.PropertyMap
}

// Read is a read result; nil means gone.
type Read struct {
	ID      string
	Inputs  resource.PropertyMap
	Outputs resource.PropertyMap
}

func (o *Ordering) site(in resource.PropertyMap) string {
	if v, ok := in[PropSiteID]; ok {
		if uv := unwrap(v); uv.IsString() && uv.StringValue() != "" {
			return uv.StringValue()
		}
	}
	if o.Site != nil && o.Site() != "" {
		return o.Site()
	}
	return "default"
}

// StoredID is the stored id of the singleton of a site: <api>:<siteId>.
func (o *Ordering) StoredID(siteID string) string { return o.Binding.API() + ":" + siteID }

// parseID returns the site id of a stored id.
func (o *Ordering) parseID(id string) (string, error) {
	p := o.Binding.API() + ":"
	if strings.HasPrefix(id, p) && len(id) > len(p) {
		return strings.TrimPrefix(id, p), nil
	}
	return "", fmt.Errorf("stored id %q does not name the %s API (want %s<site id>)", id, o.Binding.API(), p)
}

// Create adopts the site's order: GET, compose, PUT. It never fails because
// entries exist (there is nothing to create).
func (o *Ordering) Create(ctx context.Context, s *Singleton, in resource.PropertyMap) (*Created, error) {
	siteID, err := o.Binding.SiteID(ctx, o.site(in))
	if err != nil {
		return nil, err
	}
	return o.apply(ctx, s, siteID, in)
}

// Update composes and PUTs onto a fresh read.
func (o *Ordering) Update(ctx context.Context, s *Singleton, id string, in resource.PropertyMap) (*Created, error) {
	siteID, err := o.parseID(id)
	if err != nil {
		return nil, err
	}
	return o.apply(ctx, s, siteID, in)
}

func (o *Ordering) apply(ctx context.Context, s *Singleton, siteID string, in resource.PropertyMap) (*Created, error) {
	managed, err := stringList(in[resource.PropertyKey(s.Property)])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.Property, err)
	}
	unlisted := unlistedOf(in)
	var got []string
	for attempt := 0; ; attempt++ {
		live, err := o.Binding.GetOrder(ctx, s, siteID)
		if err != nil {
			return nil, err
		}
		body, err := Compose(managed, live, unlisted)
		if err != nil {
			return nil, err
		}
		got, err = o.Binding.PutOrder(ctx, s, siteID, body)
		if err == nil {
			break
		}
		// One re-read covers an entry created elsewhere between GET and PUT.
		if errors.Is(err, ErrIncomplete) && attempt == 0 {
			continue
		}
		return nil, err
	}
	return &Created{ID: o.StoredID(siteID), Outputs: outputs(s, in, managed, got)}, nil
}

// ReadOrder refreshes (inputs set) or imports (importing) the singleton. An
// import id is the site's name or id, or the stored id; an import adopts the
// whole live order as the managed list.
func (o *Ordering) ReadOrder(ctx context.Context, s *Singleton, id string, in resource.PropertyMap, importing bool) (*Read, error) {
	siteID, err := o.parseID(id)
	if err != nil {
		if !importing {
			return nil, err
		}
		if siteID, err = o.Binding.SiteID(ctx, id); err != nil {
			return nil, err
		}
	}
	live, err := o.Binding.GetOrder(ctx, s, siteID)
	if errors.Is(err, ErrGone) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	inputs := resource.PropertyMap{}
	if importing {
		inputs[resource.PropertyKey(s.Property)] = strings2(live)
		inputs[PropUnlisted] = resource.NewStringProperty(UnlistedLast)
	} else {
		for k, v := range in {
			inputs[k] = v
		}
		managed, _ := stringList(in[resource.PropertyKey(s.Property)])
		inputs[resource.PropertyKey(s.Property)] = strings2(Project(live, len(managed)))
	}
	proj, _ := stringList(inputs[resource.PropertyKey(s.Property)])
	return &Read{ID: o.StoredID(siteID), Inputs: inputs, Outputs: outputs(s, inputs, proj, live)}, nil
}

// Delete releases the order: no call, the live order stays as it is (there is
// no "unordered" state, and a reset would reorder other people's entries).
func (o *Ordering) Delete(s *Singleton, id string) {
	if o.Warn != nil {
		o.Warn(fmt.Sprintf("%s %s released: the site's order is left as it is", s.Token, id))
	}
}

// Diff is the in-place/replace decision: a changed managed list (element-wise)
// or unlisted is an update; a changed siteId is a replacement.
type Diff struct {
	Changed  []string
	Replaces []string
}

// DiffInputs compares the old and new inputs.
func (o *Ordering) DiffInputs(s *Singleton, olds, news resource.PropertyMap) Diff {
	var d Diff
	if !sameList(olds[resource.PropertyKey(s.Property)], news[resource.PropertyKey(s.Property)]) {
		d.Changed = append(d.Changed, s.Property)
	}
	if unlistedOf(olds) != unlistedOf(news) {
		d.Changed = append(d.Changed, PropUnlisted)
	}
	if o.siteInput(olds) != o.siteInput(news) {
		d.Changed = append(d.Changed, PropSiteID)
		d.Replaces = append(d.Replaces, PropSiteID)
	}
	sort.Strings(d.Changed)
	return d
}

func (o *Ordering) siteInput(in resource.PropertyMap) string {
	if v, ok := in[PropSiteID]; ok {
		if uv := unwrap(v); uv.IsString() {
			return uv.StringValue()
		}
		if v.IsComputed() {
			return "<unknown>"
		}
	}
	return ""
}

func outputs(s *Singleton, in resource.PropertyMap, managed, live []string) resource.PropertyMap {
	return resource.PropertyMap{
		resource.PropertyKey(s.Property): strings2(managed),
		PropUnlisted:                     resource.NewStringProperty(unlistedOf(in)),
		PropEffectiveOrder:               strings2(live),
	}
}

func unlistedOf(in resource.PropertyMap) string {
	if v, ok := in[PropUnlisted]; ok {
		if uv := unwrap(v); uv.IsString() && uv.StringValue() != "" {
			return uv.StringValue()
		}
	}
	return UnlistedLast
}

func unwrap(v resource.PropertyValue) resource.PropertyValue {
	for {
		switch {
		case v.IsSecret():
			v = v.SecretValue().Element
		case v.IsOutput() && v.OutputValue().Known:
			v = v.OutputValue().Element
		default:
			return v
		}
	}
}

func stringList(v resource.PropertyValue) ([]string, error) {
	v = unwrap(v)
	if v.IsNull() {
		return nil, nil
	}
	if !v.IsArray() {
		return nil, fmt.Errorf("want a list of ids")
	}
	var out []string
	for i, e := range v.ArrayValue() {
		e = unwrap(e)
		if !e.IsString() {
			return nil, fmt.Errorf("element %d is not a known id", i)
		}
		out = append(out, e.StringValue())
	}
	return out, nil
}

func sameList(a, b resource.PropertyValue) bool {
	if hasUnknown(a) || hasUnknown(b) {
		return false
	}
	x, errA := stringList(a)
	y, errB := stringList(b)
	if errA != nil || errB != nil || len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func hasUnknown(v resource.PropertyValue) bool {
	v = unwrap(v)
	if v.IsComputed() || v.IsOutput() {
		return true
	}
	if v.IsArray() {
		for _, e := range v.ArrayValue() {
			if hasUnknown(e) {
				return true
			}
		}
	}
	return false
}

func strings2(ids []string) resource.PropertyValue {
	vs := make([]resource.PropertyValue, 0, len(ids))
	for _, id := range ids {
		vs = append(vs, resource.NewStringProperty(id))
	}
	return resource.NewArrayProperty(vs)
}
