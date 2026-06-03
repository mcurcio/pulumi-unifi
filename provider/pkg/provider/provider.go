// Package provider implements the UniFi provider's callback hooks on top of the
// generic cloudy-sky-software REST CRUD framework. For the read-path MVP the
// only behavior we override is authentication (X-API-Key), API-key/site config,
// and injecting the site ID into the {siteId} path parameter. All CRUD lifecycle
// hooks fall through to the framework's Unimplemented defaults.
//
// The hand-written surface is split by responsibility:
//   - provider.go    assembly/lifecycle (the struct, makeProvider, GetGlobalPathParams)
//   - config.go      OnConfigure, apiHost validation, env-var fallback resolution
//   - auth.go        GetAuthorizationHeader
//   - pagination.go  OnPostInvoke list-page auto-aggregation
//   - transport.go   the allowInsecure transport
package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pulumi/pulumi/pkg/v3/resource/provider"

	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	fwCallback "github.com/cloudy-sky-software/pulumi-provider-framework/callback"
	fwRest "github.com/cloudy-sky-software/pulumi-provider-framework/rest"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// defaultSiteID is used when neither config nor env supplies a site ID. "default"
// is the conventional UniFi site identifier on a single-site controller.
const defaultSiteID = "default"

type unifiProvider struct {
	fwCallback.UnimplementedProviderCallback

	name    string
	version string

	apiKey        string
	siteID        string
	allowInsecure bool

	// handler is the framework REST provider this callback drives. It is set
	// after fwRest.MakeProvider returns (see makeProvider) and read by the
	// config/pagination hooks for transport injection and paginated GETs. Keeping
	// it on the struct (rather than a package global) makes the provider reentrant
	// and the callbacks unit-testable without save/restore of package state.
	handler *fwRest.Provider

	// metadata is the same crudMap/name-map bundle the framework runs on,
	// stashed so OnPostInvoke can resolve a token's read path for pagination.
	metadata openapigen.ProviderMetadata
}

func makeProvider(host *provider.HostClient, name, version string, pulumiSchemaBytes, openapiDocBytes, metadataBytes []byte) (pulumirpc.ResourceProviderServer, error) {
	p := &unifiProvider{
		name:    name,
		version: version,
		siteID:  defaultSiteID,
	}

	// Stash the metadata so OnPostInvoke can look up read paths for pagination.
	// The framework parses the same bytes internally but exposes no accessor.
	if err := json.Unmarshal(metadataBytes, &p.metadata); err != nil {
		return nil, fmt.Errorf("unmarshaling provider metadata: %w", err)
	}

	// p is itself the framework callback; pass it in, then capture the returned
	// handle on the struct so the hooks can reach the live HTTP client.
	rp, err := fwRest.MakeProvider(host, name, version, pulumiSchemaBytes, openapiDocBytes, metadataBytes, p)
	if err != nil {
		return nil, err
	}
	p.handler = rp.(*fwRest.Provider)
	return rp, nil
}

// GetGlobalPathParams supplies the {siteId} path parameter shared by all
// site-scoped endpoints so the framework can build request URLs.
func (p *unifiProvider) GetGlobalPathParams(_ context.Context, _ *pulumirpc.ConfigureRequest) (map[string]string, error) {
	return map[string]string{
		"siteId": p.siteID,
	}, nil
}
