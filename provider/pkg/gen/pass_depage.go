package gen

import (
	"fmt"
	"strings"

	"github.com/pulumi/inflector"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
)

// depagePass (D-M2.5) removes the pagination internals that leak into the public
// API. The provider's OnPostInvoke auto-aggregates every page into one result
// (CLAUDE.md / DESIGN §0), so the `*Page` list data sources are misleading: the
// name promises pagination the provider has deliberately removed, and the result
// type still carries `limit`/`offset` knobs that are meaningless after
// aggregation (there is no second page to fetch; they reflect one underlying
// request, not the aggregate) — see 03-F7.
//
// This pass, for each list data source whose function token ends in `Page` and
// whose return type is a genuine page envelope (carries data + limit + offset):
//
//  1. Renames the function token get<Entity>Page → list<Entities> (plural via the
//     pulumi inflector — getWifiBroadcastPage → listWifiBroadcasts,
//     getDnsPolicyPage → listDnsPolicies), moving every token-keyed map entry in
//     lockstep (renameToken). The verb flips get→list because the aggregated
//     result is the whole collection.
//  2. Drops `limit`/`offset` from the aggregated result type (its Properties and
//     Required), keeping `data` (and the aggregate `count`/`totalCount`).
//
// The `Page` suffix is the marker pulschema leaves; token-rename intentionally
// left these for this dedicated pass. Each page type is referenced by exactly one
// function (verified), so trimming its fields is local.
//
// Deterministic: each rename/trim depends only on the token's own short name and
// its return-type's fields, applied over sorted keys; independent of map
// iteration order.
func depagePass(s *GenState) error {
	for _, tok := range sortedKeys(s.Pkg.Functions) {
		short := tokenShortName(tok)
		entity, isPage := strings.CutSuffix(short, "Page")
		if !isPage {
			continue
		}
		entity = strings.TrimPrefix(entity, "get")
		if entity == "" {
			continue
		}

		// Resolve the function's return type; only de-page a genuine page envelope.
		typeTok := functionReturnTypeToken(s.Pkg.Functions[tok])
		if typeTok == "" {
			continue
		}
		ct, ok := s.Pkg.Types[typeTok]
		if !ok || !isPageEnvelope(ct) {
			continue
		}

		// (2) Drop the now-meaningless paging knobs from the aggregated result.
		dropProperty(&ct, "limit")
		dropProperty(&ct, "offset")
		s.Pkg.Types[typeTok] = ct

		// (1) Rename get<Entity>Page → list<Entities>.
		mod, _ := splitToken(tok)
		newShort := "list" + inflector.Pluralize(entity)
		newTok := mod + newShort
		if newTok == tok {
			continue
		}
		if err := renameToken(s, tok, newTok, false); err != nil {
			return fmt.Errorf("depage: %w", err)
		}
	}
	return nil
}

// functionReturnTypeToken returns the in-document type token a function's return
// type refs (e.g. "unifi:sites/v1:IntegrationWifiBroadcastPageDto"), or "" if the
// return is not a single in-document type ref.
func functionReturnTypeToken(f pschema.FunctionSpec) string {
	if f.ReturnType == nil || f.ReturnType.TypeSpec == nil {
		return ""
	}
	const prefix = "#/types/"
	ref := f.ReturnType.TypeSpec.Ref
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}
	return strings.TrimPrefix(ref, prefix)
}

// isPageEnvelope reports whether a type is a list-page envelope: it has the
// aggregated `data` collection plus the per-request `limit`/`offset` paging knobs
// this pass removes. The presence of all three distinguishes a real page wrapper
// from an ordinary `*Page`-named entity type.
func isPageEnvelope(ct pschema.ComplexTypeSpec) bool {
	_, hasData := ct.Properties["data"]
	_, hasLimit := ct.Properties["limit"]
	_, hasOffset := ct.Properties["offset"]
	return hasData && hasLimit && hasOffset
}

// dropProperty removes a property from a complex type's Properties map and its
// Required list. No-op if absent.
func dropProperty(ct *pschema.ComplexTypeSpec, name string) {
	delete(ct.Properties, name)
	ct.Required = removeString(ct.Required, name)
}
