//go:build integration

// Write-path integration test. Excluded from the default `make test` build (it
// needs a running mock); run with `make test-mock`, which brings up the Prism
// TLS mock and sets UNIFI_MOCK_ADDR.
//
// What it proves end-to-end through the real provider + framework code: the
// framework's Create / Update / Delete build spec-valid POST / PUT / DELETE
// requests for a managed resource and parse the responses — in particular that
// Create extracts the resource `id` from the create response body.
//
// What it cannot prove: Prism is **stateless**. It answers from static schema
// examples, so it never persists the created zone. Every Create returns the same
// example id; the Update/Delete that follow dispatch to the item route
// (.../firewall/zones/{id}) and parse a 200, but they do not mutate any server
// state, so this is a dispatch-and-parse test, not a true round-trip. The live
// round-trip is the infra-gated Tier-2 test (test/e2e/), tracked separately.
//
// Subject — deviation from the plan. The plan named FirewallPolicy as the write
// subject (the canonical full-CRUD resource). This uses **FirewallZone** instead
// because its request body is flat — `name` + `networkIds` (a uuid array) — and
// can be authored spec-valid by hand. FirewallPolicy's create body is deeply
// discriminated (action keyed by `type`, ipProtocolScope by `ipVersion`); a
// hand-written body cannot be made Prism-valid without empirical iteration, and
// Prism request-validation 422s on any violation. FirewallZone exercises exactly
// the same dispatch paths the plan cares about: POST-to-create (its crudMap has
// C≠P, so the framework uses POST), PUT-to-update (no PATCH endpoint, so the
// framework falls back to PUT on the item path), and DELETE. After B1 coalesces
// CRUD onto the discriminated variants, a variant like `Standard`
// (wifi/broadcasts) gains full CRUD and would be a natural addition here.
package provider

import (
	"context"
	"os"
	"testing"

	structpb "google.golang.org/protobuf/types/known/structpb"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// mockWriteResource is the managed-resource token exercised against the mock.
// Its crudMap is c=/v1/sites/{siteId}/firewall/zones (collection),
// r/d/p=/v1/sites/{siteId}/firewall/zones/{firewallZoneId} (item), no PATCH.
const mockWriteResource = "unifi:sites/v1:FirewallZone"

// mockWriteURN wraps the token in a Pulumi URN. GetResourceTypeToken parses the
// type out of the URN (urn:pulumi:<stack>::<project>::<type>::<name>), so the
// type segment must be exactly the resource token.
const mockWriteURN = "urn:pulumi:test::test::" + mockWriteResource + "::claude-a6-test-zone"

// exampleNetworkID is a syntactically valid uuid (the spec's own example for a
// network id). Prism validates `networkIds` items as format:uuid, so the value
// must parse even though the stateless mock never resolves it to a real network.
const exampleNetworkID = "dfb21062-8ea0-4dca-b1d8-1eb3da00e58b"

func TestWritePathAgainstMock(t *testing.T) {
	apiHost := os.Getenv("UNIFI_MOCK_ADDR")
	if apiHost == "" {
		t.Skip("UNIFI_MOCK_ADDR not set; run via `make test-mock`")
	}

	rp, err := makeProvider(
		nil, "unifi", "0.0.0-test",
		readArtifact(t, "schema.json"),
		readArtifact(t, "openapi_generated.yml"),
		readArtifact(t, "metadata.json"),
	)
	if err != nil {
		t.Fatalf("makeProvider: %v", err)
	}

	ctx := context.Background()
	// allowInsecure=true: the mock serves a self-signed cert and the framework's
	// default transport ignores SSL_CERT_FILE on darwin (it verifies against the
	// keychain), so the mock tier trusts the cert through the real A3 flag — the
	// same path the read test uses. Leaving SendsOldInputs unset keeps
	// engineSendsOldInputs=false, so Update sources its {firewallZoneId} path param
	// from Olds and Delete from Properties (CreatePutRequest/CreateDeleteRequest) —
	// see the per-call setup.
	if _, err := rp.Configure(ctx, &pulumirpc.ConfigureRequest{
		Variables: map[string]string{
			"unifi:config:apiKey":        "mock-api-key",
			"unifi:config:apiHost":       apiHost,
			"unifi:config:siteId":        mockSiteID,
			"unifi:config:allowInsecure": "true",
		},
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	mustMarshal := func(pm resource.PropertyMap) *structpb.Struct {
		t.Helper()
		s, err := plugin.MarshalProperties(pm, plugin.MarshalOptions{})
		if err != nil {
			t.Fatalf("marshal properties: %v", err)
		}
		return s
	}

	networkIds := resource.NewArrayProperty([]resource.PropertyValue{
		resource.NewStringProperty(exampleNetworkID),
	})

	// Create: POST /v1/sites/<mockSiteID>/firewall/zones with {name, networkIds}.
	// The collection path carries only {siteId} (supplied globally), so no id is
	// needed yet. The framework extracts `id` from the Firewall_zone response.
	createProps := resource.PropertyMap{
		"name":       resource.NewStringProperty("claude-a6-test-zone"),
		"networkIds": networkIds,
	}
	cresp, err := rp.Create(ctx, &pulumirpc.CreateRequest{
		Urn:        mockWriteURN,
		Properties: mustMarshal(createProps),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := cresp.GetId()
	if id == "" {
		t.Fatal("Create returned an empty id; the framework could not extract `id` from the create response")
	}

	// Update: no PATCH endpoint, so the framework PUTs the full body to the item
	// path. {firewallZoneId} is resolved from the OLD state (CreatePutRequest is
	// called with oldState), so id must live in Olds. News is the new body.
	oldState := resource.PropertyMap{
		"id":         resource.NewStringProperty(id),
		"name":       resource.NewStringProperty("claude-a6-test-zone"),
		"networkIds": networkIds,
	}
	newState := resource.PropertyMap{
		"name":       resource.NewStringProperty("claude-a6-test-zone-renamed"),
		"networkIds": networkIds,
	}
	if _, err := rp.Update(ctx, &pulumirpc.UpdateRequest{
		Urn:       mockWriteURN,
		Id:        id,
		Olds:      mustMarshal(oldState),
		News:      mustMarshal(newState),
		OldInputs: mustMarshal(oldState),
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Delete: DELETE the item path. {firewallZoneId} is resolved from Properties
	// (CreateDeleteRequest is called with the delete inputs), so id must live
	// there too.
	if _, err := rp.Delete(ctx, &pulumirpc.DeleteRequest{
		Urn:        mockWriteURN,
		Id:         id,
		Properties: mustMarshal(oldState),
		OldInputs:  mustMarshal(oldState),
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
