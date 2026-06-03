package gen

import (
	"fmt"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// replaceOnChangesPass (D-M3.1) marks each resource's immutable/identity input
// properties `replaceOnChanges`, so changing one forces a *replace* rather than an
// in-place update the API would reject (apply fails) or silently drop (state drifts
// from the program) (review 01-H2). pulschema infers none of this — like
// ExcludedPaths, it needs an editorial pass.
//
// In this Pulumi schema version `replaceOnChanges` is a per-property bool on
// PropertySpec (the serializable schema has no resource-level ReplaceOnChanges
// list), so the pass sets it field-by-field — the same shape as
// markSecretFieldsPass.
//
// Derive-by-default, pin-by-exception. An input property is immutable when it is:
//
//  1. spec-readOnly — the OpenAPI create body marks it `readOnly` (an
//     output-y/server-assigned field). The pinned 10.4.57 spec carries no readOnly
//     fields, so this derives nothing today, but the rule is kept so a future spec
//     that adds them is handled with no code change.
//  2. the discriminated-variant's discriminator (type / management). D-M2.1 already
//     pinned it to a single-value Const; marking it replace is harmless (a Const
//     cannot change) and correct (the variant identity is fixed). Identified as the
//     input property D-M2.1 pinned to a Const — rename-independent (this pass runs
//     after token-rename, so the token no longer pascal-cases back to the value).
//  3. an immutableFields pin from mappings.yaml that the resource actually has —
//     spec-implicit immutability a rule cannot see (vlanId: changing a network's
//     VLAN is a replace, not an edit; siteId: a resource-level site-scope override
//     the framework honors over the provider-global one, but the API has no
//     move-to-another-site edit, so changing it recreates the resource — D-M3.2).
//
// Runs late — after naming/types are final — because it only annotates the shipped
// resource set; it adds/renames nothing, so token/type passes need not see its
// edits. A dead immutableFields pin (a name no resource carries) is a loud error,
// so the data cannot silently rot across a spec bump.
//
// Deterministic: each resource's immutable set is the union of its own spec body,
// its own discriminator, and the global pins; the set is sorted before marking, and
// resources are iterated over sorted keys.
func replaceOnChangesPass(s *GenState) error {
	pins := immutableFieldPins()
	pinSeen := make(map[string]bool, len(pins))

	for _, tok := range sortedKeys(s.Pkg.Resources) {
		res := s.Pkg.Resources[tok]
		if len(res.InputProperties) == 0 {
			continue
		}

		immutable := map[string]bool{}

		// (1) spec-readOnly inputs.
		ro, err := readOnlyInputs(s, tok)
		if err != nil {
			return err
		}
		for _, name := range ro {
			immutable[name] = true
		}

		// (2) the variant discriminator (type / management), if any: the input
		// D-M2.1 pinned to a Const.
		for name, ps := range res.InputProperties {
			if ps.Const != nil {
				immutable[name] = true
			}
		}

		// (3) immutableFields pins this resource actually carries (incl. siteId — its
		// replace semantics are pinned via mappings.yaml, D-M3.2).
		for _, name := range pins {
			if _, has := res.InputProperties[name]; has {
				immutable[name] = true
				pinSeen[name] = true
			}
		}

		names := make([]string, 0, len(immutable))
		for name := range immutable {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			ps, ok := res.InputProperties[name]
			if !ok {
				// readOnlyInputs/discriminatorInput only yield names that are input
				// properties, and pins are membership-checked above; a miss is a defect.
				return fmt.Errorf("replace-on-changes: resource %q immutable field %q is not an input property (derivation drift?)", tok, name)
			}
			ps.ReplaceOnChanges = true
			res.InputProperties[name] = ps
		}
		s.Pkg.Resources[tok] = res
	}

	// A pin that matched no resource is a dead entry — fail loud so the data layer
	// cannot silently carry a name the spec no longer has (mirrors the dead-exclusion
	// guard on excludedPaths).
	for _, name := range pins {
		if !pinSeen[name] {
			return fmt.Errorf("replace-on-changes: immutableFields pin %q matched no resource input (dead pin — re-check on spec bump)", name)
		}
	}
	return nil
}

// siteIDInput is the SDK name of the site-scope input every resource carries (the
// {siteId} path parameter). It is a per-resource override of the provider-global
// site (D-M3.2): pinned in mappings.yaml immutableFields (changing it recreates the
// resource) and described via descriptions.inputs.siteId.
const siteIDInput = "siteId"

// readOnlyInputs returns the SDK names of the resource's create-body properties the
// spec marks `readOnly`, restricted to names that are actual input properties of the
// resource. The create body is the entity's POST request body (a $ref to a
// component schema, or a oneOf of such refs for a discriminated entity); readOnly
// fields anywhere in that schema's resolved property set are immutable.
func readOnlyInputs(s *GenState, tok string) ([]string, error) {
	m := s.Meta.ResourceCRUDMap[tok]
	if m == nil || m.C == nil {
		return nil, nil
	}
	body := createBodySchema(s.Doc, *m.C)
	if body == nil {
		return nil, nil
	}

	inputs := s.Pkg.Resources[tok].InputProperties
	seen := map[string]bool{}
	var out []string
	for _, apiName := range readOnlyProps(body) {
		sdk := openapigen.ToSdkName(apiName)
		if seen[sdk] {
			continue
		}
		if _, has := inputs[sdk]; !has {
			continue // not an input on this variant — nothing to mark
		}
		seen[sdk] = true
		out = append(out, sdk)
	}
	sort.Strings(out)
	return out, nil
}

// createBodySchema returns the resolved JSON request-body schema of the collection
// POST at collPath, or nil if there is none.
func createBodySchema(doc *openapi3.T, collPath string) *openapi3.Schema {
	item := doc.Paths.Find(collPath)
	if item == nil || item.Post == nil || item.Post.RequestBody == nil || item.Post.RequestBody.Value == nil {
		return nil
	}
	media := item.Post.RequestBody.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil {
		return nil
	}
	return media.Schema.Value
}

// readOnlyProps collects the API names of every `readOnly` property reachable in a
// (resolved) request-body schema: its own properties plus those of any oneOf/anyOf/
// allOf branch (a discriminated body is a oneOf of variant schemas). Refs are
// followed via the loader-populated .Value. Bounded by the schema graph; a visited
// set guards the (rare) cyclic ref.
func readOnlyProps(sch *openapi3.Schema) []string {
	var out []string
	visited := map[*openapi3.Schema]bool{}
	var walk func(*openapi3.Schema)
	walk = func(sc *openapi3.Schema) {
		if sc == nil || visited[sc] {
			return
		}
		visited[sc] = true
		for name, pref := range sc.Properties {
			if pref != nil && pref.Value != nil && pref.Value.ReadOnly {
				out = append(out, name)
			}
		}
		for _, branch := range [][]*openapi3.SchemaRef{sc.OneOf, sc.AnyOf, sc.AllOf} {
			for _, ref := range branch {
				if ref != nil {
					walk(ref.Value)
				}
			}
		}
	}
	walk(sch)
	return out
}
