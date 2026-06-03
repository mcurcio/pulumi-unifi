package gen

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/cloudy-sky-software/pulumi-provider-framework/openapi"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
)

// runPipelineTyped reproduces the codegen pipeline like runPipeline but returns
// the typed schema + metadata structs, so crudMap-consistency tests can assert
// on tokens and endpoint bindings directly instead of re-parsing JSON.
func runPipelineTyped(t *testing.T) (pschema.PackageSpec, openapigen.ProviderMetadata) {
	t.Helper()
	specBytes, err := os.ReadFile(findSpec(t))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	sanitized, err := SanitizeSpecBytes(specBytes)
	if err != nil {
		t.Fatalf("SanitizeSpecBytes: %v", err)
	}
	doc := openapi.GetOpenAPISpec(sanitized)
	if err := FixOpenAPIDoc(doc); err != nil {
		t.Fatalf("FixOpenAPIDoc: %v", err)
	}
	pkg, metadata, _ := PulumiSchema(*doc)
	return pkg, metadata
}

// runPulschemaRaw runs pulschema's GatherResourcesFromAPI on the fixed spec
// WITHOUT this package's post-process passes, so a test can observe pulschema's
// unrepaired output (in particular the create-only discriminated stubs the
// coalesce pass exists to fix). It mirrors PulumiSchema's setup minus runPasses.
func runPulschemaRaw(t *testing.T) (pschema.PackageSpec, *openapigen.ProviderMetadata) {
	t.Helper()
	doc := fixedDoc(t)
	pkg := packageSpec()
	openAPICtx := &openapigen.OpenAPIContext{
		Doc:           *doc,
		Pkg:           &pkg,
		ExcludedPaths: excludedPaths,
	}
	meta, _, err := openAPICtx.GatherResourcesFromAPI(map[string]string{
		packageName: "Unifi",
		"":          "Provider",
	})
	if err != nil {
		t.Fatalf("GatherResourcesFromAPI: %v", err)
	}
	return pkg, meta
}

// TestCoalesceStillNeeded asserts the coalesce pass is not yet redundant: raw
// pulschema (pre-pass) still emits at least one discriminated-variant resource
// bound to create only (C set, R nil). If an upstream pulschema fix (PR G-U1)
// starts binding full CRUD per variant, every resource will arrive with R set,
// this test fails, and that failure is the signal that pass_coalesce_crud.go can
// be deleted. (A-M0.8 / 06-F7.)
func TestCoalesceStillNeeded(t *testing.T) {
	pkg, meta := runPulschemaRaw(t)

	createOnly := 0
	for tok := range pkg.Resources {
		m := meta.ResourceCRUDMap[tok]
		if m != nil && m.C != nil && m.R == nil {
			createOnly++
		}
	}
	if createOnly == 0 {
		t.Error("no create-only resource stubs from raw pulschema — the coalesce pass (pass_coalesce_crud.go) may now be redundant; if upstream G-U1 landed, delete it and rebase the golden")
	}
	t.Logf("raw pulschema emits %d create-only resource stubs (coalesce pass still needed)", createOnly)
}

// itemPathRE matches a REST item-level path: one ending in a single
// "/{param}" segment (e.g. /v1/sites/{siteId}/firewall/policies/{id}). A
// collection path ends in a literal segment.
var itemPathRE = regexp.MustCompile(`/\{[^/}]+\}$`)

func isItemPath(p string) bool { return itemPathRE.MatchString(p) }

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// TestCRUDMapCoversAllTokens is the lenient (reverse-direction) consistency
// guard: every live resource/function token must have a crudMap entry, or the
// framework cannot dispatch it. The strict forward direction (every crudMap
// *key* is a live token) is TestCRUDMapKeysAreLiveTokens, green since B1's
// orphan pruning landed.
func TestCRUDMapCoversAllTokens(t *testing.T) {
	pkg, meta := runPipelineTyped(t)
	for tok := range pkg.Resources {
		if _, ok := meta.ResourceCRUDMap[tok]; !ok {
			t.Errorf("resource token %q has no crudMap entry — framework cannot dispatch it", tok)
		}
	}
	for tok := range pkg.Functions {
		if _, ok := meta.ResourceCRUDMap[tok]; !ok {
			t.Errorf("function token %q has no crudMap entry — framework cannot dispatch it", tok)
		}
	}
}

// TestCRUDMapKeysAreLiveTokens is the strict forward-direction guard: every
// crudMap key must resolve to a live schema token. Before B1, pulschema emitted
// ~68 orphan keys (singular bases and …Dto/…CreateUpdateDto pairs) that bound to
// no resource or function — inert on the read path but a wrong-endpoint hazard
// once writes dispatch resource tokens. B1's Phase-2 orphan pruning removes them.
func TestCRUDMapKeysAreLiveTokens(t *testing.T) {
	pkg, meta := runPipelineTyped(t)
	for tok := range meta.ResourceCRUDMap {
		_, isResource := pkg.Resources[tok]
		_, isFunction := pkg.Functions[tok]
		if !isResource && !isFunction {
			t.Errorf("crudMap key %q is neither a resource nor a function token (orphan)", tok)
		}
	}
}

// TestResourceCRUDBindsItemLevel is the executable spec for B1. A managed
// resource must round-trip: C bound to a collection path and R + D (plus U/P
// where present) bound to the item-level {id} path. pulschema fragments each
// discriminated entity's verbs across per-variant tokens, leaving 18 resources
// as create-only stubs (C only) — a created resource then dies on the next
// `pulumi up` because Read has no endpoint. B1's Phase-1 coalescing fills the
// missing verbs from the shared item path, so every resource now binds R + D.
func TestResourceCRUDBindsItemLevel(t *testing.T) {
	pkg, meta := runPipelineTyped(t)
	for tok := range pkg.Resources {
		m := meta.ResourceCRUDMap[tok]
		if m == nil {
			t.Errorf("resource %q has no crudMap entry", tok)
			continue
		}
		if m.C == nil || isItemPath(*m.C) {
			t.Errorf("resource %q: C must bind a collection path, got %q", tok, deref(m.C))
		}
		if m.R == nil || !isItemPath(*m.R) {
			t.Errorf("resource %q: R must bind an item-level {id} path, got %q", tok, deref(m.R))
		}
		if m.D == nil || !isItemPath(*m.D) {
			t.Errorf("resource %q: D must bind an item-level {id} path, got %q", tok, deref(m.D))
		}
		if m.U != nil && !isItemPath(*m.U) {
			t.Errorf("resource %q: U must bind an item-level {id} path, got %q", tok, *m.U)
		}
		if m.P != nil && !isItemPath(*m.P) {
			t.Errorf("resource %q: P must bind an item-level {id} path, got %q", tok, *m.P)
		}
	}
}

// isChildItemPath reports whether item is coll's canonical item sibling:
// coll + "/{param}" with exactly one more path-parameter segment. It re-derives
// the relationship the gen layer asserts, independently of the code under test.
func isChildItemPath(coll, item string) bool {
	prefix := coll + "/"
	if !strings.HasPrefix(item, prefix) {
		return false
	}
	seg := item[len(prefix):]
	return len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}' && !strings.Contains(seg, "/")
}

// TestDiscriminatedResourcesHaveFullCRUD is the targeted proof of B1's Phase-1
// coalescing: the per-variant resource tokens pulschema emits as create-only
// stubs each gain read + delete bound to their entity's shared item path. It
// asserts the structural sibling relationship (R/D == C + "/{param}", R == D)
// rather than hardcoded path strings, so it survives spec param-name churn while
// still proving every listed variant round-trips. TestResourceCRUDBindsItemLevel
// covers the whole resource set; this pins the discriminated variants by name so
// a regression that silently drops one is caught explicitly.
func TestDiscriminatedResourcesHaveFullCRUD(t *testing.T) {
	pkg, meta := runPipelineTyped(t)

	// Tokens that carried only C before B1, grouped by the entity (comment) whose
	// item path their CRUD coalesces onto. The discriminator rides in the request
	// body, so every variant of an entity shares one item path.
	formerStubs := []string{
		"unifi:sites/v1:Standard", "unifi:sites/v1:IotOptimized", // wifi/broadcasts
		"unifi:sites/v1:Gateway", "unifi:sites/v1:Switch", "unifi:sites/v1:Unmanaged", // networks
		"unifi:sites/v1:ARecord", "unifi:sites/v1:AaaaRecord", "unifi:sites/v1:CnameRecord",
		"unifi:sites/v1:MxRecord", "unifi:sites/v1:SrvRecord", "unifi:sites/v1:TxtRecord",
		"unifi:sites/v1:ForwardDomain",              // dns/policies
		"unifi:sites/v1:Ipv4", "unifi:sites/v1:Mac", // acl-rules
		"unifi:sites/v1:Ipv4Addresses", "unifi:sites/v1:Ipv6Addresses", "unifi:sites/v1:Ports", // traffic-matching-lists
	}

	for _, tok := range formerStubs {
		if _, ok := pkg.Resources[tok]; !ok {
			t.Errorf("%s: expected a live resource token (spec drift?)", tok)
			continue
		}
		m := meta.ResourceCRUDMap[tok]
		if m == nil {
			t.Errorf("%s: no crudMap entry", tok)
			continue
		}
		if m.C == nil || isItemPath(*m.C) {
			t.Errorf("%s: C must stay a collection path, got %q", tok, deref(m.C))
			continue
		}
		if m.R == nil || !isItemPath(*m.R) {
			t.Errorf("%s: R must coalesce to the item path, got %q", tok, deref(m.R))
		}
		if m.D == nil || !isItemPath(*m.D) {
			t.Errorf("%s: D must coalesce to the item path, got %q", tok, deref(m.D))
		}
		// R and D must be the one shared item path, and that path must be C's
		// canonical "{param}" sibling.
		if m.R != nil && m.D != nil && *m.R != *m.D {
			t.Errorf("%s: R %q and D %q must be the same shared item path", tok, *m.R, *m.D)
		}
		if m.R != nil && m.C != nil && !isChildItemPath(*m.C, *m.R) {
			t.Errorf("%s: R %q must be the item sibling of C %q (C + \"/{param}\")", tok, *m.R, *m.C)
		}
	}

	// The wifi/broadcasts item path also exposes PUT, so its variants must gain P
	// — proving PUT (not just GET/DELETE) coalesces. This is the metadata behind
	// the plan's `jq '.crudMap["…:Standard"] | keys' == ["c","d","p","r"]` check.
	for _, tok := range []string{"unifi:sites/v1:Standard", "unifi:sites/v1:IotOptimized"} {
		m := meta.ResourceCRUDMap[tok]
		if m == nil || m.P == nil || !isItemPath(*m.P) {
			var got string
			if m != nil {
				got = deref(m.P)
			}
			t.Errorf("%s: P must coalesce from the item path's PUT, got %q", tok, got)
		}
	}
}
