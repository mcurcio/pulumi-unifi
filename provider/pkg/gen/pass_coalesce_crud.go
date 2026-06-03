package gen

import (
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// coalesceDiscriminatedCRUDPass repairs the create-only resource stubs pulschema
// emits for discriminated entities, and prunes the orphan crudMap keys it leaves
// behind.
//
// pulschema splits each oneOf+discriminator request body into one Pulumi
// resource per variant (e.g. a Wi-Fi broadcast becomes Standard + IotOptimized),
// but binds only the POST (create) verb to each variant token; the entity's
// GET/PATCH/PUT/DELETE verbs are keyed under separate per-verb schema tokens. A
// variant resource can therefore be created but has no read/update/delete
// endpoint, so it dies on the next `pulumi up` with "resource read endpoint is
// unknown". 18 of the 21 generated resources are such create-only stubs.
//
// The repair is mechanical and spec-driven. A collection POST path P_coll has a
// single canonical item path P_coll + "/{param}" carrying that entity's
// GET/PATCH/PUT/DELETE; the discriminator rides in the request body, so the item
// path is shared by — and correct for — every variant. Phase 1 fills any missing
// R/U/D/P on each resource token from that item path's verbs, never overwriting a
// verb pulschema already bound (so the 3 already-complete resources are
// untouched, and a verb the spec does not expose stays nil).
//
// Phase 2 prunes orphan crudMap keys: entries bound to neither a live resource
// nor a function token (pulschema's per-verb …CreateUpdateDto and singular-base
// shadow tokens). They are inert on the read path but a wrong-endpoint hazard
// once writes dispatch on resource tokens.
//
// Deterministic: a token's fill depends only on its own C and the spec, and a
// prune depends only on token-set membership — both independent of map iteration
// order — so schema.json/metadata.json stay byte-identical run to run
// (TestPipelineDeterministic). Keys are iterated sorted regardless.
func coalesceDiscriminatedCRUDPass(s *GenState) error {
	crudMap := s.Meta.ResourceCRUDMap
	doc := s.Doc
	pkg := s.Pkg

	excluded := make(map[string]bool, len(excludedPaths))
	for _, p := range excludedPaths {
		excluded[p] = true
	}

	// Phase 1: fill missing item-level verbs onto resource tokens from the
	// collection's canonical item path.
	for _, tok := range sortedKeys(crudMap) {
		if _, isResource := pkg.Resources[tok]; !isResource {
			continue // functions are read-only; only resources need full CRUD
		}
		m := crudMap[tok]
		if m == nil || m.C == nil {
			// A managed resource with no create endpoint is itself a defect, but
			// that is the framework's contract to enforce; we only fill verbs from
			// a known collection path. Nothing to coalesce here.
			continue
		}

		itemPath := findItemPath(doc, *m.C, excluded)
		if itemPath != "" {
			if item := doc.Paths.Find(itemPath); item != nil {
				if m.R == nil && item.Get != nil {
					m.R = strPtr(itemPath)
				}
				if m.U == nil && item.Patch != nil {
					m.U = strPtr(itemPath)
				}
				if m.D == nil && item.Delete != nil {
					m.D = strPtr(itemPath)
				}
				if m.P == nil && item.Put != nil {
					m.P = strPtr(itemPath)
				}
			}
		}

		// Negative-coverage guard (A-M0.7): a managed resource that still has no
		// read endpoint after coalescing is a create-only stub that would die on
		// the next `pulumi up` ("resource read endpoint is unknown"). Fail the
		// build loudly rather than shipping it. The cause is an entity whose
		// collection path has no resolvable item sibling (P_coll + "/{param}") —
		// an unexpected spec shape this pass cannot repair.
		if m.R == nil {
			return fmt.Errorf("resource %q is create-only with no resolvable read endpoint: no item path (%s + \"/{param}\") found to coalesce R/U/D/P from", tok, *m.C)
		}
	}

	// Phase 2: prune orphan keys that bind to no live token.
	for _, tok := range sortedKeys(crudMap) {
		_, isResource := pkg.Resources[tok]
		_, isFunction := pkg.Functions[tok]
		if !isResource && !isFunction {
			delete(crudMap, tok)
		}
	}

	return nil
}

// findItemPath returns the canonical item path for a collection path: the unique
// sibling of the form collPath + "/{param}" (exactly one more path-parameter
// segment). Grandchildren (collPath + "/{param}/...") and excluded paths are not
// item paths. Returns "" when none exists. Sorted iteration keeps the result
// deterministic even in the (unexpected) case of multiple matches.
func findItemPath(doc *openapi3.T, collPath string, excluded map[string]bool) string {
	prefix := collPath + "/"
	for _, p := range sortedKeys(doc.Paths.Map()) {
		if excluded[p] || !strings.HasPrefix(p, prefix) {
			continue
		}
		if seg := p[len(prefix):]; isSinglePathParamSegment(seg) {
			return p
		}
	}
	return ""
}

// isSinglePathParamSegment reports whether s is exactly one "{param}" path
// segment: brace-wrapped, non-empty, with no nested "/".
func isSinglePathParamSegment(s string) bool {
	return len(s) > 2 && s[0] == '{' && s[len(s)-1] == '}' && !strings.Contains(s, "/")
}

// strPtr returns a pointer to a fresh copy of s, so each crudMap verb field
// holds its own backing string (mirroring pulschema's per-verb &path).
func strPtr(s string) *string { return &s }
