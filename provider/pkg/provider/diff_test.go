package provider

import (
	"context"
	"sort"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
	"google.golang.org/protobuf/types/known/structpb"
)

// TestDiffVariantFieldsUpdateInPlace runs Diff through the framework over the
// generated artifacts: a change to a variant-only field of a discriminated
// resource (AclRuleIpv4.protocolFilter dropped, AclRuleMac.networkIdFilter
// changed, DnsARecord.ipv4Address changed) is an in-place update, as the PUT
// accepts it; only the replaceOnChanges inputs replace. Before OnDiff the
// framework looked only at the shared base's properties and replaced
// (measured live on AclRuleIpv4, slice 2 step 1).
func TestDiffVariantFieldsUpdateInPlace(t *testing.T) {
	rp, err := makeProvider(nil, "unifi", "0.0.0-test",
		readArtifact(t, "schema.json"), readArtifact(t, "openapi_generated.yml"), readArtifact(t, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := func(pm map[string]any) *structpb.Struct {
		s, err := plugin.MarshalProperties(resource.NewPropertyMapFromMap(pm), plugin.MarshalOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, c := range []struct {
		tok        string
		olds, news map[string]any
		replaces   []string
	}{
		{"unifi:sites/v1:AclRuleIpv4",
			map[string]any{"type": "IPV4", "name": "a", "enabled": true, "action": "BLOCK", "protocolFilter": []any{"TCP"}},
			map[string]any{"type": "IPV4", "name": "a", "enabled": true, "action": "ALLOW"}, nil},
		{"unifi:sites/v1:AclRuleMac",
			map[string]any{"type": "MAC", "name": "a", "enabled": true, "action": "ALLOW", "networkIdFilter": "n1"},
			map[string]any{"type": "MAC", "name": "b", "enabled": true, "action": "ALLOW", "networkIdFilter": "n2"}, nil},
		{"unifi:sites/v1:DnsARecord",
			map[string]any{"type": "A_RECORD", "domain": "a.test", "ipv4Address": "10.0.0.1", "ttlSeconds": 60, "enabled": true},
			map[string]any{"type": "A_RECORD", "domain": "a.test", "ipv4Address": "10.0.0.2", "ttlSeconds": 60, "enabled": true}, nil},
	} {
		resp, err := rp.Diff(context.Background(), &pulumirpc.DiffRequest{Urn: "urn:pulumi:t::p::" + c.tok + "::x", Id: "id-1",
			OldInputs: m(c.olds), Olds: m(c.olds), News: m(c.news)})
		if err != nil {
			t.Fatalf("%s: %v", c.tok, err)
		}
		got := append([]string(nil), resp.GetReplaces()...)
		sort.Strings(got)
		if resp.GetChanges() != pulumirpc.DiffResponse_DIFF_SOME || len(got) != len(c.replaces) {
			t.Errorf("%s: changes %v replaces %v diffs %v; want an in-place update", c.tok, resp.GetChanges(), got, resp.GetDiffs())
		}
	}
}
