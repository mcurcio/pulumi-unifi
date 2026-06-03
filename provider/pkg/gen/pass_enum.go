package gen

import (
	"fmt"
	"sort"
	"strings"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
)

// enumCleanupPass (D-M2.7) does two scoped cleanups on the final Pulumi Types:
//
//  1. Dedup structurally-identical enums (03-F11). pulschema emits one enum type
//     per operation/variant the spec duplicates, so the same closed value set
//     surfaces under several tokens — `ACLRuleAction` / `ACLRuleObjectAction` /
//     `ACLRuleUpdateAction` (all ["ALLOW","BLOCK"]), `ConnectionStateFilterItem`
//     vs `FirewallPolicyConnectionStateFilterItem`, `IpsecFilter` vs
//     `FirewallPolicyIpsecFilter`, … This bloats the `_enums` surface and makes
//     imports ambiguous (which `…Action` for an update?). The pass merges every
//     family of enums that share the SAME module, SAME underlying type, and SAME
//     value set into one canonical type and rewrites every `#/types/...` ref that
//     pointed at a merged duplicate (in resources, functions, and other types) to
//     the canonical token, in lockstep.
//
//     Module-scoped on purpose: a token's module is part of its public identity,
//     so an enum is only merged with siblings in its own module (a
//     `pending-devices/v1` consumer is never handed a `sites/v1` type). The
//     canonical NAME is editorial — the engine derives a sensible default (the
//     shortest member's short name; ties broken alphabetically) and mappings.yaml
//     pins the exceptions where that default is too generic (enums.canonicalRename,
//     keyed by the derived default name). Upstream root cause is per-operation
//     schema duplication (deferred — see Track G).
//
//  2. Prune unreferenced empty types (01-L1). The low-quality spec leaves
//     type-less object schemas that pulschema emits as empty `type: object` types
//     (no properties, no enum). Where such a type is referenced by nothing
//     (resource, function, or other type), it is pure SDK noise and is dropped. A
//     referenced empty type is NEVER dropped — emptiness is a spec artifact, not a
//     licence to break a live ref.
//
// EXPLICITLY out of scope: numeric-enum preservation (broadcastingFrequenciesGHz
// etc., 03-F9) — recovering the numeric value sets the sanitizer strips is
// deferred upstream to pulschema (G-U3); this pass does not touch them.
//
// Deterministic: families are grouped by a stable signature and the canonical is
// chosen by a total order over short names; every ref rewrite and prune is applied
// over sorted keys, independent of map iteration order.
func enumCleanupPass(s *GenState) error {
	if err := dedupEnums(s); err != nil {
		return err
	}
	pruneUnreferencedEmptyTypes(s)
	return nil
}

// enumSignature identifies a dedup family: same module, same underlying type, same
// ordered value set. Two enums with this signature are interchangeable.
type enumSignature struct {
	module     string
	underlying string
	values     string // newline-joined sorted values — a comparable map key
}

// dedupEnums merges structurally-identical enum types (within a module) into one
// canonical token and rewrites every ref to the merged duplicates.
func dedupEnums(s *GenState) error {
	// Group enum tokens by signature.
	families := map[enumSignature][]string{}
	for tok, ct := range s.Pkg.Types {
		if !isEnumType(ct) {
			continue
		}
		sig := enumSignature{
			module:     tokenModule(tok),
			underlying: ct.Type,
			values:     enumValueKey(ct),
		}
		families[sig] = append(families[sig], tok)
	}

	// Build the rename map (every non-canonical member → its canonical) and, for any
	// canonical that is a pinned NEW name (not an existing member), materialize it by
	// copying the family's representative body to the new token.
	renames := map[string]string{}
	for _, sig := range sortedEnumSignatures(families) {
		members := families[sig]
		if len(members) < 2 {
			continue // a unique enum is already canonical
		}
		canonical, source, err := canonicalEnumToken(members)
		if err != nil {
			return err
		}
		if _, exists := s.Pkg.Types[canonical]; !exists {
			// Pinned rename to a fresh token: give it the family's enum body so refs
			// rewritten to it resolve. (members all share a value set by construction.)
			s.Pkg.Types[canonical] = s.Pkg.Types[source]
		}
		for _, m := range members {
			if m != canonical {
				renames[m] = canonical
			}
		}
	}
	if len(renames) == 0 {
		return nil
	}

	// Drop the merged duplicates (the canonical now carries the value set), then
	// rewrite every ref pointing at them. A canonical is never a rename source (one
	// canonical per family), so deleting the sources cannot remove a target.
	for src := range renames {
		delete(s.Pkg.Types, src)
	}
	rewriteTypeRefs(s, renames)
	return nil
}

// canonicalEnumToken picks the canonical token for a dedup family and the source
// member whose body represents it. The engine's default is the member with the
// shortest short name (ties broken alphabetically); an enums.canonicalRename pin
// in mappings.yaml (keyed by that derived default short name) overrides the NAME,
// kept in the family's module. When the pin names a token that is not itself a
// member, the caller materializes it from source (the returned default member).
func canonicalEnumToken(members []string) (canonical, source string, err error) {
	sorted := append([]string(nil), members...)
	sort.Slice(sorted, func(i, j int) bool {
		si, sj := tokenShortName(sorted[i]), tokenShortName(sorted[j])
		if len(si) != len(sj) {
			return len(si) < len(sj)
		}
		return si < sj
	})
	defaultTok := sorted[0]
	defaultShort := tokenShortName(defaultTok)

	pinned, ok := enumCanonicalRename(defaultShort)
	if !ok {
		return defaultTok, defaultTok, nil
	}
	// Re-token the pin into the family's module so it stays in-module.
	mod, _ := splitToken(defaultTok)
	canonical = mod + pinned
	// A pin that lands on a DIFFERENT existing member would silently drop that
	// member's identity into the rename churn; require the pin to name either a fresh
	// token or the default itself, so the intent is unambiguous.
	for _, m := range members {
		if m == canonical && m != defaultTok {
			return "", "", fmt.Errorf("enum-cleanup: canonicalRename %q→%q collides with existing family member %q (pin a fresh name or the default)", defaultShort, pinned, canonical)
		}
	}
	return canonical, defaultTok, nil
}

// pruneUnreferencedEmptyTypes drops Types entries that are empty objects (no
// properties, no enum — the type-less spec artifacts) and are referenced by
// nothing. A referenced empty type is never dropped.
func pruneUnreferencedEmptyTypes(s *GenState) {
	// Iterate to a fixpoint: pruning one empty type can make another (that only it
	// referenced) unreferenced. Bounded by the type count; deterministic.
	for {
		referenced := referencedTypeTokens(s)
		var prune []string
		for _, tok := range sortedKeys(s.Pkg.Types) {
			if referenced[tok] {
				continue
			}
			if isEmptyType(s.Pkg.Types[tok]) {
				prune = append(prune, tok)
			}
		}
		if len(prune) == 0 {
			return
		}
		for _, tok := range prune {
			delete(s.Pkg.Types, tok)
		}
	}
}

// isEnumType reports whether a complex type is an enum (has enum values).
func isEnumType(ct pschema.ComplexTypeSpec) bool {
	return len(ct.Enum) > 0
}

// isEmptyType reports whether a complex type is an empty object: no properties and
// no enum values. (Its Type is the type-less spec artifact's "object".)
func isEmptyType(ct pschema.ComplexTypeSpec) bool {
	return len(ct.Properties) == 0 && len(ct.Enum) == 0
}

// enumValueKey returns a stable, comparable key for an enum's value set: the
// stringified values, sorted and newline-joined. Two enums with the same key have
// the same value set regardless of source ordering.
func enumValueKey(ct pschema.ComplexTypeSpec) string {
	vals := make([]string, 0, len(ct.Enum))
	for _, e := range ct.Enum {
		vals = append(vals, fmt.Sprintf("%v", e.Value))
	}
	sort.Strings(vals)
	return strings.Join(vals, "\n")
}

// tokenModule returns the module segment of a token: "unifi:sites/v1:State" →
// "sites/v1". "" if the token is not of the expected three-part shape.
func tokenModule(tok string) string {
	parts := strings.Split(tok, ":")
	if len(parts) >= 3 {
		return parts[1]
	}
	return ""
}

// sortedEnumSignatures returns the family signatures in a deterministic order so
// the rename map is built independent of map iteration order.
func sortedEnumSignatures(families map[enumSignature][]string) []enumSignature {
	sigs := make([]enumSignature, 0, len(families))
	for sig := range families {
		sigs = append(sigs, sig)
	}
	sort.Slice(sigs, func(i, j int) bool {
		a, b := sigs[i], sigs[j]
		if a.module != b.module {
			return a.module < b.module
		}
		if a.underlying != b.underlying {
			return a.underlying < b.underlying
		}
		return a.values < b.values
	})
	return sigs
}

// referencedTypeTokens returns the set of in-document type tokens reachable by a
// `#/types/...` ref from any resource, function, or other type.
func referencedTypeTokens(s *GenState) map[string]bool {
	refs := map[string]bool{}
	collect := func(ts *pschema.TypeSpec) {
		walkTypeSpec(ts, func(t *pschema.TypeSpec) {
			if tok := refTypeToken(t.Ref); tok != "" {
				refs[tok] = true
			}
		})
	}
	forEachTypeSpec(s, collect)
	return refs
}

// rewriteTypeRefs rewrites every `#/types/<old>` ref to `#/types/<new>` across the
// whole schema (resources, functions, and types), following the rename map. A
// chain is collapsed in one hop because the rename map never maps a canonical to
// another token.
func rewriteTypeRefs(s *GenState, renames map[string]string) {
	const prefix = "#/types/"
	apply := func(ts *pschema.TypeSpec) {
		walkTypeSpec(ts, func(t *pschema.TypeSpec) {
			tok := refTypeToken(t.Ref)
			if tok == "" {
				return
			}
			if to, ok := renames[tok]; ok {
				t.Ref = prefix + to
			}
		})
	}
	forEachTypeSpec(s, apply)
}

// refTypeToken returns the in-document type token a ref points at, or "" if the
// ref is not an in-document `#/types/...` ref.
func refTypeToken(ref string) string {
	const prefix = "#/types/"
	if tok, ok := strings.CutPrefix(ref, prefix); ok {
		return tok
	}
	return ""
}

// forEachTypeSpec invokes fn on every top-level TypeSpec-bearing slot in the
// schema (each property's inline TypeSpec, each function's return type). fn is
// responsible for recursing into nested Items/AdditionalProperties/OneOf via
// walkTypeSpec. The traversal covers resources (outputs + inputs), functions
// (inputs, outputs, return type), and other types' properties — the complete set
// of places a type ref can appear.
func forEachTypeSpec(s *GenState, fn func(*pschema.TypeSpec)) {
	visitProps := func(props map[string]pschema.PropertySpec) {
		for name, ps := range props {
			fn(&ps.TypeSpec)
			props[name] = ps
		}
	}
	visitObject := func(o *pschema.ObjectTypeSpec) {
		if o != nil {
			visitProps(o.Properties)
		}
	}

	for tok, r := range s.Pkg.Resources {
		visitProps(r.Properties)
		visitProps(r.InputProperties)
		s.Pkg.Resources[tok] = r
	}
	for tok, f := range s.Pkg.Functions {
		visitObject(f.Inputs)
		visitObject(f.Outputs)
		if f.ReturnType != nil {
			if f.ReturnType.TypeSpec != nil {
				fn(f.ReturnType.TypeSpec)
			}
			visitObject(f.ReturnType.ObjectTypeSpec)
		}
		s.Pkg.Functions[tok] = f
	}
	for tok, ct := range s.Pkg.Types {
		visitProps(ct.Properties)
		s.Pkg.Types[tok] = ct
	}
}

// walkTypeSpec invokes fn on ts and every nested TypeSpec it contains
// (Items, AdditionalProperties, OneOf), so a ref buried in an array element or a
// map value type is rewritten/collected too. fn mutates in place via the pointer.
func walkTypeSpec(ts *pschema.TypeSpec, fn func(*pschema.TypeSpec)) {
	if ts == nil {
		return
	}
	fn(ts)
	walkTypeSpec(ts.Items, fn)
	walkTypeSpec(ts.AdditionalProperties, fn)
	for i := range ts.OneOf {
		walkTypeSpec(&ts.OneOf[i], fn)
	}
}
