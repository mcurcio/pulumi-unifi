package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// variantRequiredPass restores the required inputs that pulschema loses on a
// discriminated variant whose schema is an `allOf` (docs/design/private-binding.md
// §S2.3, probe a2).
//
// A variant schema such as IntegrationMacAclRuleCreateUpdateDto is
//
//	{allOf: [{$ref: <base>}, {properties: {..., networkIdFilter}}],
//	 required: [action, enabled, name, networkIdFilter]}
//
// pulschema merges the allOf members' properties but takes the required list
// only from the shared base, so `networkIdFilter` (required by the spec, and by
// the live server: 400 "networkIdFilter must not be null") ends up optional and a
// program passes preview and fails at `up`.
//
// For every discriminated variant resource the pass reads the variant schema the
// collection POST's discriminator maps to, and adds to requiredInputs every name
// the variant itself requires: its own `required` list plus the `required` lists
// of its inline (non-$ref) allOf members. It never lifts the shared $ref base's
// list (that one pulschema already applied), never lifts the discriminator (it is
// a pinned const), and only lifts names that are input properties of the
// resource. Deterministic: sorted tokens, and requiredInputs stays sorted-unique
// for the names it adds.
func variantRequiredPass(s *GenState) error {
	for _, tok := range sortedKeys(s.Pkg.Resources) {
		m := s.Meta.ResourceCRUDMap[tok]
		if m == nil || m.C == nil {
			continue
		}
		propName, value, ok, err := deriveDiscriminator(s.Doc, tok, *m.C)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		variant, err := variantSchema(s.Doc, *m.C, value)
		if err != nil {
			return fmt.Errorf("variant-required: %s: %w", tok, err)
		}
		res := s.Pkg.Resources[tok]
		have := namesOf(res.RequiredInputs)
		var add []string
		for _, name := range variantRequired(variant) {
			sdk := openapigen.ToSdkName(name)
			if sdk == propName || have[sdk] {
				continue
			}
			if _, isInput := res.InputProperties[sdk]; !isInput {
				continue
			}
			have[sdk] = true
			add = append(add, sdk)
		}
		if len(add) == 0 {
			continue
		}
		sort.Strings(add)
		res.RequiredInputs = append(res.RequiredInputs, add...)
		s.Pkg.Resources[tok] = res
	}
	return nil
}

// variantSchema returns the schema the collection POST's discriminator maps
// value to.
func variantSchema(doc *openapi3.T, collPath, value string) (*openapi3.Schema, error) {
	op := doc.Paths.Find(collPath)
	media := op.Post.RequestBody.Value.Content.Get("application/json")
	body := media.Schema.Value
	ref, ok := body.Discriminator.Mapping[value]
	if !ok {
		return nil, fmt.Errorf("discriminator value %q has no mapping", value)
	}
	// The mapping names a component; the oneOf members carry the resolved
	// schemas under the same ref.
	for _, member := range body.OneOf {
		if member != nil && member.Ref == ref.Ref && member.Value != nil {
			return member.Value, nil
		}
	}
	name := strings.TrimPrefix(ref.Ref, "#/components/schemas/")
	if doc.Components != nil {
		if sr := doc.Components.Schemas[name]; sr != nil && sr.Value != nil {
			return sr.Value, nil
		}
	}
	return nil, fmt.Errorf("discriminator value %q maps to %q, which does not resolve", value, ref.Ref)
}

// variantRequired is the variant's own required names: its `required` list and
// those of its inline allOf members (a $ref member is the shared base).
func variantRequired(v *openapi3.Schema) []string {
	out := append([]string(nil), v.Required...)
	for _, member := range v.AllOf {
		if member == nil || member.Ref != "" || member.Value == nil {
			continue
		}
		out = append(out, member.Value.Required...)
	}
	return out
}

func namesOf(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}
