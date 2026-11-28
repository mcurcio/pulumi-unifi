//go:build integration

// AclRuleOrdering against the Prism TLS mock (`make test-mock`): the gRPC
// calls go through the dispatcher, the engine (the site-keyed leadingBlock singleton), the
// official binding and the controller client (TLS via allowInsecure, the key
// header) to the mock (docs/design/private-binding.md §S2.11). Prism keeps no
// state and validates requests against the spec, so this proves wire and shape
// (the GET is decoded, the PUT body is spec-valid), not the ordering contract;
// that is the fake-controller test (dispatch_singleton_test.go) and Tier 2.
package dispatch

import (
	"context"
	"os"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

func TestOrderingAgainstMock(t *testing.T) {
	apiHost := os.Getenv("UNIFI_MOCK_ADDR")
	if apiHost == "" {
		t.Skip("UNIFI_MOCK_ADDR not set; run via `make test-mock`")
	}
	const mockSite = "00000000-0000-0000-0000-000000000000" // a UUID: no site lookup
	d, err := NewDispatcher(&fakeFramework{}, "unifi", []byte(`{}`), []byte(orderingSurface))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := d.Configure(ctx, &pulumirpc.ConfigureRequest{Variables: map[string]string{
		"unifi:config:apiKey": "mock-api-key", "unifi:config:apiHost": apiHost,
		"unifi:config:siteId": mockSite, "unifi:config:allowInsecure": "true",
	}}); err != nil {
		t.Fatal(err)
	}
	in := mustStruct(t, map[string]any{"orderedAclRuleIds": []any{"dfb21062-8ea0-4dca-b1d8-1eb3da00e58b"}, "unlisted": "LAST"})
	cr, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: orderingURN, Properties: in})
	if err != nil {
		t.Fatalf("Create (GET + spec-valid PUT): %v", err)
	}
	if cr.Id != "official:"+mockSite {
		t.Errorf("stored id %s", cr.Id)
	}
	out, _ := plugin.UnmarshalProperties(cr.Properties, plugin.MarshalOptions{})
	if !out["effectiveOrder"].IsArray() {
		t.Errorf("effectiveOrder not decoded: %v", out)
	}
	if _, err := d.Read(ctx, &pulumirpc.ReadRequest{Urn: orderingURN, Id: cr.Id, Inputs: in, Properties: cr.Properties}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := d.Update(ctx, &pulumirpc.UpdateRequest{Urn: orderingURN, Id: cr.Id, News: in}); err != nil {
		t.Fatalf("Update: %v", err)
	}
}
