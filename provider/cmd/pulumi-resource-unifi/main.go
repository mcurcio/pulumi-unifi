// pulumi-resource-unifi is the UniFi Pulumi provider plugin binary. It embeds
// the generated package schema, CRUD metadata, and fixed OpenAPI doc, then
// serves them through the cloudy-sky-software REST CRUD framework.
package main

import (
	_ "embed"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/provider"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/version"
)

const providerName = "unifi"

//go:embed schema.json
var pulumiSchema []byte

//go:embed openapi_generated.yml
var openapiDocBytes []byte

//go:embed metadata.json
var metadataBytes []byte

func main() {
	provider.Serve(providerName, version.Version, pulumiSchema, openapiDocBytes, metadataBytes)
}
