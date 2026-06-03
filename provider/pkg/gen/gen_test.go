package gen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/cloudy-sky-software/pulumi-provider-framework/openapi"
)

// findSpec walks up from the test's working directory to locate the pinned
// OpenAPI spec at the repo root, so the test is independent of where `go test`
// is invoked from. The spec is a build artifact (fetched by `make fetch`, not
// committed); if it is absent the test skips with that instruction rather than
// failing — `make test` fetches it first.
func findSpec(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	const rel = "openapi/unifi-network-10.4.57.json"
	for {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skipf("pinned spec %s not found; run `make fetch` (it is a build artifact, not committed)", rel)
		}
		dir = parent
	}
}

// runPipeline reproduces the codegen entrypoint in-process: sanitize raw bytes,
// load, fix, and run pulschema. Returns the marshaled schema and metadata.
func runPipeline(t *testing.T, specBytes []byte) (schemaJSON, metadataJSON []byte) {
	t.Helper()
	sanitized, err := SanitizeSpecBytes(specBytes)
	if err != nil {
		t.Fatalf("SanitizeSpecBytes: %v", err)
	}
	doc := openapi.GetOpenAPISpec(sanitized)
	if err := FixOpenAPIDoc(doc); err != nil {
		t.Fatalf("FixOpenAPIDoc: %v", err)
	}
	pkg, metadata, _ := PulumiSchema(*doc)

	schemaJSON, err = json.MarshalIndent(pkg, "", "    ")
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	metadataJSON, err = json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	return schemaJSON, metadataJSON
}

// TestPipelineDeterministic is the regression guard for the discriminator-map
// ordering bug: running the full codegen pipeline twice over the same spec must
// produce byte-identical schema.json and metadata.json. This is what `make
// generate` relies on to yield an empty git diff.
func TestPipelineDeterministic(t *testing.T) {
	specBytes, err := os.ReadFile(findSpec(t))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}

	schema1, meta1 := runPipeline(t, specBytes)
	schema2, meta2 := runPipeline(t, specBytes)

	if string(schema1) != string(schema2) {
		t.Errorf("schema.json is non-deterministic across runs (len %d vs %d)", len(schema1), len(schema2))
	}
	if string(meta1) != string(meta2) {
		t.Errorf("metadata.json is non-deterministic across runs (len %d vs %d)", len(meta1), len(meta2))
	}
}

// TestFixOpenAPIDocInjectsAuth verifies Risk-A handling: an apiKey security
// scheme named X-API-Key is injected (the framework derives the auth header name
// from it).
func TestFixOpenAPIDocInjectsAuth(t *testing.T) {
	doc := &openapi3.T{
		OpenAPI: "3.1.0",
		Info:    &openapi3.Info{Title: "t", Version: "1"},
		Paths:   openapi3.NewPaths(),
	}
	if err := FixOpenAPIDoc(doc); err != nil {
		t.Fatalf("FixOpenAPIDoc: %v", err)
	}

	scheme := doc.Components.SecuritySchemes[apiKeySchemeName]
	if scheme == nil || scheme.Value == nil {
		t.Fatalf("expected security scheme %q to be injected", apiKeySchemeName)
	}
	if scheme.Value.Type != "apiKey" || scheme.Value.In != "header" {
		t.Errorf("expected apiKey/header scheme, got type=%q in=%q", scheme.Value.Type, scheme.Value.In)
	}
	if scheme.Value.Name != apiKeyHeaderName {
		t.Errorf("expected header name %q, got %q", apiKeyHeaderName, scheme.Value.Name)
	}
	if apiKeyHeaderName != "X-API-Key" {
		t.Errorf("apiKeyHeaderName must match the real UniFi header; got %q", apiKeyHeaderName)
	}
}

// TestFixOpenAPIDocRewritesServer verifies Risk-B handling: the relative
// "/integration" server becomes an absolute URL whose path ends in the real
// Integration API base path (the framework can only swap the host at runtime).
func TestFixOpenAPIDocRewritesServer(t *testing.T) {
	doc := &openapi3.T{
		OpenAPI: "3.1.0",
		Info:    &openapi3.Info{Title: "t", Version: "1"},
		Paths:   openapi3.NewPaths(),
		Servers: openapi3.Servers{&openapi3.Server{URL: "/integration"}},
	}
	if err := FixOpenAPIDoc(doc); err != nil {
		t.Fatalf("FixOpenAPIDoc: %v", err)
	}

	got := doc.Servers[0].URL
	want := "https://" + placeholderHost + integrationBasePath
	if got != want {
		t.Errorf("server URL = %q, want %q", got, want)
	}
}

// TestEnsureSchemaTitles confirms every title-less component schema gets a unique
// title equal to its key, the fix that makes discriminated-getter naming stable.
func TestEnsureSchemaTitles(t *testing.T) {
	doc := &openapi3.T{
		Components: &openapi3.Components{
			Schemas: openapi3.Schemas{
				"Alpha": &openapi3.SchemaRef{Value: &openapi3.Schema{}},
				"Beta":  &openapi3.SchemaRef{Value: &openapi3.Schema{Title: "Kept"}},
			},
		},
	}
	ensureSchemaTitles(doc)

	if got := doc.Components.Schemas["Alpha"].Value.Title; got != "Alpha" {
		t.Errorf("title-less schema: got title %q, want %q", got, "Alpha")
	}
	if got := doc.Components.Schemas["Beta"].Value.Title; got != "Kept" {
		t.Errorf("pre-titled schema overwritten: got %q, want %q", got, "Kept")
	}
}
