// pulumi-resource-unifi is the UniFi Pulumi provider plugin binary. It embeds
// the generated package schema, CRUD metadata, and fixed OpenAPI doc, then
// serves them through the cloudy-sky-software REST CRUD framework, wrapped by
// the private-API dispatcher (docs/design/private-binding.md §2).
//
// schema.merged.json and private.json are written by `make generate_schema`
// (gitignored). With PRIVATE=0 (the default) schema.merged.json equals the
// schema.json golden and private.json serves no kinds, so the plugin behaves
// exactly as the framework alone.
package main

import (
	_ "embed"

	rpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/private"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/provider"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/version"
)

const providerName = "unifi"

// The framework runs on the official golden only, so its checks are untouched.
//
//go:embed schema.json
var pulumiSchema []byte

//go:embed openapi_generated.yml
var openapiDocBytes []byte

//go:embed metadata.json
var metadataBytes []byte

// GetSchema returns the merged schema (official + private kinds).
//
//go:embed schema.merged.json
var mergedSchema []byte

// The private kinds the engine serves.
//
//go:embed private.json
var privateSurface []byte

func main() {
	provider.Serve(providerName, version.Version, pulumiSchema, openapiDocBytes, metadataBytes,
		func(fw rpc.ResourceProviderServer) (rpc.ResourceProviderServer, error) {
			return private.NewDispatcher(fw, providerName, mergedSchema, privateSurface)
		})
}
