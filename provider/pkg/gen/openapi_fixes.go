package gen

import (
	"github.com/getkin/kin-openapi/openapi3"
)

const (
	// apiKeySchemeName is the components.securitySchemes key under which the
	// injected X-API-Key scheme is registered.
	apiKeySchemeName = "ApiKeyAuth"
	// apiKeyHeaderName is the HTTP header the UniFi Integration API expects the
	// key in. The provider framework derives the auth header *name* from the
	// security scheme's `name`, so this value must match the real API.
	apiKeyHeaderName = "X-API-Key"

	// integrationBasePath is the absolute path prefix the UniFi controller serves
	// the Integration API under. The vendored spec ships a relative "/integration"
	// server, which the framework cannot use as a base URL; we rewrite it to an
	// absolute URL whose host is overridden at runtime via the apiHost config.
	integrationBasePath = "/proxy/network/integration"
	// placeholderHost is a stand-in host baked into the generated server URL. At
	// configure time the framework swaps only the host (apiHost / UNIFI_API_HOST),
	// leaving the scheme and path intact.
	placeholderHost = "localhost"
)

// FixOpenAPIDoc applies UniFi-specific corrections to the vendored spec before
// pulschema processes it:
//
//   - Risk A: the spec has no securitySchemes, but the API is authenticated with
//     an X-API-Key header. We inject an apiKey security scheme so the framework
//     names the auth header correctly at request time.
//   - Risk B: the spec's server is the relative "/integration". The framework
//     uses Servers[0].URL verbatim as the base URL and can only override the
//     host, so we rewrite it to an absolute URL ending in the real Integration
//     API base path.
//
// All mutations are deterministic.
func FixOpenAPIDoc(openAPIDoc *openapi3.T) error {
	injectAPIKeySecurityScheme(openAPIDoc)
	rewriteServerURL(openAPIDoc)
	ensureSchemaTitles(openAPIDoc)
	return nil
}

// ensureSchemaTitles gives every title-less component schema a Title equal to
// its component key.
//
// Why: for a GET endpoint whose response is a discriminated union, pulschema
// emits one getter function per discriminator-mapping variant, naming it
// `"get" + schema.Title` (openapi.go genResourcesFromAPI). The vendored beezly
// spec sets no titles, so every variant collapses to the function token "get",
// and pulschema overwrites that single key while ranging the discriminator
// mapping — a Go map, so the surviving definition (and thus the generated type)
// is non-deterministic across runs.
//
// Component keys are unique and, post-SanitizeSpecBytes, already valid
// identifiers, so using the key as the title yields unique, stable getter names.
// Other naming paths already fall back to the component key when Title is empty
// (getResourceTitleFromRequestSchema), so setting it to the same basis leaves
// resource/type tokens unchanged.
func ensureSchemaTitles(openAPIDoc *openapi3.T) {
	if openAPIDoc.Components == nil {
		return
	}
	for key, ref := range openAPIDoc.Components.Schemas {
		if ref == nil || ref.Value == nil {
			continue
		}
		if ref.Value.Title == "" {
			ref.Value.Title = key
		}
	}
}

// injectAPIKeySecurityScheme adds an `in: header` apiKey scheme named X-API-Key
// (Risk A). The framework's getAuthHeaderName reads this scheme's name.
func injectAPIKeySecurityScheme(openAPIDoc *openapi3.T) {
	if openAPIDoc.Components == nil {
		openAPIDoc.Components = &openapi3.Components{}
	}
	if openAPIDoc.Components.SecuritySchemes == nil {
		openAPIDoc.Components.SecuritySchemes = openapi3.SecuritySchemes{}
	}
	openAPIDoc.Components.SecuritySchemes[apiKeySchemeName] = &openapi3.SecuritySchemeRef{
		Value: &openapi3.SecurityScheme{
			Type: "apiKey",
			In:   "header",
			Name: apiKeyHeaderName,
		},
	}

	// Apply the scheme globally so it is the documented requirement for every op.
	openAPIDoc.Security = openapi3.SecurityRequirements{
		openapi3.SecurityRequirement{apiKeySchemeName: []string{}},
	}
}

// rewriteServerURL replaces the relative "/integration" server with an absolute
// URL (Risk B). The host is a placeholder overridden at runtime via apiHost.
func rewriteServerURL(openAPIDoc *openapi3.T) {
	absolute := "https://" + placeholderHost + integrationBasePath
	if len(openAPIDoc.Servers) == 0 {
		openAPIDoc.Servers = openapi3.Servers{&openapi3.Server{URL: absolute}}
		return
	}
	openAPIDoc.Servers[0].URL = absolute
}
