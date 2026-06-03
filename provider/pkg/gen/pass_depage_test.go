package gen

import (
	"strings"
	"testing"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
)

// TestDepageRemovesPageLeakageOnPipeline is the D-M2.5 regression guard: after the
// full pipeline, no list data source leaks pagination internals. The provider's
// OnPostInvoke auto-aggregates every page, so (1) no function token keeps a `*Page`
// short name (renamed to a plural `list<Entities>`), and (2) aggregated result
// types keep `data` but drop the per-request `limit`/`offset` knobs. Dropping the
// pass or its registration re-leaks the `*Page` tokens and fails here.
func TestDepageRemovesPageLeakageOnPipeline(t *testing.T) {
	pkg, _ := runPipelineTyped(t)

	for tok := range pkg.Functions {
		if strings.HasSuffix(tokenShortName(tok), "Page") {
			t.Errorf("function %q still leaks the pagination `*Page` suffix", tok)
		}
	}

	// A representative de-paged list exists under its plural list name...
	f, ok := pkg.Functions["unifi:sites/v1:listWifiBroadcasts"]
	if !ok {
		t.Fatal("expected getWifiBroadcastPage to be renamed to listWifiBroadcasts")
	}
	// ...and its result type dropped the paging knobs but kept the aggregated data.
	typeTok := functionReturnTypeToken(f)
	ct, ok := pkg.Types[typeTok]
	if !ok {
		t.Fatalf("result type %q for listWifiBroadcasts missing", typeTok)
	}
	if _, has := ct.Properties["data"]; !has {
		t.Error("aggregated result type lost its `data` collection")
	}
	if _, has := ct.Properties["limit"]; has {
		t.Error("aggregated result type still carries `limit` (a removed paging knob)")
	}
	if _, has := ct.Properties["offset"]; has {
		t.Error("aggregated result type still carries `offset` (a removed paging knob)")
	}
}

// TestIsPageEnvelope unit-checks the envelope discriminator: data+limit+offset is a
// page wrapper to de-page; missing any of the three is an ordinary `*Page`-named
// entity type that must be left untouched.
func TestIsPageEnvelope(t *testing.T) {
	env := pschema.ComplexTypeSpec{ObjectTypeSpec: pschema.ObjectTypeSpec{Properties: map[string]pschema.PropertySpec{
		"data": {}, "limit": {}, "offset": {},
	}}}
	if !isPageEnvelope(env) {
		t.Error("data+limit+offset should be detected as a page envelope")
	}

	notEnv := pschema.ComplexTypeSpec{ObjectTypeSpec: pschema.ObjectTypeSpec{Properties: map[string]pschema.PropertySpec{
		"data": {}, // no limit/offset
	}}}
	if isPageEnvelope(notEnv) {
		t.Error("a data-only type must not be treated as a page envelope")
	}
}
