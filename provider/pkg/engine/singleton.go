package engine

// The site-keyed singleton with a leadingBlock list (docs/design/private-
// binding.md §S2.5, AclRuleOrdering): one document per site, named by the
// site, whose one field is a list of ids that forms the leading block of the
// site's live order. These are Kind facts (Kind.Shape = singleton,
// Kind.KeyedBySite, Field.Merge = leadingBlock); no kind is special-cased by
// name. The binding reads and writes the document on the kind's item path with
// the site's id; the engine composes and projects the order.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// Unlisted policies (the leadingBlock merge's `unlisted` input).
const (
	UnlistedLast   = "LAST"
	UnlistedReject = "REJECT"
)

// Property names a site-keyed leadingBlock singleton has besides its list
// (provider/pkg/gen/pass_singletons.go generates the same names): the
// unlisted policy, the full live order, and the resource's own site.
const (
	PropUnlisted       = "unlisted"
	PropEffectiveOrder = "effectiveOrder"
	PropSiteID         = "siteId"
)

// Defaults fills the inputs' schema defaults the engine owns (a leadingBlock
// singleton's unlisted policy, LAST), as the framework does for its
// resources. It reports whether it changed anything; the inputs of every
// other kind are returned as they are.
func (e *Engine) Defaults(k *Kind, news resource.PropertyMap) (resource.PropertyMap, bool) {
	if !k.KeyedBySite {
		return news, false
	}
	out := resource.PropertyMap{}
	for key, v := range news {
		out[key] = v
	}
	if v, ok := out[PropUnlisted]; !ok || v.IsNull() {
		out[PropUnlisted] = resource.NewStringProperty(UnlistedLast)
	}
	return out, true
}

// checkSiteKeyed validates the inputs: the managed ids are strings without
// duplicates and without a stored-id prefix (a private kind's object is
// ordered by its official id, its aclRuleId output), and unlisted is LAST or
// REJECT. Unknown values (entries not created yet) pass.
func checkSiteKeyed(k *Kind, news resource.PropertyMap) []Failure {
	lb := k.LeadingBlock()
	var fails []Failure
	add := func(p, f string, a ...any) { fails = append(fails, Failure{Property: p, Reason: fmt.Sprintf(f, a...)}) }
	prop := lb.Property
	v, ok := news[resource.PropertyKey(prop)]
	switch {
	case !ok || v.IsNull():
		add(prop, "%s is required", prop)
	case v.IsComputed() || v.IsOutput() && !v.OutputValue().Known:
	case !unwrapPV(v).IsArray():
		add(prop, "%s must be a list of ids", prop)
	default:
		seen := map[string]bool{}
		for i, el := range unwrapPV(v).ArrayValue() {
			el = unwrapPV(el)
			if el.IsComputed() || el.IsOutput() && !el.OutputValue().Known {
				continue
			}
			if !el.IsString() || el.StringValue() == "" {
				add(prop, "%s[%d] must be a non-empty id", prop, i)
				continue
			}
			id := el.StringValue()
			for _, a := range storedAPIs {
				if strings.HasPrefix(id, a+":") {
					add(prop, "%s[%d] %q is a stored resource id, not an entry id; for a private kind pass its aclRuleId output", prop, i, id)
				}
			}
			if seen[id] {
				add(prop, "%s lists %q twice", prop, id)
			}
			seen[id] = true
		}
	}
	if u, ok := news[PropUnlisted]; ok && !u.IsNull() && !u.IsComputed() {
		if uv := unwrapPV(u); !uv.IsString() || (uv.StringValue() != UnlistedLast && uv.StringValue() != UnlistedReject) {
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
// managed ones are reordered, or when a managed one is gone. With REJECT,
// readSite projects the whole live order, so any unlisted entry is drift.
func Project(live []string, n int) []string {
	if n > len(live) {
		n = len(live)
	}
	return append([]string{}, live[:n]...)
}

// siteOf is the site a resource names: its siteId input, else the provider's.
func (e *Engine) siteOf(in resource.PropertyMap) string {
	if v, ok := in[PropSiteID]; ok {
		if uv := unwrapPV(v); uv.IsString() && uv.StringValue() != "" {
			return uv.StringValue()
		}
	}
	if e.Site != nil && e.Site() != "" {
		return e.Site()
	}
	return "default"
}

// adoptSite is Create: nothing can be created, so it reads the site's
// document, composes and PUTs. It never fails because entries exist.
func (e *Engine) adoptSite(ctx context.Context, k *Kind, in resource.PropertyMap) (string, resource.PropertyMap, error) {
	siteID, err := e.bind(k).SiteID(ctx, e.siteOf(in))
	if err != nil {
		return "", nil, err
	}
	return e.applySite(ctx, k, siteID, in)
}

// updateSite composes and PUTs onto a fresh read.
func (e *Engine) updateSite(ctx context.Context, k *Kind, id string, in resource.PropertyMap) (resource.PropertyMap, error) {
	siteID, err := e.ParseID(k, id)
	if err != nil {
		return nil, err
	}
	_, out, err := e.applySite(ctx, k, siteID, in)
	return out, err
}

func (e *Engine) applySite(ctx context.Context, k *Kind, siteID string, in resource.PropertyMap) (string, resource.PropertyMap, error) {
	lb := k.LeadingBlock()
	managed, err := stringList(in[resource.PropertyKey(lb.Property)])
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", lb.Property, err)
	}
	unlisted := unlistedOf(in)
	var got []string
	for attempt := 0; ; attempt++ {
		live, err := e.liveOrder(ctx, k, siteID)
		if err != nil {
			return "", nil, err
		}
		body, err := Compose(managed, live, unlisted)
		if err != nil {
			return "", nil, err
		}
		if body == nil {
			body = []string{}
		}
		resp, err := e.bind(k).Update(ctx, k, siteID, map[string]any{lb.Wire: body})
		if err == nil {
			if got, err = idsIn(k, firstObject(resp)); err != nil || got == nil {
				got = body // the answer does not carry the order: what was sent
			}
			break
		}
		// One re-read covers an entry created elsewhere between GET and PUT.
		if errors.Is(err, ErrIncomplete) && attempt == 0 {
			continue
		}
		return "", nil, err
	}
	return e.StoredID(k, siteID), siteOutputs(k, in, managed, got), nil
}

// liveOrder reads the site's live order; a site that is gone is ErrNotFound.
func (e *Engine) liveOrder(ctx context.Context, k *Kind, siteID string) ([]string, error) {
	doc, found, err := e.bind(k).Get(ctx, k, siteID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%s of site %s: %w", k.Token, siteID, ErrNotFound)
	}
	ids, err := idsIn(k, doc)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		return nil, fmt.Errorf("%s of site %s: the answer has no %s", k.Token, siteID, k.LeadingBlock().Wire)
	}
	return ids, nil
}

// idsIn reads the leadingBlock list from a document; nil when it is absent.
func idsIn(k *Kind, doc map[string]any) ([]string, error) {
	lb := k.LeadingBlock()
	raw, ok := doc[lb.Wire]
	if !ok {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s is not a list", k.Token, lb.Wire)
	}
	out := make([]string, 0, len(list))
	for i, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s[%d] is not an id", k.Token, lb.Wire, i)
		}
		out = append(out, s)
	}
	return out, nil
}

// readSite refreshes (inputs set) or imports (importing) the singleton. An
// import id is the site's name or id, or the stored id; an import adopts the
// whole live order as the managed list.
func (e *Engine) readSite(ctx context.Context, k *Kind, id string, in resource.PropertyMap, importing bool) (*ReadResult, error) {
	lb := k.LeadingBlock()
	key := resource.PropertyKey(lb.Property)
	siteID, err := e.ParseID(k, id)
	if err != nil {
		if !importing {
			return nil, err
		}
		if siteID, err = e.bind(k).SiteID(ctx, id); err != nil {
			return nil, err
		}
	}
	live, err := e.liveOrder(ctx, k, siteID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	inputs := resource.PropertyMap{}
	if importing {
		inputs[key] = idList(live)
		inputs[PropUnlisted] = resource.NewStringProperty(UnlistedLast)
	} else {
		for name, v := range in {
			inputs[name] = v
		}
		managed, _ := stringList(in[key])
		n := len(managed)
		// With REJECT the program claims the whole order, so the projection
		// is the whole live order: an unlisted entry anywhere is drift, and
		// the next update fails in Compose naming it.
		if unlistedOf(in) == UnlistedReject {
			n = len(live)
		}
		inputs[key] = idList(Project(live, n))
	}
	proj, _ := stringList(inputs[key])
	return &ReadResult{ID: e.StoredID(k, siteID), Inputs: inputs, Outputs: siteOutputs(k, inputs, proj, live)}, nil
}

// diffSiteKeyed is the in-place/replace decision: a changed managed list
// (element-wise) or unlisted is an update; a changed siteId is a replacement.
func diffSiteKeyed(k *Kind, olds, news resource.PropertyMap) DiffResult {
	prop := k.LeadingBlock().Property
	d := DiffResult{Detailed: map[string]string{}}
	if !sameList(olds[resource.PropertyKey(prop)], news[resource.PropertyKey(prop)]) {
		d.Changed = append(d.Changed, prop)
	}
	if unlistedOf(olds) != unlistedOf(news) {
		d.Changed = append(d.Changed, PropUnlisted)
	}
	if siteInput(olds) != siteInput(news) {
		d.Changed = append(d.Changed, PropSiteID)
		d.Replaces = append(d.Replaces, PropSiteID)
	}
	for _, p := range d.Changed {
		d.Detailed[p] = "update"
	}
	sort.Strings(d.Changed)
	return d
}

func siteInput(in resource.PropertyMap) string {
	if v, ok := in[PropSiteID]; ok {
		if uv := unwrapPV(v); uv.IsString() {
			return uv.StringValue()
		}
		if v.IsComputed() {
			return "<unknown>"
		}
	}
	return ""
}

func siteOutputs(k *Kind, in resource.PropertyMap, managed, live []string) resource.PropertyMap {
	return resource.PropertyMap{
		resource.PropertyKey(k.LeadingBlock().Property): idList(managed),
		PropUnlisted:       resource.NewStringProperty(unlistedOf(in)),
		PropEffectiveOrder: idList(live),
	}
}

func unlistedOf(in resource.PropertyMap) string {
	if v, ok := in[PropUnlisted]; ok {
		if uv := unwrapPV(v); uv.IsString() && uv.StringValue() != "" {
			return uv.StringValue()
		}
	}
	return UnlistedLast
}

// unwrapPV removes secret and known-output wrappers.
func unwrapPV(v resource.PropertyValue) resource.PropertyValue {
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
	v = unwrapPV(v)
	if v.IsNull() {
		return nil, nil
	}
	if !v.IsArray() {
		return nil, fmt.Errorf("want a list of ids")
	}
	var out []string
	for i, el := range v.ArrayValue() {
		el = unwrapPV(el)
		if !el.IsString() {
			return nil, fmt.Errorf("element %d is not a known id", i)
		}
		out = append(out, el.StringValue())
	}
	return out, nil
}

func sameList(a, b resource.PropertyValue) bool {
	if unknownPV(a) || unknownPV(b) {
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

func unknownPV(v resource.PropertyValue) bool {
	v = unwrapPV(v)
	if v.IsComputed() || v.IsOutput() {
		return true
	}
	if v.IsArray() {
		for _, el := range v.ArrayValue() {
			if unknownPV(el) {
				return true
			}
		}
	}
	return false
}

func idList(ids []string) resource.PropertyValue {
	vs := make([]resource.PropertyValue, 0, len(ids))
	for _, id := range ids {
		vs = append(vs, resource.NewStringProperty(id))
	}
	return resource.NewArrayProperty(vs)
}
