// Package gen turns the vendored UniFi OpenAPI spec into a Pulumi package
// schema + CRUD metadata via pulschema. Resource grouping/CRUD mapping is
// auto-derived by pulschema from the REST path shape and verbs; the editorial
// surface here is the provider config, the OpenAPIContext, and ExcludedPaths.
//
// schema.go is orchestration only: it assembles the static package identity
// (packagespec.go), runs pulschema, and applies the post-process passes
// (genstate.go / pass_*.go). The static PackageSpec literal, the config block,
// and excludedPaths live in packagespec.go.
package gen

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"

	pythongen "github.com/pulumi/pulumi/pkg/v3/codegen/python"
	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// PulumiSchema generates the Pulumi package schema, CRUD metadata, and the
// pulschema-updated OpenAPI doc for the given (already fixed) spec.
func PulumiSchema(openAPIDoc openapi3.T) (pschema.PackageSpec, openapigen.ProviderMetadata, openapi3.T) {
	pkg := packageSpec()

	csharpNamespaces := map[string]string{
		packageName: "Unifi",
		"":          "Provider",
	}

	openAPICtx := &openapigen.OpenAPIContext{
		Doc:           openAPIDoc,
		Pkg:           &pkg,
		ExcludedPaths: excludedPaths,
	}

	providerMetadata, updatedOpenAPIDoc, err := openAPICtx.GatherResourcesFromAPI(csharpNamespaces)
	if err != nil {
		contract.Failf("generating resources from OpenAPI spec: %v", err)
	}

	// Run the ordered post-process passes over the shared GenState. pulschema's
	// output needs deterministic repair/polish (e.g. discriminated-variant CRUD
	// coalescing); each pass mutates the same Pkg/Meta/Doc in place. The order is
	// significant — see runPasses.
	state := &GenState{Pkg: &pkg, Meta: providerMetadata, Doc: &updatedOpenAPIDoc}
	runPasses(state)

	pkg.Language["python"] = rawMessage(pythongen.PackageInfo{
		PackageName: "pulumi_unifi",
		Requires: map[string]string{
			"pulumi": ">=3.0.0,<4.0.0",
		},
		PyProject: struct {
			Enabled bool `json:"enabled,omitempty"`
		}{Enabled: true},
	})

	metadata := openapigen.ProviderMetadata{
		ResourceCRUDMap:  providerMetadata.ResourceCRUDMap,
		AutoNameMap:      providerMetadata.AutoNameMap,
		SDKToAPINameMap:  providerMetadata.SDKToAPINameMap,
		APIToSDKNameMap:  providerMetadata.APIToSDKNameMap,
		PathParamNameMap: providerMetadata.PathParamNameMap,
	}
	return pkg, metadata, updatedOpenAPIDoc
}

func rawMessage(v interface{}) pschema.RawMessage {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(v)
	contract.Assert(err == nil)
	return out.Bytes()
}

// passes is the ordered list of post-pulschema transforms, run in sequence over
// a shared GenState. Order is significant: structural/CRUD repair runs first, so
// later naming/pruning/annotation passes (added by Track D) see a coalesced,
// fully-bound resource set. Each pass is named for logging + deterministic
// ordering and is independently testable. Document the rationale for any new
// pass's position when appending here.
var passes = []pass{
	// Structural/CRUD repair first, so later naming passes see a coalesced,
	// fully-bound resource set.
	{name: "coalesce-discriminated-crud", fn: coalesceDiscriminatedCRUDPass},
	// Inject the discriminator const while tokens still carry their pulschema
	// names (ToPascalCase(discriminatorValue)) — discriminatorInjectPass inverts
	// that naming to recover each variant's value, so it must run BEFORE the token
	// rename re-prefixes the tokens. It reads the spec discriminator mapping, not
	// the token, but matches on the token short name, so order matters.
	{name: "discriminator-inject", fn: discriminatorInjectPass},
	{name: "mark-secret-fields", fn: markSecretFieldsPass},
}

// runPasses applies every registered pass to state in order. A pass returning an
// error is a codegen-time defect (the spec or an upstream assumption changed), so
// it aborts the build loudly via contract.Failf — the correct idiom for the
// build-time codegen path.
func runPasses(state *GenState) {
	for _, p := range passes {
		if err := p.fn(state); err != nil {
			contract.Failf("gen pass %q: %v", p.name, err)
		}
	}
}

// sortedKeys returns a map's string keys in sorted order, for deterministic
// iteration (matching the sort.Strings idiom used elsewhere in this package).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
