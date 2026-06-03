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

	return aggregatePages(first, fetch)
}

// aggregatePages assembles a complete UniFi list response from its first page by
// following offset/limit until the reported totalCount is reached or a page
// returns no rows. fetch(offset, limit) yields the decoded page for that window.
// The returned envelope is the first page with `data` holding every row and
// count/offset/limit reconciled to the full set. Pure (no HTTP) so the paging
// loop is unit-testable; the empty-page terminator also bounds a server that
// reports a totalCount it never fills.
func aggregatePages(first map[string]interface{}, fetch func(offset, limit int) (map[string]interface{}, error)) (map[string]interface{}, error) {
	all := toSlice(first["data"])
	total, hasTotal := toInt(first["totalCount"])

	for {
		if hasTotal && len(all) >= total {
			break
		}
		page, err := fetch(len(all), listPageLimit)
		if err != nil {
			return nil, err
		}
		rows := toSlice(page["data"])
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
