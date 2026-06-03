// Code generator for the UniFi Pulumi provider.
//
// It reads the vendored OpenAPI spec, applies UniFi-specific fixes
// (FixOpenAPIDoc — inject the X-API-Key security scheme, rewrite the relative
// server URL to an absolute one), runs pulschema to derive the Pulumi package
// schema + CRUD metadata, and writes the three artifacts the plugin binary
// embeds: schema.json, metadata.json, openapi_generated.yml.
//
// Usage: pulumi-gen-unifi [-spec PATH] [-out DIR] [-version V] schema
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/cloudy-sky-software/pulumi-provider-framework/openapi"

	"github.com/pulumi/pulumi/pkg/v3/codegen/schema"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"

	providerSchemaGen "github.com/mcurcio/pulumi-unifi/provider/pkg/gen"
	providerVersion "github.com/mcurcio/pulumi-unifi/provider/pkg/version"
)

// Language is the SDK/codegen target requested on the command line.
type Language string

const (
	// Schema generates the Pulumi package schema + metadata + fixed OpenAPI doc.
	Schema Language = "schema"
)

func main() {
	flag.Usage = func() {
		_, err := fmt.Fprint(flag.CommandLine.Output(), "Usage: pulumi-gen-unifi [flags] <language>\n")
		contract.IgnoreError(err)
		flag.PrintDefaults()
	}

	var version string
	var specPath string
	var outDir string
	flag.StringVar(&version, "version", providerVersion.Version, "provider version to record in generated code")
	flag.StringVar(&specPath, "spec", filepath.Join("openapi", providerSchemaGen.SpecFileName()), "path to the pinned OpenAPI spec (fetched at build by openapi/fetch.sh)")
	flag.StringVar(&outDir, "out", filepath.Join("provider", "cmd", "pulumi-resource-unifi"), "directory to write generated artifacts into")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		os.Exit(2)
	}

	switch Language(args[0]) {
	case Schema:
		specBytes, err := os.ReadFile(specPath)
		if err != nil {
			panic(fmt.Errorf("reading OpenAPI spec %s: %w", specPath, err))
		}

		// The vendored beezly spec names many component schemas with spaces and
		// parentheses, which are not valid OpenAPI 3 component keys. Sanitize the
		// raw bytes before the loader validates them.
		specBytes, err = providerSchemaGen.SanitizeSpecBytes(specBytes)
		if err != nil {
			panic(fmt.Errorf("sanitizing OpenAPI spec: %w", err))
		}

		openAPIDoc := openapi.GetOpenAPISpec(specBytes)

		// Assert the fetched bytes actually contain the pinned version. fetch.sh
		// verifies the sha256, but a mis-edited SHA that resolves to a different
		// (yet self-consistent) spec would otherwise generate silently from the
		// wrong API version. info.version is the only in-band tell.
		if openAPIDoc.Info == nil || openAPIDoc.Info.Version != providerSchemaGen.PinnedSpecVersion {
			got := "<no info.version>"
			if openAPIDoc.Info != nil {
				got = openAPIDoc.Info.Version
			}
			panic(fmt.Errorf("spec info.version %q does not match the pinned version %q (wrong spec fetched? bump openapi/pin.env)", got, providerSchemaGen.PinnedSpecVersion))
		}

		if err := providerSchemaGen.FixOpenAPIDoc(openAPIDoc); err != nil {
			panic(fmt.Errorf("fixing OpenAPI doc: %w", err))
		}

		schemaSpec, metadata, updatedOpenAPIDoc := providerSchemaGen.PulumiSchema(*openAPIDoc)

		mustWritePulumiSchema(schemaSpec, outDir)

		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			panic(fmt.Errorf("marshaling metadata: %w", err))
		}
		mustWriteFile(outDir, "metadata.json", metadataBytes)

		updatedOpenAPIDocBytes, err := yaml.Marshal(updatedOpenAPIDoc)
		if err != nil {
			panic(fmt.Errorf("marshaling fixed OpenAPI doc: %w", err))
		}
		mustWriteFile(outDir, "openapi_generated.yml", updatedOpenAPIDocBytes)
	default:
		panic(fmt.Sprintf("unrecognized language %q", args[0]))
	}
}

func mustWritePulumiSchema(pkgSpec schema.PackageSpec, outDir string) {
	// The version is injected into the binary at build time, not baked into the
	// committed schema, so generation stays deterministic across versions.
	pkgSpec.Version = ""
	schemaJSON, err := json.MarshalIndent(pkgSpec, "", "    ")
	if err != nil {
		panic(fmt.Errorf("marshaling Pulumi schema: %w", err))
	}
	mustWriteFile(outDir, "schema.json", schemaJSON)
}

func mustWriteFile(rootDir, filename string, contents []byte) {
	outPath := filepath.Join(rootDir, filename)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(outPath, contents, 0o644); err != nil {
		panic(err)
	}
}
