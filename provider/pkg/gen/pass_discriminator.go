package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// discriminatorInjectPass removes the redundant, unguided discriminator input
// (F1 / 01-C1 / 03-F1) from every split-variant resource.
//
// pulschema splits each oneOf+discriminator request body into one resource per
// variant, naming each token ToPascalCase(discriminatorValue) — so A_RECORD
// becomes ARecord, STANDARD becomes Standard, GATEWAY becomes Gateway, and so on
// (pulschema openapi.go: `discriminatedResourceName := ToPascalCase(value)`).
// But the split leaves the discriminator property itself (type / management) on
// each variant as a *required, free-form, un-enumerated string*: the consumer
// already chose ARecord, yet must also type type="A_RECORD" — a magic string
// with no enum, default, or IDE hint, validated only at apply time.
//
// This pass reverses pulschema's own derivation to recover, per variant, exactly
// which discriminator value the framework must inject. For each resource it:
//
//  1. finds the resource's create (collection POST) path from the crudMap,
//  2. reads that POST body's discriminator (propertyName + mapping),
//  3. matches the variant whose ToPascalCase(value) equals the resource's token
//     short name — the inverse of pulschema's naming — to get (propertyName,
//     value), and
//  4. pins that input property to Const+Default = value and drops it from
//     requiredInputs, so the value is fixed and the framework injects it on the
//     wire (no consumer input).
//
// The derivation is entirely spec-driven: nothing is hardcoded. A resource whose
// POST body carries no discriminator (the flat resources — FirewallZone,
// FirewallPolicy, Voucher, AdoptDevice) is skipped untouched. If a variant token
// cannot be matched back to a mapping value, that is a spec/derivation drift and
// the pass errors loudly rather than silently leaving a required magic string.
//
// Deterministic: each resource's edit depends only on its own crudMap C path and
// the spec's discriminator mapping, independent of map iteration order. Keys are
// iterated sorted regardless.
func discriminatorInjectPass(s *GenState) error {
	crudMap := s.Meta.ResourceCRUDMap
	doc := s.Doc
	pkg := s.Pkg

	for _, tok := range sortedKeys(pkg.Resources) {
		m := crudMap[tok]
		if m == nil || m.C == nil {
			continue // no create path → cannot resolve the request-body discriminator
		}

		propName, value, ok, err := deriveDiscriminator(doc, tok, *m.C)
		if err != nil {
			return err
		}
		if !ok {
			continue // flat (non-discriminated) resource — nothing to inject
		}

		res := pkg.Resources[tok]
		ps, present := res.InputProperties[propName]
		if !present {
			return fmt.Errorf("discriminator: resource %q has discriminator %q=%q in its create body but no matching input property (spec/token drift?)", tok, propName, value)
		}

		// Pin to a single fixed value: Const makes it a literal of that value,
		// Default supplies it so the framework injects it on the wire. Re-assert
		// the plain string type.
		ps.Const = value
		ps.Default = value
		ps.TypeSpec = pschema.TypeSpec{Type: "string"}
		res.InputProperties[propName] = ps

		res.RequiredInputs = removeString(res.RequiredInputs, propName)
		pkg.Resources[tok] = res
	}

	return nil
}

// deriveDiscriminator resolves, for a resource token and its create (collection
// POST) path, the discriminator (propertyName, value) the resource represents —
// the inverse of pulschema's ToPascalCase(value) token naming. ok is false when
// the POST body has no discriminator (a flat resource).
func deriveDiscriminator(doc *openapi3.T, tok, collPath string) (propName, value string, ok bool, err error) {
	op := doc.Paths.Find(collPath)
	if op == nil || op.Post == nil || op.Post.RequestBody == nil || op.Post.RequestBody.Value == nil {
		return "", "", false, nil
	}
	media := op.Post.RequestBody.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		return "", "", false, nil
	}
	disc := media.Schema.Value.Discriminator
	if disc == nil || disc.PropertyName == "" || len(disc.Mapping) == 0 {
		return "", "", false, nil
	}

	short := tokenShortName(tok)
	// pulschema names the SDK property via ToSdkName(propertyName); the resource's
	// input property key is therefore the SDK form, not the raw API name.
	propName = openapigen.ToSdkName(disc.PropertyName)

	var matched []string
	for discValue := range disc.Mapping {
		if openapigen.ToPascalCase(discValue) == short {
			matched = append(matched, discValue)
		}
	}
	switch len(matched) {
	case 0:
		return "", "", false, fmt.Errorf("discriminator: resource %q (create %s) has discriminator %q but no mapping value pascal-cases to the token name %q; mapping=%v (spec/derivation drift?)",
			tok, collPath, disc.PropertyName, short, sortedMappingValues(disc.Mapping))
	case 1:
		return propName, matched[0], true, nil
	default:
		return "", "", false, fmt.Errorf("discriminator: resource %q matches multiple mapping values %v for token %q — ambiguous", tok, matched, short)
	}
}

// sortedMappingValues returns a discriminator mapping's keys (the discriminator
// values) sorted, for deterministic error messages.
func sortedMappingValues(mapping openapi3.StringMap[openapi3.MappingRef]) []string {
	out := make([]string, 0, len(mapping))
	for v := range mapping {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// tokenShortName returns the segment after the last ":" of a Pulumi token
// (e.g. "unifi:sites/v1:ARecord" -> "ARecord").
func tokenShortName(tok string) string {
	if i := strings.LastIndex(tok, ":"); i >= 0 {
		return tok[i+1:]
	}
	return tok
}

// removeString returns ss without the first occurrence of v, preserving order.
// Used to drop the injected discriminator from a resource's requiredInputs.
func removeString(ss []string, v string) []string {
	for i, s := range ss {
		if s == v {
			return append(ss[:i:i], ss[i+1:]...)
		}
	}
	return ss
}
