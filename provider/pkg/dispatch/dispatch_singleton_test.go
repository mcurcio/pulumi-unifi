package dispatch

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/internal/fakectl"
)

// orderingSurface is the PRIVATE=0 surface, as private/gen.EmptySurface
// writes it (checked there by TestNoModelIsGolden): one official kind, a
// singleton keyed by its site with one leadingBlock field.
const orderingSurface = `{"kinds":[{"api":"official","collection":"","createPath":"","envelope":"","fields":` +
	`{"orderedAclRuleIds":{"items":{"type":"string"},"merge":"leadingBlock","property":"orderedAclRuleIds",` +
	`"required":true,"type":"array","wire":"orderedAclRuleIds"}},"idField":"","itemGet":true,` +
	`"itemPath":"/v1/sites/{siteId}/acl-rules/ordering","keyedBySite":true,"kind":"","listGet":false,` +
	`"listPath":"","naturalKey":null,"shape":"singleton","token":"unifi:sites/v1:AclRuleOrdering"}],"source":{}}`

const (
	orderingURN = "urn:pulumi:dev::proj::unifi:sites/v1:AclRuleOrdering::order"
	testKey     = "test-key-not-a-secret"
	siteUUID    = "8c7d8f04-0000-4000-8000-000000000001"
)

// newOrderingDispatcher serves AclRuleOrdering from the PRIVATE=0 surface against the fake official API over TLS,
// configured the way a user would: apiHost, apiKey, allowInsecure, a site name.
func newOrderingDispatcher(t *testing.T, order ...string) (*Dispatcher, *fakeFramework, *fakectl.Official) {
	t.Helper()
	off := fakectl.NewOfficial(testKey, siteUUID, "default", order...)
	t.Cleanup(off.Close)
	fw := &fakeFramework{}
	d, err := NewDispatcher(fw, "unifi", []byte(`{"name":"unifi"}`), []byte(orderingSurface))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Configure(context.Background(), &pulumirpc.ConfigureRequest{Variables: map[string]string{
		"unifi:config:apiKey": testKey, "unifi:config:apiHost": off.Host(),
		"unifi:config:allowInsecure": "true", "unifi:config:siteId": "default",
	}}); err != nil {
		t.Fatal(err)
	}
	return d, fw, off
}

func idsOf(t *testing.T, pmap resource.PropertyMap, key string) []string {
	t.Helper()
	var out []string
	for _, v := range pmap[resource.PropertyKey(key)].ArrayValue() {
		out = append(out, v.StringValue())
	}
	return out
}

func TestOrderingLifecycleThroughTheDispatcher(t *testing.T) {
	ctx := context.Background()
	d, fw, off := newOrderingDispatcher(t, "r1", "u1", "r2")

	// Check fills the default and rejects a stored private id.
	chk, err := d.Check(ctx, &pulumirpc.CheckRequest{Urn: orderingURN,
		News: mustStruct(t, map[string]any{"orderedAclRuleIds": []any{"r2", "private:65f0"}})})
	if err != nil {
		t.Fatal(err)
	}
	if len(chk.Failures) != 1 || !strings.Contains(chk.Failures[0].Reason, "aclRuleId") {
		t.Fatalf("check failures %v", chk.Failures)
	}
	chk, _ = d.Check(ctx, &pulumirpc.CheckRequest{Urn: orderingURN,
		News: mustStruct(t, map[string]any{"orderedAclRuleIds": []any{"r2", "r1"}})})
	in, _ := plugin.UnmarshalProperties(chk.GetInputs(), plugin.MarshalOptions{})
	if len(chk.Failures) != 0 || in["unlisted"].StringValue() != "LAST" {
		t.Fatalf("check %v inputs %v", chk.Failures, in)
	}

	// Create adopts: managed block first, the unmanaged rule kept in the tail.
	cr, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: orderingURN, Properties: chk.GetInputs()})
	if err != nil {
		t.Fatal(err)
	}
	if cr.Id != "official:"+siteUUID {
		t.Errorf("stored id %s", cr.Id)
	}
	if got := off.Order(); !reflect.DeepEqual(got, []string{"r2", "r1", "u1"}) {
		t.Errorf("live order %v", got)
	}
	if off.SiteLists != 1 {
		t.Errorf("site name resolved with %d lists, want 1", off.SiteLists)
	}
	if len(fw.calls) != 1 || fw.calls[0] != "Configure" {
		t.Errorf("the framework saw %v; a singleton never reaches it", fw.calls)
	}

	// Refresh is a no-op while the managed block leads.
	rd, err := d.Read(ctx, &pulumirpc.ReadRequest{Urn: orderingURN, Id: cr.Id, Inputs: chk.GetInputs(), Properties: cr.Properties})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := d.Diff(ctx, &pulumirpc.DiffRequest{Urn: orderingURN, Id: cr.Id, OldInputs: rd.Inputs, Olds: rd.Properties, News: chk.GetInputs()})
	if err != nil || diff.Changes != pulumirpc.DiffResponse_DIFF_NONE {
		t.Fatalf("no-op diff %v %v", diff, err)
	}

	// An out-of-band move of the unmanaged rule ahead shows as drift.
	off.SetOrder("u1", "r2", "r1")
	rd, _ = d.Read(ctx, &pulumirpc.ReadRequest{Urn: orderingURN, Id: cr.Id, Inputs: chk.GetInputs(), Properties: cr.Properties})
	rin, _ := plugin.UnmarshalProperties(rd.Inputs, plugin.MarshalOptions{})
	if got := idsOf(t, rin, "orderedAclRuleIds"); !reflect.DeepEqual(got, []string{"u1", "r2"}) {
		t.Errorf("projected %v", got)
	}
	diff, _ = d.Diff(ctx, &pulumirpc.DiffRequest{Urn: orderingURN, Id: cr.Id, OldInputs: rd.Inputs, Olds: rd.Properties, News: chk.GetInputs()})
	if diff.Changes != pulumirpc.DiffResponse_DIFF_SOME || len(diff.Replaces) != 0 {
		t.Errorf("drift diff %v", diff)
	}

	// Update reorders, keeping the unmanaged rule in the tail.
	news := mustStruct(t, map[string]any{"orderedAclRuleIds": []any{"r1", "r2"}, "unlisted": "LAST"})
	up, err := d.Update(ctx, &pulumirpc.UpdateRequest{Urn: orderingURN, Id: cr.Id, News: news})
	if err != nil {
		t.Fatal(err)
	}
	if got := off.Order(); !reflect.DeepEqual(got, []string{"r1", "r2", "u1"}) {
		t.Errorf("live order after update %v", got)
	}
	upOut, _ := plugin.UnmarshalProperties(up.Properties, plugin.MarshalOptions{})
	if got := idsOf(t, upOut, "effectiveOrder"); !reflect.DeepEqual(got, []string{"r1", "r2", "u1"}) {
		t.Errorf("effectiveOrder %v", got)
	}

	// A rule created elsewhere between GET and PUT: one re-read and retry.
	first := true
	off.BeforePut = func(o *fakectl.Official) {
		if first {
			first = false
			o.SetOrder(append(o.Order(), "late")...)
		}
	}
	if _, err := d.Update(ctx, &pulumirpc.UpdateRequest{Urn: orderingURN, Id: cr.Id, News: news}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	off.BeforePut = nil

	// An id that is not live: the server's answer is shown.
	bad := mustStruct(t, map[string]any{"orderedAclRuleIds": []any{"nope"}, "unlisted": "LAST"})
	if _, err := d.Update(ctx, &pulumirpc.UpdateRequest{Urn: orderingURN, Id: cr.Id, News: bad}); err == nil ||
		!strings.Contains(err.Error(), "invalid-reference") {
		t.Errorf("unknown id: %v", err)
	}

	// Import by site name adopts the whole order.
	imp, err := d.Read(ctx, &pulumirpc.ReadRequest{Urn: orderingURN, Id: "default"})
	if err != nil || imp.Id != "official:"+siteUUID {
		t.Fatalf("import %v %v", imp, err)
	}

	// Delete releases: no PUT, the order stays.
	puts, before := off.Puts, off.Order()
	if _, err := d.Delete(ctx, &pulumirpc.DeleteRequest{Urn: orderingURN, Id: cr.Id}); err != nil {
		t.Fatal(err)
	}
	if off.Puts != puts || !reflect.DeepEqual(off.Order(), before) {
		t.Error("delete must leave the order as it is")
	}
}

func TestOrderingNeedsAKey(t *testing.T) {
	off := fakectl.NewOfficial(testKey, siteUUID, "default", "r1")
	defer off.Close()
	d, err := NewDispatcher(&fakeFramework{}, "unifi", []byte(`{}`), []byte(orderingSurface))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = d.Configure(context.Background(), &pulumirpc.ConfigureRequest{Variables: map[string]string{
		"unifi:config:apiHost": off.Host(), "unifi:config:allowInsecure": "true"}})
	_, err = d.Create(context.Background(), &pulumirpc.CreateRequest{Urn: orderingURN,
		Properties: mustStruct(t, map[string]any{"orderedAclRuleIds": []any{"r1"}})})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("err %v", err)
	}
}
