package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// paginatedDataSource is a site-scoped list data source whose read path is a
// page envelope ({data,totalCount}) — the shape OnPostInvoke aggregates. It is
// the same token TestWirePath exercises.
const paginatedDataSource = "unifi:sites/v1:getWifiBroadcastPage"

// newProviderAgainst wires a real provider+framework stack at the httptest TLS
// server, trusting its self-signed cert via allowInsecure=true. Shared shape
// with TestWirePath; no Docker, so it runs in the default `make test` gate.
func newProviderAgainst(t *testing.T, srv *httptest.Server) pulumirpc.ResourceProviderServer {
	t.Helper()
	rp, err := makeProvider(
		nil, "unifi", "0.0.0-test",
		readArtifact(t, "schema.json"),
		readArtifact(t, "openapi_generated.yml"),
		readArtifact(t, "metadata.json"),
	)
	if err != nil {
		t.Fatalf("makeProvider: %v", err)
	}
	if _, err := rp.Configure(context.Background(), &pulumirpc.ConfigureRequest{
		Variables: map[string]string{
			"unifi:config:apiKey":        "k",
			"unifi:config:apiHost":       strings.TrimPrefix(srv.URL, "https://"),
			"unifi:config:allowInsecure": "true",
		},
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	return rp
}

func invokeDataSource(t *testing.T, rp pulumirpc.ResourceProviderServer, tok string) resource.PropertyMap {
	t.Helper()
	args, err := plugin.MarshalProperties(resource.PropertyMap{}, plugin.MarshalOptions{})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	resp, err := rp.Invoke(context.Background(), &pulumirpc.InvokeRequest{Tok: tok, Args: args})
	if err != nil {
		t.Fatalf("Invoke(%s): %v", tok, err)
	}
	if fs := resp.GetFailures(); len(fs) > 0 {
		t.Fatalf("Invoke(%s) failures: %v", tok, fs)
	}
	out, err := plugin.UnmarshalProperties(resp.GetReturn(), plugin.MarshalOptions{})
	if err != nil {
		t.Fatalf("unmarshal return: %v", err)
	}
	return out
}

// TestOnPostInvokeAggregatesPages drives a real data-source Invoke through the
// provider + framework against an httptest server serving a 3-page collection,
// proving OnPostInvoke's pagination end-to-end (E-M4.4): all rows are assembled,
// each follow-up GET carries the right offset/limit, and the page count matches.
func TestOnPostInvokeAggregatesPages(t *testing.T) {
	const total = 2*listPageLimit + 7 // 3 pages: full, full, partial

	var mu sync.Mutex
	var offsets, limits []int
	requests := 0

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		// The framework's own first GET carries no offset/limit; OnPostInvoke's
		// follow-ups set both. Record the windowing params when present.
		q := r.URL.Query()
		offset := 0
		if v := q.Get("offset"); v != "" {
			offset, _ = strconv.Atoi(v)
			offsets = append(offsets, offset)
		}
		if v := q.Get("limit"); v != "" {
			lim, _ := strconv.Atoi(v)
			limits = append(limits, lim)
		}
		mu.Unlock()

		// Serve the window [offset, offset+listPageLimit) clamped to total.
		end := offset + listPageLimit
		if end > total {
			end = total
		}
		data := make([]map[string]any, 0, end-offset)
		for i := offset; i < end; i++ {
			data = append(data, map[string]any{"id": fmt.Sprintf("row-%d", i)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":       data,
			"totalCount": total,
			"count":      len(data),
			"offset":     offset,
			"limit":      listPageLimit,
		})
	}))
	defer srv.Close()

	rp := newProviderAgainst(t, srv)
	out := invokeDataSource(t, rp, paginatedDataSource)

	dataVal, ok := out["data"]
	if !ok {
		t.Fatalf("result has no data field; got keys %v", keysOf(out))
	}
	got := len(dataVal.ArrayValue())
	if got != total {
		t.Errorf("aggregated %d rows, want all %d across the 3 pages", got, total)
	}

	mu.Lock()
	defer mu.Unlock()
	// Three GETs total: the framework's initial read (offset=0, its own default
	// limit) and OnPostInvoke's two follow-ups paging from the running total
	// (offset=listPageLimit, then 2*listPageLimit), each with limit=listPageLimit.
	// The follow-up windowing is what E-M4.4 proves.
	wantOffsets := []int{0, listPageLimit, 2 * listPageLimit}
	if !equalInts(offsets, wantOffsets) {
		t.Errorf("GET offsets = %v, want %v (framework read + 2 paged follow-ups)", offsets, wantOffsets)
	}
	if len(limits) < 3 {
		t.Fatalf("expected 3 GETs carrying a limit, got %d (%v)", len(limits), limits)
	}
	for _, l := range limits[1:] { // skip the framework's own initial GET
		if l != listPageLimit {
			t.Errorf("follow-up GET limit = %d, want %d", l, listPageLimit)
		}
	}
}

// TestOnPostInvokePropagatesPageError proves a non-200 on a follow-up page GET
// aborts the Invoke with an error rather than silently truncating (E-M4.4). The
// first (framework) GET succeeds with a full page promising more; the follow-up
// GET returns 500.
func TestOnPostInvokePropagatesPageError(t *testing.T) {
	const total = 2 * listPageLimit // promises a second page

	first := true
	var mu sync.Mutex
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		isFirst := first
		first = false
		mu.Unlock()

		if !isFirst {
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
			return
		}
		data := make([]map[string]any, 0, listPageLimit)
		for i := 0; i < listPageLimit; i++ {
			data = append(data, map[string]any{"id": fmt.Sprintf("row-%d", i)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": data, "totalCount": total, "count": len(data), "offset": 0, "limit": listPageLimit,
		})
	}))
	defer srv.Close()

	rp := newProviderAgainst(t, srv)
	args, err := plugin.MarshalProperties(resource.PropertyMap{}, plugin.MarshalOptions{})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	resp, err := rp.Invoke(context.Background(), &pulumirpc.InvokeRequest{Tok: paginatedDataSource, Args: args})
	// The page-2 500 must surface — either as an Invoke error or as failures, not
	// as a silently-truncated success.
	if err == nil && (resp == nil || len(resp.GetFailures()) == 0) {
		t.Error("expected the page-2 500 to propagate as an Invoke error/failure, got a clean result")
	}
}

func keysOf(m resource.PropertyMap) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	return out
}
