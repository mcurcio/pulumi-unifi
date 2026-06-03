// Package gen turns the vendored UniFi OpenAPI spec into a Pulumi package
// schema + CRUD metadata via pulschema. Resource grouping/CRUD mapping is
// auto-derived by pulschema from the REST path shape and verbs; the editorial
// surface here is the provider config, the OpenAPIContext, and ExcludedPaths.
package gen

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

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

	// pulschema binds only the create verb to each discriminated-variant resource
	// token; coalesce the remaining CRUD from the shared item path and prune the
	// orphan keys it leaves behind. See coalesceDiscriminatedCRUD.
	coalesceDiscriminatedCRUD(providerMetadata, updatedOpenAPIDoc, pkg)

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

// coalesceDiscriminatedCRUD repairs the create-only resource stubs pulschema
// emits for discriminated entities, and prunes the orphan crudMap keys it leaves
// behind.
//
// pulschema splits each oneOf+discriminator request body into one Pulumi
// resource per variant (e.g. a Wi-Fi broadcast becomes Standard + IotOptimized),
// but binds only the POST (create) verb to each variant token; the entity's
// GET/PATCH/PUT/DELETE verbs are keyed under separate per-verb schema tokens. A
// variant resource can therefore be created but has no read/update/delete
// endpoint, so it dies on the next `pulumi up` with "resource read endpoint is
// unknown". 18 of the 21 generated resources are such create-only stubs.
//
// The repair is mechanical and spec-driven. A collection POST path P_coll has a
// single canonical item path P_coll + "/{param}" carrying that entity's
// GET/PATCH/PUT/DELETE; the discriminator rides in the request body, so the item
// path is shared by — and correct for — every variant. Phase 1 fills any missing
// R/U/D/P on each resource token from that item path's verbs, never overwriting a
// verb pulschema already bound (so the 3 already-complete resources are
// untouched, and a verb the spec does not expose stays nil).
//
// Phase 2 prunes orphan crudMap keys: entries bound to neither a live resource
// nor a function token (pulschema's per-verb …CreateUpdateDto and singular-base
// shadow tokens). They are inert on the read path but a wrong-endpoint hazard
// once writes dispatch on resource tokens.
//
// Deterministic: a token's fill depends only on its own C and the spec, and a
// prune depends only on token-set membership — both independent of map iteration
// order — so schema.json/metadata.json stay byte-identical run to run
// (TestPipelineDeterministic). Keys are iterated sorted regardless.
func coalesceDiscriminatedCRUD(meta *openapigen.ProviderMetadata, doc openapi3.T, pkg pschema.PackageSpec) {
	crudMap := meta.ResourceCRUDMap

	excluded := make(map[string]bool, len(excludedPaths))
	for _, p := range excludedPaths {
		excluded[p] = true
	}

	// Phase 1: fill missing item-level verbs onto resource tokens from the
	// collection's canonical item path.
	for _, tok := range sortedKeys(crudMap) {
		if _, isResource := pkg.Resources[tok]; !isResource {
			continue // functions are read-only; only resources need full CRUD
		}
		m := crudMap[tok]
		if m == nil || m.C == nil {
			continue
		}
		itemPath := findItemPath(doc, *m.C, excluded)
		if itemPath == "" {
			continue
		}
		item := doc.Paths.Find(itemPath)
		if item == nil {
			continue
		}
		if m.R == nil && item.Get != nil {
			m.R = strPtr(itemPath)
		}
		if m.U == nil && item.Patch != nil {
			m.U = strPtr(itemPath)
		}
		if m.D == nil && item.Delete != nil {
			m.D = strPtr(itemPath)
		}
		if m.P == nil && item.Put != nil {
			m.P = strPtr(itemPath)
		}
	}

	// Phase 2: prune orphan keys that bind to no live token.
	for _, tok := range sortedKeys(crudMap) {
		_, isResource := pkg.Resources[tok]
		_, isFunction := pkg.Functions[tok]
		if !isResource && !isFunction {
			delete(crudMap, tok)
		}
	}
}

// findItemPath returns the canonical item path for a collection path: the unique
// sibling of the form collPath + "/{param}" (exactly one more path-parameter
// segment). Grandchildren (collPath + "/{param}/...") and excluded paths are not
// item paths. Returns "" when none exists. Sorted iteration keeps the result
// deterministic even in the (unexpected) case of multiple matches.
func findItemPath(doc openapi3.T, collPath string, excluded map[string]bool) string {
	prefix := collPath + "/"
	for _, p := range sortedKeys(doc.Paths.Map()) {
		if excluded[p] || !strings.HasPrefix(p, prefix) {
			continue
		}
		if seg := p[len(prefix):]; isSinglePathParamSegment(seg) {
			return p
		}
	}
	return ""
}

// isSinglePathParamSegment reports whether s is exactly one "{param}" path
// segment: brace-wrapped, non-empty, with no nested "/".
func isSinglePathParamSegment(s string) bool {
	return len(s) > 2 && s[0] == '{' && s[len(s)-1] == '}' && !strings.Contains(s, "/")
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

// strPtr returns a pointer to a fresh copy of s, so each crudMap verb field
// holds its own backing string (mirroring pulschema's per-verb &path).
func strPtr(s string) *string { return &s }
