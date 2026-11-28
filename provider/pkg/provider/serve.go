package provider

import (
	"github.com/pulumi/pulumi/pkg/v3/resource/provider"

	"github.com/pulumi/pulumi/sdk/v3/go/common/util/cmdutil"
	rpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// Wrap decorates the framework's server, e.g. with the private-API dispatcher
// (provider/pkg/private). A nil Wrap serves the framework as is.
type Wrap func(rpc.ResourceProviderServer) (rpc.ResourceProviderServer, error)

// Serve launches the gRPC server for the UniFi resource provider.
func Serve(providerName, version string, pulumiSchema, openapiDocBytes, metadataBytes []byte, wrap Wrap) {
	err := provider.Main(providerName, func(host *provider.HostClient) (rpc.ResourceProviderServer, error) {
		fw, err := makeProvider(host, providerName, version, pulumiSchema, openapiDocBytes, metadataBytes)
		if err != nil || wrap == nil {
			return fw, err
		}
		return wrap(fw)
	})
	if err != nil {
		cmdutil.ExitError(err.Error())
	}
}
