package private

import (
	"context"
	"strings"
	"testing"

	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/internal/fakectl"
)

// The classic kinds through the gRPC surface (private-binding.md §S2.8): a
// classic collection round-trips with the classic envelope, and a settings
// singleton adopts on Create and is released on Delete with no request.
func TestDispatcherClassicKinds(t *testing.T) {
	ctx := context.Background()
	d, _, srv := newDispatcher(t)
	t.Setenv("UNIFI_APIKEY", "")
	t.Setenv("UNIFI_API_HOST", "")
	if _, err := d.Configure(ctx, &pulumirpc.ConfigureRequest{Variables: map[string]string{"unifi:config:networkBaseUrl": srv.URL}}); err != nil {
		t.Fatal(err)
	}
	crewURN := "urn:pulumi:dev::proj::" + fakectl.CrewToken + "::c"
	in := mustStruct(t, map[string]any{"name": "crew-1", "rateMax": 300})
	cr, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: crewURN, Properties: in})
	if err != nil || !strings.HasPrefix(cr.Id, "private:") || srv.Count("rest/crew") != 1 {
		t.Fatalf("classic create: %v %v", cr, err)
	}
	if _, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: crewURN, Properties: in}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate classic name: %v", err)
	}
	if _, err := d.Delete(ctx, &pulumirpc.DeleteRequest{Urn: crewURN, Id: cr.Id}); err != nil || srv.Count("rest/crew") != 0 {
		t.Fatalf("classic delete: %v", err)
	}

	tickURN := "urn:pulumi:dev::proj::" + fakectl.TickToken + "::tick"
	tin := mustStruct(t, map[string]any{"mode": "manual"})
	tc, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: tickURN, Properties: tin})
	if err != nil || tc.Id != "private:"+fakectl.TickID {
		t.Fatalf("singleton adopt: %v %v", tc, err)
	}
	srv.Requests()
	if _, err := d.Delete(ctx, &pulumirpc.DeleteRequest{Urn: tickURN, Id: tc.Id}); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("a singleton release sends no request, sent %d", n)
	}
	if o := srv.Object("rest/setting/tick", fakectl.TickID); o["mode"] != "manual" {
		t.Errorf("release must leave the settings as they are: %v", o)
	}
}
