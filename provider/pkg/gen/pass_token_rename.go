package gen

import (
	"fmt"
	"regexp"
	"strings"
)

// tokenRenamePass is the single naming pass (D-M2.2 + D-M2.3 — naming is one
// responsibility, so one file): it makes the public token surface idiomatic.
//
//   - Resources (D-M2.2): the context-free per-variant tokens pulschema emits
//     (bare Standard, Mac, Ipv4, Gateway, ARecord …) are prefixed with their
//     parent entity so they carry context: WifiBroadcastStandard, DnsARecord,
//     ManagedNetworkGateway, TrafficMatchIpv4Addresses. The prefix is keyed off
//     the resource's create (collection) path — the stable, spec-derived entity
//     identity — via the data-driven entityPrefixes table in mappings.yaml (the
//     editorial layer is DATA, not Go; see MAPPING-LAYER.md). A resource is
//     prefixed only when its collection POST actually carries a discriminator, so
//     a flat resource that happens to share a collection path is never
//     mis-prefixed; an unmapped discriminated entity fails loud.
//
//   - Functions (D-M2.3): the operationId-derived function tokens are full of
//     upstream garbage — internal DTO names (getIntegrationDnsARecordDto),
//     snake_case bleed (getGateway_managed_network_details), broken
//     singularization (getCountrie, getFirewallPolicie), and get/list verb
//     confusion (listTrafficMatching is actually a get-one). normalizeFunction
//     cleans these deterministically by string transform: strip Integration/Dto,
//     PascalCase snake fragments (normalizing acronyms), fix irregular plurals
//     and apply the explicit get/list settlements — all pinned in mappings.yaml.
//     The `*Page` list tokens are intentionally left to the de-page pass (D-M2.5).
//
// Renaming a token rewrites every token-keyed map in lockstep (Pkg.Resources /
// Pkg.Functions, Meta.ResourceCRUDMap, Meta.AutoNameMap); nothing in the schema
// references a resource/function token by $ref, so the rewrite is local to those
// maps. A rename that would collide with an existing token is a loud error.
//
// Deterministic: every rename is a pure function of the token (and its spec
// collection path), applied over sorted keys; the output token set is
// independent of map iteration order.
func tokenRenamePass(s *GenState) error {
	if err := renameResources(s); err != nil {
		return err
	}
	return renameFunctions(s)
}

// The resource-naming editorial table (entity prefixes per discriminated
// collection path) lives in mappings.yaml and is read via entityPrefix() — the
// engine derives by default, the data file pins by exception. See
// docs/reviews/MAPPING-LAYER.md.

// renameResources entity-prefixes the discriminated-variant resource tokens.
//
// Derive-by-default, pin-by-exception with a LOUD failure on the gap: a resource
// whose create body carries a discriminator (pulschema split it into bare,
// context-free per-variant tokens like Standard/Mac) but whose collection path
// has no entityPrefix mapping is an *unmapped entity* — its public token name
// would be an un-pinned, context-free, collision-prone string. Rather than ship
// that silently, the pass errors so a spec bump that introduces a new
// discriminated entity forces a deliberate mappings.yaml entry. A flat
// (non-discriminated) resource needs no prefix and is left as-is.
func renameResources(s *GenState) error {
	crudMap := s.Meta.ResourceCRUDMap
	doc := s.Doc

	for _, tok := range sortedKeys(s.Pkg.Resources) {
		m := crudMap[tok]
		if m == nil || m.C == nil {
			continue
		}
		_, _, isDisc, err := deriveDiscriminator(doc, tok, *m.C)
		if err != nil {
			return err
		}

		prefix, ok := entityPrefix(*m.C)
		if !ok {
			if isDisc {
				// Unmapped discriminated entity: bare per-variant token would ship
				// un-pinned. Fail loud (MAPPING-LAYER.md acceptance criterion).
				return fmt.Errorf("token-rename: unmapped entity — resource %q's create collection %q carries a discriminator (per-variant split) but has no entityPrefix in mappings.yaml; add one so its public token name is pinned", tok, *m.C)
			}
			continue // flat resource — no prefix needed
		}
		// Guard: a prefix entry must point at a genuinely discriminated entity. If
		// this collection's POST body has no discriminator, the table is stale.
		if !isDisc {
			return fmt.Errorf("token-rename: mappings.yaml entityPrefix %q for collection %q but resource %q's create body carries no discriminator (stale prefix entry?)", prefix, *m.C, tok)
		}

		mod, short := splitToken(tok)
		newTok := mod + prefix + short
		if newTok == tok {
			continue
		}
		if err := renameToken(s, tok, newTok, true); err != nil {
			return err
		}
	}
	return nil
}

// renameFunctions normalizes the function tokens (strip DTO noise, fix casing,
// singulars, and the get/list near-duplicates).
func renameFunctions(s *GenState) error {
	for _, tok := range sortedKeys(s.Pkg.Functions) {
		mod, short := splitToken(tok)
		newShort := normalizeFunction(short)
		if newShort == short {
			continue
		}
		if err := renameToken(s, tok, mod+newShort, false); err != nil {
			return err
		}
	}
	return nil
}

// The function-name editorial tables (acronym fixups, irregular singulars, and
// the explicit get/list near-duplicate settlements) all live in mappings.yaml
// and are read via the accessors in mappings.go — the engine derives the bulk
// mechanically (strip Integration/Dto, PascalCase snake fragments), the data
// file pins only what a rule cannot get right. See docs/reviews/MAPPING-LAYER.md.

var funcVerbRE = regexp.MustCompile(`^(get|list)(.*)$`)

// normalizeFunction cleans one function token's short name. See tokenRenamePass.
func normalizeFunction(short string) string {
	if repl, ok := explicitFunctionRename(short); ok {
		return repl
	}
	m := funcVerbRE.FindStringSubmatch(short)
	if m == nil {
		return short
	}
	verb, rest := m[1], m[2]

	rest = strings.TrimPrefix(rest, "Integration")
	rest = strings.TrimSuffix(rest, "Dto")

	if strings.Contains(rest, "_") {
		rest = pascalizeSnake(rest)
	}

	rest = fixIrregularSingular(rest)

	return verb + rest
}

// pascalizeSnake PascalCases an underscore-separated fragment, normalizing
// all-caps acronyms (VPN → Vpn) and down-casing the tail of other all-caps words.
func pascalizeSnake(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if fixed, ok := acronymFixup(strings.ToUpper(p)); ok {
			b.WriteString(fixed)
			continue
		}
		if p == strings.ToUpper(p) {
			// e.g. a stray all-caps fragment: keep first upper, lower the rest.
			b.WriteString(strings.ToUpper(p[:1]) + strings.ToLower(p[1:]))
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

// fixIrregularSingular repairs a broken-singular suffix from the exceptions
// table in mappings.yaml (Countrie → Country, Policie → Policy, Categorie →
// Category). Iterated over sorted keys for determinism.
func fixIrregularSingular(s string) string {
	singulars := irregularSingularMap()
	for _, bad := range sortedKeys(singulars) {
		if strings.HasSuffix(s, bad) {
			return strings.TrimSuffix(s, bad) + singulars[bad]
		}
	}
	return s
}

// splitToken splits a Pulumi token into its module prefix (everything up to and
// including the final ":") and short name. "unifi:sites/v1:ARecord" →
// ("unifi:sites/v1:", "ARecord").
func splitToken(tok string) (modPrefix, short string) {
	if i := strings.LastIndex(tok, ":"); i >= 0 {
		return tok[:i+1], tok[i+1:]
	}
	return "", tok
}

// renameToken moves a single token from old to new across every token-keyed map:
// the schema's Resources/Functions and the metadata's ResourceCRUDMap and
// AutoNameMap. isResource selects the schema map. A collision with an existing
// token is a loud error (two schema entries cannot share one SDK class name).
func renameToken(s *GenState, oldTok, newTok string, isResource bool) error {
	if oldTok == newTok {
		return nil
	}

	if isResource {
		if _, exists := s.Pkg.Resources[newTok]; exists {
			return fmt.Errorf("token-rename: resource rename %q → %q collides with an existing resource", oldTok, newTok)
		}
		r, ok := s.Pkg.Resources[oldTok]
		if !ok {
			return fmt.Errorf("token-rename: resource %q not found", oldTok)
		}
		s.Pkg.Resources[newTok] = r
		delete(s.Pkg.Resources, oldTok)
	} else {
		if _, exists := s.Pkg.Functions[newTok]; exists {
			return fmt.Errorf("token-rename: function rename %q → %q collides with an existing function", oldTok, newTok)
		}
		f, ok := s.Pkg.Functions[oldTok]
		if !ok {
			return fmt.Errorf("token-rename: function %q not found", oldTok)
		}
		s.Pkg.Functions[newTok] = f
		delete(s.Pkg.Functions, oldTok)
	}

	if m, ok := s.Meta.ResourceCRUDMap[oldTok]; ok {
		if _, exists := s.Meta.ResourceCRUDMap[newTok]; exists {
			return fmt.Errorf("token-rename: crudMap rename %q → %q collides", oldTok, newTok)
		}
		s.Meta.ResourceCRUDMap[newTok] = m
		delete(s.Meta.ResourceCRUDMap, oldTok)
	}

	if v, ok := s.Meta.AutoNameMap[oldTok]; ok {
		if _, exists := s.Meta.AutoNameMap[newTok]; exists {
			return fmt.Errorf("token-rename: autoNameMap rename %q → %q collides", oldTok, newTok)
		}
		s.Meta.AutoNameMap[newTok] = v
		delete(s.Meta.AutoNameMap, oldTok)
	}

	return nil
}
