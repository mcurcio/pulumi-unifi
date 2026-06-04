package gen

import (
	"testing"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// TestExcludeResourcesDropsActionsKeepsDataSources is the D-M3.3 integration
// proof: the Voucher/AdoptDevice action/batch RPCs must NOT ship as managed
// resources, AND the read-only data sources their shared endpoints serve must
// survive (the whole reason this is a resource-level exclusion, not an
// excludedPaths entry — those paths also back getVoucher /
// getAdoptedDeviceDetail / listAdoptedDeviceOverviews).
func TestExcludeResourcesDropsActionsKeepsDataSources(t *testing.T) {
	pkg, meta := runPipelineTyped(t)

	// The excluded resource tokens are gone from the resource set AND their
	// token-keyed metadata (no orphan crudMap/autoNameMap keys).
	for _, tok := range []string{"unifi:sites/v1:Voucher", "unifi:sites/v1:AdoptDevice"} {
		if _, ok := pkg.Resources[tok]; ok {
			t.Errorf("excluded resource %q still present in pkg.Resources", tok)
		}
		if _, ok := meta.ResourceCRUDMap[tok]; ok {
			t.Errorf("orphan crudMap entry for excluded resource %q", tok)
		}
		if _, ok := meta.AutoNameMap[tok]; ok {
			t.Errorf("orphan autoNameMap entry for excluded resource %q", tok)
		}
	}

	// The data sources sharing those endpoints SURVIVE — this is the property a
	// path-level exclusion would have broken.
	for _, fn := range []string{
		"unifi:sites/v1:getVoucher",
		"unifi:sites/v1:getAdoptedDeviceDetail",
		"unifi:sites/v1:listAdoptedDeviceOverviews",
	} {
		if _, ok := pkg.Functions[fn]; !ok {
			t.Errorf("data source %q was dropped — resource-level exclusion must preserve it", fn)
		}
	}
}

// TestExcludeResourcesDeadPinFailsLoud proves the dead-pin guard (mirroring
// TestExcludedPathsResolve and the immutableFields dead-pin guard): an
// excludeResources entry that matches no live resource token must fail loud, so a
// spec bump that renames/removes one of these tokens cannot leave a silently
// no-op exclusion behind.
func TestExcludeResourcesDeadPinFailsLoud(t *testing.T) {
	c := "/v1/sites/{siteId}/things"
	pkg := &pschema.PackageSpec{
		Resources: map[string]pschema.ResourceSpec{"unifi:sites/v1:RealThing": {}},
		Functions: map[string]pschema.FunctionSpec{},
	}
	meta := &openapigen.ProviderMetadata{
		ResourceCRUDMap: map[string]*openapigen.CRUDOperationsMap{"unifi:sites/v1:RealThing": {C: &c}},
		AutoNameMap:     map[string]string{},
	}
	s := &GenState{Pkg: pkg, Meta: meta}

	// excludeResourcesPass reads the embedded mappings.yaml, whose entries
	// (Voucher/AdoptDevice) are absent from this synthetic package — so every pin
	// is dead and the pass must error.
	if err := excludeResourcesPass(s); err == nil {
		t.Error("expected a dead-pin error when an excludeResources token matches no live resource")
	}
}

// TestExcludeResourcesLockstepDelete is the focused unit proof: a matching token
// is removed from the schema and BOTH token-keyed metadata maps together, leaving
// no orphan, while an unrelated resource and the data-source functions are
// untouched. Uses the live mappings.yaml tokens against a synthetic package that
// carries them.
func TestExcludeResourcesLockstepDelete(t *testing.T) {
	pins := mappingExcludeResources()
	if len(pins) == 0 {
		t.Skip("no excludeResources pins to exercise")
	}

	c := "/v1/sites/{siteId}/x"
	keep := "unifi:sites/v1:KeepMe"
	keepFn := "unifi:sites/v1:getKeepMe"

	resources := map[string]pschema.ResourceSpec{keep: {}}
	crud := map[string]*openapigen.CRUDOperationsMap{keep: {C: &c}}
	autoName := map[string]string{keep: "name"}
	for _, tok := range pins {
		resources[tok] = pschema.ResourceSpec{}
		crud[tok] = &openapigen.CRUDOperationsMap{C: &c}
		autoName[tok] = "name"
	}

	pkg := &pschema.PackageSpec{
		Resources: resources,
		Functions: map[string]pschema.FunctionSpec{keepFn: {}},
	}
	meta := &openapigen.ProviderMetadata{ResourceCRUDMap: crud, AutoNameMap: autoName}
	s := &GenState{Pkg: pkg, Meta: meta}

	if err := excludeResourcesPass(s); err != nil {
		t.Fatalf("excludeResourcesPass: %v", err)
	}

	for _, tok := range pins {
		if _, ok := pkg.Resources[tok]; ok {
			t.Errorf("resource %q not deleted", tok)
		}
		if _, ok := meta.ResourceCRUDMap[tok]; ok {
			t.Errorf("crudMap[%q] not deleted (orphan)", tok)
		}
		if _, ok := meta.AutoNameMap[tok]; ok {
			t.Errorf("autoNameMap[%q] not deleted (orphan)", tok)
		}
	}
	// Untouched neighbors.
	if _, ok := pkg.Resources[keep]; !ok {
		t.Errorf("unrelated resource %q was removed", keep)
	}
	if _, ok := pkg.Functions[keepFn]; !ok {
		t.Errorf("function %q was removed (functions must be untouched)", keepFn)
	}
}
