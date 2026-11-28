//go:build integration

// ACL rule write dispatch against the Prism TLS mock (`make test-mock`), under
// the slice-2 tokens AclRuleIpv4 / AclRuleMac (docs/design/private-binding.md
// §S2.3). Like TestWritePathAgainstMock this is dispatch-and-parse only: Prism
// validates each request against the spec and answers from examples, so it
// proves the framework sends spec-valid POST / PUT / DELETE bodies for both
// variants (the discriminator const injected, AclRuleMac's networkIdFilter
// present, no `index`), not a round trip. The live round trip is Tier 2.
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

func TestACLRuleWritesAgainstMock(t *testing.T) {
	apiHost := os.Getenv("UNIFI_MOCK_ADDR")
	if apiHost == "" {
		t.Skip("UNIFI_MOCK_ADDR not set; run via `make test-mock`")
	}
	rp, err := makeProvider(nil, "unifi", "0.0.0-test",
		readArtifact(t, "schema.json"), readArtifact(t, "openapi_generated.yml"), readArtifact(t, "metadata.json"))
	if err != nil {
		t.Fatalf("makeProvider: %v", err)
	}
	ctx := context.Background()
	if _, err := rp.Configure(ctx, &pulumirpc.ConfigureRequest{Variables: map[string]string{
		"unifi:config:apiKey": "mock-api-key", "unifi:config:apiHost": apiHost,
		"unifi:config:siteId": mockSiteID, "unifi:config:allowInsecure": "true",
	}}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	mustMarshal := func(pm resource.PropertyMap) *structpb.Struct {
		t.Helper()
		s, err := plugin.MarshalProperties(pm, plugin.MarshalOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, c := range []struct {
		token string
		props resource.PropertyMap
	}{
		{"unifi:sites/v1:AclRuleIpv4", resource.PropertyMap{
			"type": resource.NewStringProperty("IPV4"), "name": resource.NewStringProperty("mock-acl-ipv4"),
			"enabled": resource.NewBoolProperty(true), "action": resource.NewStringProperty("BLOCK"),
		}},
		{"unifi:sites/v1:AclRuleMac", resource.PropertyMap{
			"type": resource.NewStringProperty("MAC"), "name": resource.NewStringProperty("mock-acl-mac"),
			"enabled": resource.NewBoolProperty(true), "action": resource.NewStringProperty("ALLOW"),
			"networkIdFilter": resource.NewStringProperty(exampleNetworkID),
		}},
	} {
		t.Run(c.token, func(t *testing.T) {
			urn := "urn:pulumi:test::test::" + c.token + "::acl"
			cr, err := rp.Create(ctx, &pulumirpc.CreateRequest{Urn: urn, Properties: mustMarshal(c.props)})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			id := cr.GetId()
			if id == "" {
				t.Fatal("Create returned no id")
			}
			old := c.props.Copy()
			old["id"] = resource.NewStringProperty(id)
			upd := c.props.Copy()
			upd["enabled"] = resource.NewBoolProperty(false)
			if _, err := rp.Update(ctx, &pulumirpc.UpdateRequest{Urn: urn, Id: id, Olds: mustMarshal(old),
				News: mustMarshal(upd), OldInputs: mustMarshal(old)}); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if _, err := rp.Delete(ctx, &pulumirpc.DeleteRequest{Urn: urn, Id: id, Properties: mustMarshal(old),
				OldInputs: mustMarshal(old)}); err != nil {
				t.Fatalf("Delete: %v", err)
			}
		})
	}
}
