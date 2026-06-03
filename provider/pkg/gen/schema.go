// Package gen turns the vendored UniFi OpenAPI spec into a Pulumi package
// schema + CRUD metadata via pulschema. Resource grouping/CRUD mapping is
// auto-derived by pulschema from the REST path shape and verbs; the editorial
// surface here is the provider config, the OpenAPIContext, and ExcludedPaths.
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

const packageName = "unifi"

// excludedPaths lists endpoints that are not clean CRUD resources (RPC-style
// "actions", list "ordering" mutations, and read-only sub-resource lookups).
// Feeding them to the auto-grouper produces junk resources, so they are dropped.
// This list grows empirically as codegen output is inspected.
var excludedPaths = []string{
	"/v1/sites/{siteId}/acl-rules/ordering",
	"/v1/sites/{siteId}/firewall/policies/ordering",
	"/v1/sites/{siteId}/clients/{clientId}/actions",
	"/v1/sites/{siteId}/devices/{deviceId}/actions",
	"/v1/sites/{siteId}/devices/{deviceId}/interfaces/ports/{portIdx}/actions",
	"/v1/sites/{siteId}/devices/{deviceId}/statistics/latest",
	"/v1/sites/{siteId}/networks/{networkId}/references",
}

// PulumiSchema generates the Pulumi package schema, CRUD metadata, and the
// pulschema-updated OpenAPI doc for the given (already fixed) spec.
func PulumiSchema(openAPIDoc openapi3.T) (pschema.PackageSpec, openapigen.ProviderMetadata, openapi3.T) {
	pkg := pschema.PackageSpec{
		Name:        packageName,
		Description: "A Pulumi package for managing UniFi Network resources via the official Integration API.",
		DisplayName: "UniFi",
		License:     "Apache-2.0",
		Keywords: []string{
			"pulumi",
			packageName,
			"unifi",
			"ubiquiti",
			"category/network",
			"kind/native",
		},
		Homepage:   "https://github.com/mcurcio/pulumi-unifi",
		Publisher:  "Matt Curcio",
		Repository: "https://github.com/mcurcio/pulumi-unifi",

		Config: pschema.ConfigSpec{
			Variables: map[string]pschema.PropertySpec{
				"apiKey": {
					Description: "The UniFi Network Integration API key (sent as the X-API-Key header).",
					TypeSpec:    pschema.TypeSpec{Type: "string"},
					Secret:      true,
				},
				"apiHost": {
					Description: "The UniFi controller host (and optional :port), e.g. \"192.168.1.1\" or \"unifi.example.com:443\". Overrides the host in the generated server URL.",
					TypeSpec:    pschema.TypeSpec{Type: "string"},
				},
				"siteId": {
					Description: "The UniFi site ID used to fill the {siteId} path parameter on site-scoped resources. Defaults to \"default\".",
					TypeSpec:    pschema.TypeSpec{Type: "string"},
				},
				"allowInsecure": {
					Description: "Skip TLS certificate verification when connecting to the controller. Use only for controllers presenting self-signed certificates on a trusted network. Note: this also disables the HTTP 429 rate-limit retry wrapper.",
					TypeSpec:    pschema.TypeSpec{Type: "boolean"},
				},
			},
		},

		Provider: pschema.ResourceSpec{
			ObjectTypeSpec: pschema.ObjectTypeSpec{
				Description: "The provider type for the unifi package.",
				Type:        "object",
			},
			InputProperties: map[string]pschema.PropertySpec{
				"apiKey": {
					DefaultInfo: &pschema.DefaultSpec{
						Environment: []string{"UNIFI_APIKEY"},
					},
					Description: "The UniFi Network Integration API key (sent as the X-API-Key header).",
					TypeSpec:    pschema.TypeSpec{Type: "string"},
					Secret:      true,
				},
				"apiHost": {
					DefaultInfo: &pschema.DefaultSpec{
						Environment: []string{"UNIFI_API_HOST"},
					},
					Description: "The UniFi controller host (and optional :port).",
					TypeSpec:    pschema.TypeSpec{Type: "string"},
				},
				"siteId": {
					DefaultInfo: &pschema.DefaultSpec{
						Environment: []string{"UNIFI_SITEID"},
					},
					Description: "The UniFi site ID used to fill the {siteId} path parameter.",
					TypeSpec:    pschema.TypeSpec{Type: "string"},
				},
				"allowInsecure": {
					DefaultInfo: &pschema.DefaultSpec{
						Environment: []string{"UNIFI_ALLOW_INSECURE"},
					},
					Description: "Skip TLS certificate verification when connecting to the controller.",
					TypeSpec:    pschema.TypeSpec{Type: "boolean"},
				},
			},
		},

		PluginDownloadURL: "github://api.github.com/mcurcio/pulumi-unifi",
		Types:             map[string]pschema.ComplexTypeSpec{},
		Resources:         map[string]pschema.ResourceSpec{},
		Functions:         map[string]pschema.FunctionSpec{},
		Language:          map[string]pschema.RawMessage{},
	}

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
	{name: "coalesce-discriminated-crud", fn: coalesceDiscriminatedCRUDPass},
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
