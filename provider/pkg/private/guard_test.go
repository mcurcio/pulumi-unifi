package private

import (
	"context"
	"errors"
	"strings"
	"testing"

	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// The version guard (docs/design/private-binding.md §S2.6): the first private
// call reads stat/sysinfo once; a controller on another Network version fails
// every private kind with both versions named, and leaves the official side
// alone.
func TestVersionGuard(t *testing.T) {
	ctx := context.Background()
	d, _, srv := newDispatcher(t)
	t.Setenv("UNIFI_APIKEY", "")
	t.Setenv("UNIFI_API_HOST", "")
	t.Setenv("UNIFI_NETWORK_BASE_URL", "")
	cfg := &pulumirpc.ConfigureRequest{Variables: map[string]string{
		"unifi:config:networkBaseUrl": srv.URL, "unifi:config:apiKey": "k-not-real"}}
	if _, err := d.Configure(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	in := mustStruct(t, map[string]any{"name": "t1"})

	// Matching version: one sysinfo read, then the calls go through.
	for i := 0; i < 2; i++ {
		if _, err := d.Read(ctx, &pulumirpc.ReadRequest{Urn: thingURN, Id: "t1"}); err == nil {
			t.Fatal("import of a missing object must fail")
		} else if strings.Contains(err.Error(), "UniFi Network") {
			t.Fatalf("guard tripped on the matching version: %v", err)
		}
	}
	n := 0
	for _, r := range srv.Requests() {
		if strings.HasSuffix(r.Path, "/stat/sysinfo") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("stat/sysinfo read %d times, want once", n)
	}

	// Another version: refused, with both versions; nothing written.
	srv.NetworkVersion = "10.9.1"
	if _, err := d.Configure(ctx, cfg); err != nil { // a new Configure re-arms the guard
		t.Fatal(err)
	}
	_, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: thingURN, Properties: in})
	if err == nil || !strings.Contains(err.Error(), "0.0.0-test") || !strings.Contains(err.Error(), "10.9.1") {
		t.Fatalf("create on another version: %v", err)
	}
	if srv.Count("things") != 0 {
		t.Error("the guard must stop the write")
	}
	if cr, err := d.Create(ctx, &pulumirpc.CreateRequest{Urn: officialURN}); err != nil || cr.Id != "official-1" {
		t.Errorf("official kinds are not guarded: %v %v", cr, err)
	}
}

func TestVersionGuardRetriesAReadFailure(t *testing.T) {
	calls := 0
	g := &versionGuard{want: "1.2.3", read: func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("connection refused")
		}
		return "1.2.3", nil
	}}
	if err := g.check(context.Background()); err == nil {
		t.Fatal("a read failure must fail the call")
	}
	if err := g.check(context.Background()); err != nil || calls != 2 {
		t.Fatalf("second check: %v after %d reads", err, calls)
	}
	if err := g.check(context.Background()); err != nil || calls != 2 {
		t.Fatalf("a success is cached: %v after %d reads", err, calls)
	}
	for got, want := range map[string]bool{"1.2.3": true, "1.2.3-beta": true, "1.2.30": false, "1.2": false} {
		if sameVersion(got, "1.2.3") != want {
			t.Errorf("sameVersion(%q, 1.2.3) != %v", got, want)
		}
	}
}
