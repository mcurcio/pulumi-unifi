package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	"github.com/cloudy-sky-software/pulumi-provider-framework/state"
)

// listPageLimit is the page size used for pagination follow-up GETs. The UniFi
// Integration API caps a collection's `limit` query parameter at 200.
const listPageLimit = 200

// maxPagesFallback bounds the follow-up GET count when the server reports no
// usable totalCount, so a misbehaving controller that returns non-empty pages
// forever (e.g. ignores offset) cannot hang `pulumi up` or OOM the plugin.
// 10000 pages × 200 rows = 2M rows — far beyond any real UniFi collection.
const maxPagesFallback = 10000

// OnPostInvoke aggregates paginated list responses. The framework issues exactly
// one GET per data-source read (rest/provider.go Invoke), so a collection larger
// than the server's default page silently returns only the first page — a
// correctness bug that decodes cleanly. When the decoded body is a UniFi page
// envelope ({data, totalCount, …}) we re-issue offset/limit GETs through the
// captured handler until the full collection is assembled. Every other body
// shape (naked arrays, single objects) returns nil to defer to the framework's
// own output conversion.
func (p *unifiProvider) OnPostInvoke(ctx context.Context, req *pulumirpc.InvokeRequest, outputs interface{}) (map[string]interface{}, error) {
	first, ok := outputs.(map[string]interface{})
	if !ok {
		return nil, nil
	}
	if _, hasData := first["data"]; !hasData {
		return nil, nil
	}
	if _, hasTotal := first["totalCount"]; !hasTotal {
		return nil, nil
	}

	// Resolve the read path for this token; without it (or a handler) we cannot
	// page, so hand the single decoded page back to the framework untouched.
	var readPath string
	if m := p.metadata.ResourceCRUDMap[req.GetTok()]; m != nil && m.R != nil {
		readPath = *m.R
	}
	if readPath == "" || p.handler == nil {
		return nil, nil
	}

	args, err := plugin.UnmarshalProperties(req.GetArgs(), state.DefaultUnmarshalOpts)
	if err != nil {
		return nil, fmt.Errorf("unmarshaling invoke args for pagination: %w", err)
	}

	fetch := func(offset, limit int) (map[string]interface{}, error) {
		httpReq, err := p.handler.CreateGetRequest(ctx, readPath, args, nil)
		if err != nil {
			return nil, fmt.Errorf("creating paginated get request: %w", err)
		}
		q := httpReq.URL.Query()
		q.Set("offset", strconv.Itoa(offset))
		q.Set("limit", strconv.Itoa(limit))
		httpReq.URL.RawQuery = q.Encode()

		resp, err := p.handler.GetHTTPClient().Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("executing paginated get request: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("paginated get %s returned %s: %s", readPath, resp.Status, string(body))
		}
		var page map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			return nil, fmt.Errorf("decoding paginated response: %w", err)
		}
		return page, nil
	}

	return aggregatePages(ctx, first, fetch)
}

// aggregatePages assembles a complete UniFi list response from its first page by
// following offset/limit until the reported totalCount is reached or a page
// returns no rows. fetch(offset, limit) yields the decoded page for that window.
// The returned envelope is the first page with `data` holding every row and
// count/offset/limit reconciled to the full set. Pure (no HTTP) so the paging
// loop is unit-testable.
//
// The loop is bounded three ways so a misbehaving controller cannot hang or OOM
// `pulumi up` (B-M1.1):
//   - ctx.Err() is checked each iteration, so cancellation/deadline aborts the
//     aggregate (each GET already carries ctx, but the loop itself must yield);
//   - a page ceiling caps the number of follow-up GETs — derived from totalCount
//     when the server reports it (so a server that ignores offset and returns a
//     full window forever is bounded), else maxPagesFallback.
//
// The empty-page terminator still bounds a server that reports a totalCount it
// never fills.
func aggregatePages(ctx context.Context, first map[string]interface{}, fetch func(offset, limit int) (map[string]interface{}, error)) (map[string]interface{}, error) {
	all := toSlice(first["data"])
	total, hasTotal := toInt(first["totalCount"])

	// Hard ceiling on follow-up GETs. With a known total, allow exactly the pages
	// needed plus a small slack for a partial tail; otherwise the fallback cap.
	maxPages := maxPagesFallback
	if hasTotal {
		maxPages = total/listPageLimit + 2
	}

	for page := 0; page < maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if hasTotal && len(all) >= total {
			break
		}
		decoded, err := fetch(len(all), listPageLimit)
		if err != nil {
			return nil, err
		}
		rows := toSlice(decoded["data"])
		if len(rows) == 0 {
			break
		}
		all = append(all, rows...)
	}

	first["data"] = all
	first["count"] = len(all)
	first["offset"] = 0
	first["limit"] = len(all)
	if !hasTotal {
		first["totalCount"] = len(all)
	}
	return first, nil
}

// toSlice returns v as a JSON array, or nil if it is not one. A decoded JSON
// body yields []interface{} for arrays.
func toSlice(v interface{}) []interface{} {
	s, _ := v.([]interface{})
	return s
}

// toInt returns v as an int when it is a JSON number. encoding/json decodes
// numbers into float64 when unmarshaling into interface{}.
func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}
