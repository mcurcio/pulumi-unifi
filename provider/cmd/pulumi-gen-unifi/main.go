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

	"github.com/pkg/errors"

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
	flag.StringVar(&specPath, "spec", filepath.Join("openapi", "unifi-network-10.4.57.json"), "path to the pinned OpenAPI spec (fetched at build by openapi/fetch.sh)")
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
			panic(errors.Wrapf(err, "reading OpenAPI spec %s", specPath))
		}

		// The vendored beezly spec names many component schemas with spaces and
		// parentheses, which are not valid OpenAPI 3 component keys. Sanitize the
		// raw bytes before the loader validates them.
		specBytes, err = providerSchemaGen.SanitizeSpecBytes(specBytes)
		if err != nil {
			panic(errors.Wrap(err, "sanitizing OpenAPI spec"))
		}

		openAPIDoc := openapi.GetOpenAPISpec(specBytes)

		if err := providerSchemaGen.FixOpenAPIDoc(openAPIDoc); err != nil {
			panic(errors.Wrap(err, "fixing OpenAPI doc"))
		}

		schemaSpec, metadata, updatedOpenAPIDoc := providerSchemaGen.PulumiSchema(*openAPIDoc)

		mustWritePulumiSchema(schemaSpec, outDir)

		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			panic(errors.Wrap(err, "marshaling metadata"))
		}
		mustWriteFile(outDir, "metadata.json", metadataBytes)

		updatedOpenAPIDocBytes, err := yaml.Marshal(updatedOpenAPIDoc)
		if err != nil {
			panic(errors.Wrap(err, "marshaling fixed OpenAPI doc"))
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
		panic(errors.Wrap(err, "marshaling Pulumi schema"))
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
