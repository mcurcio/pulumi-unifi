package gen

import (
	"fmt"
	"sort"
)

// excludeResourcesPass drops the action/batch RPC tokens listed in mappings.yaml's
// excludeResources from the managed resource set, in lockstep with their
// token-keyed metadata, while leaving their GET-derived data sources alone (D-M3.3).
//
// Why a resource-level exclusion and not excludedPaths: Voucher (a batch-generate
// RPC) and AdoptDevice (an imperative adopt action; destroy = un-adopt) are
// non-CRUD actions, not declarative resources — consistent with the project's
// policy of excluding non-CRUD endpoints. But their create/read endpoints are the
// SAME paths that serve useful read-only data sources (getVoucher,
// getAdoptedDeviceDetail, listAdoptedDeviceOverviews). Excluding the path would
// destroy those reads; excluding the resource token preserves them.
//
// The pass deletes each listed token from Pkg.Resources AND its entries in the
// token-keyed metadata maps (ResourceCRUDMap, AutoNameMap) together, so no orphan
// crudMap key survives (the same lockstep invariant token-rename and
// coalesce-discriminated-crud maintain). Pkg.Functions (the data sources) and all
// Pkg.Types are left untouched — a data source's read endpoint and its return
// types are independent of whether the entity is also a managed resource.
//
// A dead pin — an excludeResources entry that matches no live resource token — is
// a LOUD error (mirroring the excludedPaths-resolve guard in drift_test.go and the
// immutableFields dead-pin guard): a spec bump that renames or removes one of these
// tokens must force a deliberate mappings.yaml edit, not silently no-op.
//
// Position in the pass order (see schema.go): after token-rename, so it matches
// the final token names (Voucher/AdoptDevice are flat resources, not renamed, but
// matching post-rename keeps the contract version-agnostic), and before the
// annotation passes (descriptions / replaceOnChanges) so they never process the
// doomed resources.
//
// Deterministic: the listed tokens are matched by exact key and iterated in sorted
// order, so the resulting resource/metadata maps are independent of map iteration
// order (TestPipelineDeterministic).
func excludeResourcesPass(s *GenState) error {
	pkg := s.Pkg
	meta := s.Meta

	// Copy before sorting so the shared, embedded mappings slice is never mutated.
	toks := append([]string(nil), mappingExcludeResources()...)
	sort.Strings(toks)

	for _, tok := range toks {
		if _, ok := pkg.Resources[tok]; !ok {
			return fmt.Errorf("exclude-resources: mappings.yaml excludeResources entry %q matches no live resource token (dead pin — re-check on spec bump)", tok)
		}
		delete(pkg.Resources, tok)
		delete(meta.ResourceCRUDMap, tok)
		delete(meta.AutoNameMap, tok)
	}
	return nil
}
