package gen

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/cloudy-sky-software/pulumi-provider-framework/openapi"
)

// findSpec walks up from the test's working directory to locate the pinned
// OpenAPI spec at the repo root, so the test is independent of where `go test`
// is invoked from. The spec filename derives from the single-source pin
// (gen.SpecFileName), so it can never disagree with fetch.sh/the Makefile.
//
// The spec is a build artifact (fetched by `make fetch`, not committed). When it
// is absent the behavior is environment-dependent: in CI (CI env var set) a
// missing spec is a hard t.Fatalf so a broken fetch can't pass as a green skip;
// locally it is a t.Skipf with the fetch instruction so a bare `go test` is
// graceful (`make test` fetches first).
func findSpec(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	rel := filepath.Join("openapi", SpecFileName())
	for {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			msg := "pinned spec %s not found; run `make fetch` (it is a build artifact, not committed)"
			if os.Getenv("CI") != "" {
				t.Fatalf(msg, rel)
			}
			t.Skipf(msg, rel)
		}
		dir = parent
	}
}

// runPipeline reproduces the codegen entrypoint in-process: sanitize raw bytes,
// load, fix, and run pulschema. Returns the marshaled schema, metadata, and the
// pulschema-updated OpenAPI doc (serialized exactly as the gen entrypoint writes
// openapi_generated.yml), so determinism is checked over all three embedded
// artifacts.
func runPipeline(t *testing.T, specBytes []byte) (schemaJSON, metadataJSON, openapiYAML []byte) {
	t.Helper()
	sanitized, err := SanitizeSpecBytes(specBytes)
	if err != nil {
		t.Fatalf("SanitizeSpecBytes: %v", err)
	}
	doc := openapi.GetOpenAPISpec(sanitized)
	if err := FixOpenAPIDoc(doc); err != nil {
		t.Fatalf("FixOpenAPIDoc: %v", err)
	}
	pkg, metadata, updatedDoc := PulumiSchema(*doc)

	schemaJSON, err = MarshalSchemaJSON(pkg)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	metadataJSON, err = MarshalMetadataJSON(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	openapiYAML, err = yaml.Marshal(updatedDoc)
	if err != nil {
		t.Fatalf("marshal openapi doc: %v", err)
	}
	return schemaJSON, metadataJSON, openapiYAML
}

// TestPipelineDeterministic is the regression guard for the discriminator-map
// ordering bug: running the full codegen pipeline twice over the same spec must
// produce byte-identical schema.json, metadata.json, AND openapi_generated.yml.
// This is what `make generate` relies on to yield an empty git diff. (The fourth
// artifact, the Python SDK, is gated by a CI double-generate + git-diff step,
// since it is produced out-of-process by `pulumi package gen-sdk`.)
func TestPipelineDeterministic(t *testing.T) {
	specBytes, err := os.ReadFile(findSpec(t))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}

	schema1, meta1, openapi1 := runPipeline(t, specBytes)
	schema2, meta2, openapi2 := runPipeline(t, specBytes)

	if string(schema1) != string(schema2) {
		t.Errorf("schema.json is non-deterministic across runs (len %d vs %d)", len(schema1), len(schema2))
	}
	if string(meta1) != string(meta2) {
		t.Errorf("metadata.json is non-deterministic across runs (len %d vs %d)", len(meta1), len(meta2))
	}
	if string(openapi1) != string(openapi2) {
		t.Errorf("openapi_generated.yml is non-deterministic across runs (len %d vs %d)", len(openapi1), len(openapi2))
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

// TestFixOpenAPIDocRejectsExistingSecurity is the C-M1.4 auth guard: a spec that
// already declares a securityScheme (or top-level security) must error rather
// than silently layering the injected X-API-Key scheme on top.
func TestFixOpenAPIDocRejectsExistingSecurity(t *testing.T) {
	t.Run("existing scheme", func(t *testing.T) {
		doc := &openapi3.T{
			OpenAPI: "3.1.0",
			Info:    &openapi3.Info{Title: "t", Version: "1"},
			Paths:   openapi3.NewPaths(),
			Components: &openapi3.Components{
				SecuritySchemes: openapi3.SecuritySchemes{
					"Existing": &openapi3.SecuritySchemeRef{Value: &openapi3.SecurityScheme{Type: "http", Scheme: "bearer"}},
				},
			},
		}
		if err := FixOpenAPIDoc(doc); err == nil {
			t.Error("expected an error when the spec already declares a securityScheme")
		}
	})
	t.Run("existing top-level security", func(t *testing.T) {
		doc := &openapi3.T{
			OpenAPI:  "3.1.0",
			Info:     &openapi3.Info{Title: "t", Version: "1"},
			Paths:    openapi3.NewPaths(),
			Security: openapi3.SecurityRequirements{openapi3.SecurityRequirement{"Foo": []string{}}},
		}
		if err := FixOpenAPIDoc(doc); err == nil {
			t.Error("expected an error when the spec already declares top-level security")
		}
	})
}

// TestFixOpenAPIDocRejectsUnexpectedServer is the C-M1.4 server guard: a spec
// whose server is not the expected relative "/integration" must error rather
// than silently overwriting a (possibly versioned) base path.
func TestFixOpenAPIDocRejectsUnexpectedServer(t *testing.T) {
	doc := &openapi3.T{
		OpenAPI: "3.1.0",
		Info:    &openapi3.Info{Title: "t", Version: "1"},
		Paths:   openapi3.NewPaths(),
		Servers: openapi3.Servers{&openapi3.Server{URL: "/v2/integration"}},
	}
	if err := FixOpenAPIDoc(doc); err == nil {
		t.Error("expected an error for an unexpected server URL (base path may have moved)")
	}
}

// TestFindItemPathAmbiguity is the C-M1.4 item-path guard: a collection with two
// single-{param} item siblings is ambiguous and must error, instead of silently
// picking the sorted-first.
func TestFindItemPathAmbiguity(t *testing.T) {
	const coll = "/v1/sites/{siteId}/things"
	doc := &openapi3.T{Paths: openapi3.NewPaths()}
	doc.Paths.Set(coll+"/{id}", &openapi3.PathItem{})
	doc.Paths.Set(coll+"/{otherId}", &openapi3.PathItem{})

	if _, err := findItemPath(doc, coll, map[string]bool{}); err == nil {
		t.Error("expected an ambiguity error for two single-param item siblings")
	}

	// With one excluded, the remaining single match resolves cleanly.
	excluded := map[string]bool{coll + "/{otherId}": true}
	got, err := findItemPath(doc, coll, excluded)
	if err != nil {
		t.Fatalf("findItemPath: %v", err)
	}
	if got != coll+"/{id}" {
		t.Errorf("findItemPath = %q, want %q", got, coll+"/{id}")
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
