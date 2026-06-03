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
//     identity — via the small declarative entityPrefixes table (the editorial
//     surface, like excludedPaths). A resource is prefixed only when its
//     collection POST actually carries a discriminator, so a flat resource that
//     happens to share a collection path is never mis-prefixed.
//
//   - Functions (D-M2.3): the operationId-derived function tokens are full of
//     upstream garbage — internal DTO names (getIntegrationDnsARecordDto),
//     snake_case bleed (getGateway_managed_network_details), broken
//     singularization (getCountrie, getFirewallPolicie), and get/list verb
//     confusion (listTrafficMatching is actually a get-one). normalizeFunction
//     cleans these deterministically by string transform: strip Integration/Dto,
//     PascalCase snake fragments (normalizing acronyms), fix irregular plurals
//     from one exceptions table, and apply a few explicit get/list settlements
//     for the near-duplicate pairs. The `*Page` list tokens are intentionally
//     left to the dedicated de-page pass (D-M2.5).
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

// entityPrefixes maps a discriminated entity's collection (create) path to the
// PascalCase prefix its variant resource tokens get. This is the one declarative
// editorial table for resource naming (analogous to excludedPaths): the prefixes
// are entity-meaningful names that path-segment derivation alone cannot produce
// (e.g. /networks → ManagedNetwork, not Network; /dns/policies → Dns, not
// DnsPolicy; acl-rules and traffic-matching-lists both → TrafficMatch). Keyed by
// the spec collection path so it cannot silently bind to the wrong entity.
var entityPrefixes = map[string]string{
	"/v1/sites/{siteId}/dns/policies":           "Dns",
	"/v1/sites/{siteId}/wifi/broadcasts":        "WifiBroadcast",
	"/v1/sites/{siteId}/networks":               "ManagedNetwork",
	"/v1/sites/{siteId}/acl-rules":              "TrafficMatch",
	"/v1/sites/{siteId}/traffic-matching-lists": "TrafficMatch",
}

// renameResources entity-prefixes the discriminated-variant resource tokens.
func renameResources(s *GenState) error {
	crudMap := s.Meta.ResourceCRUDMap
	doc := s.Doc

	for _, tok := range sortedKeys(s.Pkg.Resources) {
		m := crudMap[tok]
		if m == nil || m.C == nil {
			continue
		}
		prefix, ok := entityPrefixes[*m.C]
		if !ok {
			continue // not a prefixed entity
		}
		// Guard: only prefix genuine discriminated variants. If this collection's
		// POST body has no discriminator, the prefix table is stale/misapplied.
		_, _, isDisc, err := deriveDiscriminator(doc, tok, *m.C)
		if err != nil {
			return err
		}
		if !isDisc {
			return fmt.Errorf("token-rename: entityPrefixes has %q for collection %q but resource %q's create body carries no discriminator (stale prefix table?)", prefix, *m.C, tok)
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

// acronymFixups normalizes all-caps path/operationId acronym fragments to their
// PascalCase form when splitting snake_case function tokens. One small
// declarative table.
var acronymFixups = map[string]string{
	"VPN": "Vpn",
}

// irregularSingulars fixes the naive trailing-"s" stripping that produced broken
// singulars (Countrie/Categorie/Policie). One small declarative exceptions table
// (the brief's "irregular-plural exceptions table"), matched as a token suffix so
// it works inside a compound name (DpiApplicationCategorie → …Category).
var irregularSingulars = map[string]string{
	"Countrie":  "Country",
	"Categorie": "Category",
	"Policie":   "Policy",
}

// explicitFunctionRenames settles the F5 near-duplicate get/list pairs that a
// mechanical rule cannot disambiguate: the verb (get-one vs list-many) is wrong
// in the source operationId, and the singular/plural entity differs. Kept as an
// explicit one-place table because these are genuinely irregular (the spec
// mislabels a get-one as `list`).
var explicitFunctionRenames = map[string]string{
	"listTrafficMatching":  "getTrafficMatchingList",   // get-one (R = …/{id})
	"listTrafficMatchings": "listTrafficMatchingLists", // list-all
	"getFirewallPolicie":   "listFirewallPolicies",     // list-all (counterpart of getFirewallPolicy)
}

var funcVerbRE = regexp.MustCompile(`^(get|list)(.*)$`)

// normalizeFunction cleans one function token's short name. See tokenRenamePass.
func normalizeFunction(short string) string {
	if repl, ok := explicitFunctionRenames[short]; ok {
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
		if fixed, ok := acronymFixups[strings.ToUpper(p)]; ok {
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
// table (Countrie → Country, Policie → Policy, Categorie → Category).
func fixIrregularSingular(s string) string {
	for _, bad := range sortedKeys(irregularSingulars) {
		if strings.HasSuffix(s, bad) {
			return strings.TrimSuffix(s, bad) + irregularSingulars[bad]
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
