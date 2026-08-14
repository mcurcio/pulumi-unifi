//go:build e2e

// Tier-2 live e2e test. Excluded from every default build (the `e2e` tag is set
// only by `make test-e2e`), and skipped at runtime unless UNIFI_E2E_ADDR points
// at a live controller. It drives the REAL provider + framework over the wire
// against a real UniFi OS Server restored from the committed seed snapshot
// (test/e2e/unifi-seed.tgz), so it proves what the stateless Prism mock cannot:
// real auth (the minted X-API-Key actually authorizes), real decode of live
// data, and a TRUE create→read→update→delete round-trip with persisted state.
//
// Harness: identical provider construction to the Tier-1 integration tests
// (makeProvider + readArtifact, Configure with allowInsecure=true). The only
// differences are the env gate (UNIFI_E2E_ADDR), the live apiKey
// (UNIFI_APIKEY, sourced from test/e2e/seed.env by the Makefile), and that the
// CRUD methods hit a STATEFUL controller — so Read after Create returns the
// thing we made, and Diff with identical inputs must report DIFF_NONE.
//
// Assumptions about the controller: a BARE controller (no adopted devices),
// freshly restored from seed. The CRUD subject (DnsARecord) has no foreign keys
// — it needs only a {siteId} and a self-contained body — so it works on an empty
// site. Run via `make test-e2e`.
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

// e2ePlaceholderSiteID is a harmless stand-in used only by provider calls that do
// NOT substitute {siteId} (getCountry, listSiteOverviews). The REAL controller
// rejects the "default" alias on siteId-scoped paths ("'default' is not a valid
// 'siteId' value") — it requires the site's UUID — so TestE2ECRUDRoundTrip
// discovers the real id at runtime (discoverSiteID) instead of hardcoding it.
// (The Prism mock accepted "default"; the live controller does not.)
const e2ePlaceholderSiteID = "default"

// Live tokens. getCountry has no path params (isolates auth+decode); the
// DnsARecord resource is the simplest discriminated write variant — no foreign
// keys, a self-contained body — so it round-trips on an empty site.
const (
	e2eReadDataSource = "unifi:countries/v1:getCountry"
	e2eDnsRecordTok   = "unifi:sites/v1:DnsARecord"
)

// newE2EProvider builds the real provider and Configures it against the live
// controller (addr from UNIFI_E2E_ADDR, key from UNIFI_APIKEY). It skips the
// test if either is unset, so a bare `go test -tags e2e ./...` (e.g. the static
// compile check) does not require a controller.
func newE2EProvider(t *testing.T) (pulumirpc.ResourceProviderServer, context.Context) {
	return newConfiguredProvider(t, e2ePlaceholderSiteID)
}

// newConfiguredProvider builds the real provider and Configures it against the
// live controller (addr from UNIFI_E2E_ADDR, key from UNIFI_APIKEY), scoped to
// siteID. It skips the test if either env var is unset, so a bare `go test -tags
// e2e ./...` (e.g. the static compile check) does not require a controller.
func newConfiguredProvider(t *testing.T, siteID string) (pulumirpc.ResourceProviderServer, context.Context) {
	t.Helper()

	addr := os.Getenv("UNIFI_E2E_ADDR")
	if addr == "" {
		t.Skip("UNIFI_E2E_ADDR not set; run via `make test-e2e`")
	}
	apiKey := os.Getenv("UNIFI_APIKEY")
	if apiKey == "" {
		t.Skip("UNIFI_APIKEY not set; run via `make test-e2e` (sources test/e2e/seed.env)")
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
	// allowInsecure=true: the controller serves a self-signed cert. The CA-pinned
	// secure path has separate (no-Docker) coverage; this live tier focuses on the
	// CRUD round-trip, so it trusts the cert via the product flag like Tier-1.
	if _, err := rp.Configure(ctx, &pulumirpc.ConfigureRequest{
		Variables: map[string]string{
			"unifi:config:apiKey":        apiKey,
			"unifi:config:apiHost":       addr,
			"unifi:config:siteId":        siteID,
			"unifi:config:allowInsecure": "true",
		},
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	return rp, ctx
}

// discoverSiteID resolves the live controller's real site UUID by invoking the
// siteId-less listSiteOverviews data source and taking the first site's id. The
// Integration API requires the UUID (not the "default" alias) on siteId-scoped
// paths, and the id is controller/seed-specific, so the CRUD test discovers it at
// runtime rather than hardcoding — keeping the test correct across seed re-bakes.
func discoverSiteID(t *testing.T, rp pulumirpc.ResourceProviderServer, ctx context.Context) string {
	t.Helper()
	resp, err := rp.Invoke(ctx, &pulumirpc.InvokeRequest{
		Tok:  "unifi:sites/v1:listSiteOverviews",
		Args: mustMarshalE2E(t, resource.PropertyMap{}),
	})
	if err != nil {
		t.Fatalf("Invoke(listSiteOverviews): %v", err)
	}
	if fs := resp.GetFailures(); len(fs) > 0 {
		t.Fatalf("Invoke(listSiteOverviews) returned failures: %v", fs)
	}
	out, err := plugin.UnmarshalProperties(resp.GetReturn(), plugin.MarshalOptions{})
	if err != nil {
		t.Fatalf("unmarshal listSiteOverviews return: %v", err)
	}
	data, ok := out["data"]
	if !ok || !data.IsArray() || len(data.ArrayValue()) == 0 {
		t.Fatalf("listSiteOverviews returned no sites (got %v); cannot scope the CRUD test", out)
	}
	first := data.ArrayValue()[0]
	if !first.IsObject() {
		t.Fatalf("listSiteOverviews data[0] is not an object: %v", first)
	}
	id := first.ObjectValue()["id"]
	if !id.IsString() || id.StringValue() == "" {
		t.Fatalf("listSiteOverviews data[0].id missing/empty: %v", first)
	}
	return id.StringValue()
}

func mustMarshalE2E(t *testing.T, pm resource.PropertyMap) *structpb.Struct {
	t.Helper()
	s, err := plugin.MarshalProperties(pm, plugin.MarshalOptions{})
	if err != nil {
		t.Fatalf("marshal properties: %v", err)
	}
	return s
}

// TestE2ELiveRead invokes a data source against the live controller and asserts a
// non-empty decoded result — end-to-end proof that the minted X-API-Key
// authorizes, the URL composes, TLS connects, and the JSON decodes. This is the
// minimal "the provider talks to the real API" gate.
func TestE2ELiveRead(t *testing.T) {
	rp, ctx := newE2EProvider(t)

	args := mustMarshalE2E(t, resource.PropertyMap{})
	resp, err := rp.Invoke(ctx, &pulumirpc.InvokeRequest{Tok: e2eReadDataSource, Args: args})
	if err != nil {
		t.Fatalf("Invoke(%s): %v", e2eReadDataSource, err)
	}
	if fs := resp.GetFailures(); len(fs) > 0 {
		t.Fatalf("Invoke(%s) returned failures: %v", e2eReadDataSource, fs)
	}
	out, err := plugin.UnmarshalProperties(resp.GetReturn(), plugin.MarshalOptions{})
	if err != nil {
		t.Fatalf("unmarshal return: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("expected a non-empty decoded response from %s; the live controller returned nothing", e2eReadDataSource)
	}
}

// TestE2ECRUDRoundTrip is the true write round-trip the Prism mock cannot do.
// It creates a DnsARecord, reads it back, asserts a no-op second-up (Diff with
// identical inputs => DIFF_NONE), updates the IPv4 address, reads the change,
// deletes it, and confirms it is gone. Each step uses the gRPC lifecycle
// methods directly (no full `pulumi up`).
//
// DnsARecord is the chosen subject because it is the simplest discriminated
// variant: a flat, self-contained body (enabled/domain/ipv4Address, with the
// `type` discriminator riding along as the A_RECORD const) and NO foreign keys
// (no networkIds / zone refs), so it provisions on a bare site with nothing else
// present.
func TestE2ECRUDRoundTrip(t *testing.T) {
	// The live API rejects the "default" siteId alias on siteId-scoped paths, so
	// discover the controller's real site UUID and scope a provider to it.
	disc, ctx := newE2EProvider(t)
	siteID := discoverSiteID(t, disc, ctx)
	rp, ctx := newConfiguredProvider(t, siteID)

	const domain = "pulumi-e2e.example.com"
	urn := "urn:pulumi:test::test::" + e2eDnsRecordTok + "::pulumi-e2e-a-record"

	// --- Create -------------------------------------------------------------
	// `type` is the discriminator: schema.json pins it to const+default
	// "A_RECORD" (discriminatorInjectPass), which the *generated SDK* materializes
	// and sends (dns_a_record.py: `if type is None: type = 'A_RECORD'`). The
	// framework does NOT apply Pulumi schema defaults (its Check only auto-names),
	// so a gRPC-level caller like this test must supply the discriminator itself to
	// mirror exactly what the provider receives from the SDK in production.
	// ttlSeconds is required by the per-variant body (IntegrationDnsARecordCreateUpdateDto)
	// even though the parent Create_or_update_DNS_policy schema — which the framework
	// validates against client-side — does not list it; the controller enforces it
	// (400 "ttlSeconds must not be null" without it).
	createInputs := resource.PropertyMap{
		"type":        resource.NewStringProperty("A_RECORD"),
		"enabled":     resource.NewBoolProperty(true),
		"domain":      resource.NewStringProperty(domain),
		"ipv4Address": resource.NewStringProperty("10.255.0.1"),
		"ttlSeconds":  resource.NewNumberProperty(3600),
	}
	cresp, err := rp.Create(ctx, &pulumirpc.CreateRequest{
		Urn:        urn,
		Properties: mustMarshalE2E(t, createInputs),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := cresp.GetId()
	if id == "" {
		t.Fatal("Create returned an empty id; the framework could not extract `id` from the create response")
	}

	// Best-effort cleanup if a later assertion aborts before the explicit Delete.
	created := true
	defer func() {
		if created {
			_, _ = rp.Delete(ctx, &pulumirpc.DeleteRequest{
				Urn:        urn,
				Id:         id,
				Properties: mustMarshalE2E(t, withID(createInputs, id)),
				OldInputs:  mustMarshalE2E(t, withID(createInputs, id)),
			})
		}
	}()

	// --- Read (round-trips the created state) -------------------------------
	rresp, err := rp.Read(ctx, &pulumirpc.ReadRequest{
		Urn:        urn,
		Id:         id,
		Properties: mustMarshalE2E(t, withID(createInputs, id)),
	})
	if err != nil {
		t.Fatalf("Read after Create: %v", err)
	}
	if rresp.GetId() == "" {
		t.Fatal("Read after Create returned an empty id; the record did not persist")
	}
	readProps, err := plugin.UnmarshalProperties(rresp.GetProperties(), plugin.MarshalOptions{})
	if err != nil {
		t.Fatalf("unmarshal Read properties: %v", err)
	}
	if got := readProps["domain"]; !got.IsString() || got.StringValue() != domain {
		t.Fatalf("Read domain = %v, want %q (state did not round-trip)", got, domain)
	}
	if got := readProps["ipv4Address"]; !got.IsString() || got.StringValue() != "10.255.0.1" {
		t.Fatalf("Read ipv4Address = %v, want %q", got, "10.255.0.1")
	}

	// --- Diff with identical inputs => no diff (the second-up no-op invariant) -
	dresp, err := rp.Diff(ctx, &pulumirpc.DiffRequest{
		Urn:       urn,
		Id:        id,
		Olds:      rresp.GetProperties(),
		News:      mustMarshalE2E(t, createInputs),
		OldInputs: mustMarshalE2E(t, createInputs),
	})
	if err != nil {
		t.Fatalf("Diff (no-op): %v", err)
	}
	if dresp.GetChanges() == pulumirpc.DiffResponse_DIFF_SOME {
		t.Fatalf("Diff with identical inputs reported DIFF_SOME (diffs=%v, replaces=%v); a second `pulumi up` would not be a no-op",
			dresp.GetDiffs(), dresp.GetReplaces())
	}

	// --- Update (change the IPv4 address) -----------------------------------
	updatedInputs := resource.PropertyMap{
		"type":        resource.NewStringProperty("A_RECORD"),
		"enabled":     resource.NewBoolProperty(true),
		"domain":      resource.NewStringProperty(domain),
		"ipv4Address": resource.NewStringProperty("10.255.0.2"),
		"ttlSeconds":  resource.NewNumberProperty(3600),
	}
	if _, err := rp.Update(ctx, &pulumirpc.UpdateRequest{
		Urn:       urn,
		Id:        id,
		Olds:      mustMarshalE2E(t, withID(createInputs, id)),
		News:      mustMarshalE2E(t, updatedInputs),
		OldInputs: mustMarshalE2E(t, withID(createInputs, id)),
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Read back the change.
	rresp2, err := rp.Read(ctx, &pulumirpc.ReadRequest{
		Urn:        urn,
		Id:         id,
		Properties: mustMarshalE2E(t, withID(updatedInputs, id)),
	})
	if err != nil {
		t.Fatalf("Read after Update: %v", err)
	}
	readProps2, err := plugin.UnmarshalProperties(rresp2.GetProperties(), plugin.MarshalOptions{})
	if err != nil {
		t.Fatalf("unmarshal Read-after-Update properties: %v", err)
	}
	if got := readProps2["ipv4Address"]; !got.IsString() || got.StringValue() != "10.255.0.2" {
		t.Fatalf("Read after Update ipv4Address = %v, want %q (Update did not persist)", got, "10.255.0.2")
	}

	// --- Delete + confirm gone ----------------------------------------------
	if _, err := rp.Delete(ctx, &pulumirpc.DeleteRequest{
		Urn:        urn,
		Id:         id,
		Properties: mustMarshalE2E(t, withID(updatedInputs, id)),
		OldInputs:  mustMarshalE2E(t, withID(updatedInputs, id)),
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	created = false // explicit delete succeeded; skip the deferred cleanup

	// A Read of the deleted resource must report absence. The framework signals a
	// deleted resource by returning an empty id (Pulumi's "resource no longer
	// exists" convention); a non-error 404 maps to that. An error here is also
	// acceptable evidence the record is gone.
	rresp3, err := rp.Read(ctx, &pulumirpc.ReadRequest{
		Urn:        urn,
		Id:         id,
		Properties: mustMarshalE2E(t, withID(updatedInputs, id)),
	})
	if err == nil && rresp3.GetId() != "" {
		t.Fatalf("Read after Delete still returned id %q; the record was not deleted", rresp3.GetId())
	}
}

// withID returns a copy of pm with the resource id added — the item-path CRUD
// calls (Read/Update/Delete) resolve {dnsPolicyId} from the state, so id must be
// present in the properties they receive.
func withID(pm resource.PropertyMap, id string) resource.PropertyMap {
	out := resource.PropertyMap{"id": resource.NewStringProperty(id)}
	for k, v := range pm {
		out[k] = v
	}
	return out
}
