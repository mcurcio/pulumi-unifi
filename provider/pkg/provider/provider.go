// Package provider implements the UniFi provider's callback hooks on top of the
// generic cloudy-sky-software REST CRUD framework. For the read-path MVP the
// only behavior we override is authentication (X-API-Key), API-key/site config,
// and injecting the site ID into the {siteId} path parameter. All CRUD lifecycle
// hooks fall through to the framework's Unimplemented defaults.
package provider

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
	"github.com/pulumi/pulumi/pkg/v3/resource/provider"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/logging"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	fwCallback "github.com/cloudy-sky-software/pulumi-provider-framework/callback"
	fwRest "github.com/cloudy-sky-software/pulumi-provider-framework/rest"
	"github.com/cloudy-sky-software/pulumi-provider-framework/state"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// defaultSiteID is used when neither config nor env supplies a site ID. "default"
// is the conventional UniFi site identifier on a single-site controller.
const defaultSiteID = "default"

type unifiProvider struct {
	fwCallback.UnimplementedProviderCallback

	name    string
	version string

	apiKey        string
	siteID        string
	allowInsecure bool

	// metadata is the same crudMap/name-map bundle the framework runs on,
	// stashed so OnPostInvoke can resolve a token's read path for pagination.
	metadata openapigen.ProviderMetadata
}

var (
	handler  *fwRest.Provider
	callback fwCallback.ProviderCallback
)

func makeProvider(host *provider.HostClient, name, version string, pulumiSchemaBytes, openapiDocBytes, metadataBytes []byte) (pulumirpc.ResourceProviderServer, error) {
	p := &unifiProvider{
		name:    name,
		version: version,
		siteID:  defaultSiteID,
	}

	// Stash the metadata so OnPostInvoke can look up read paths for pagination.
	// The framework parses the same bytes internally but exposes no accessor.
	if err := json.Unmarshal(metadataBytes, &p.metadata); err != nil {
		return nil, errors.Wrap(err, "unmarshaling provider metadata")
	}

	callback = p
	rp, err := fwRest.MakeProvider(host, name, version, pulumiSchemaBytes, openapiDocBytes, metadataBytes, callback)
	if err != nil {
		return nil, err
	}
	handler = rp.(*fwRest.Provider)
	return rp, nil
}

// GetAuthorizationHeader returns the bare API key. The header *name* (X-API-Key)
// comes from the injected security scheme; UniFi expects the raw key as the
// value, with no "Bearer "/scheme prefix.
func (p *unifiProvider) GetAuthorizationHeader() string {
	return p.apiKey
}

// OnConfigure reads the API key and site ID from Pulumi config, falling back to
// the environment variables declared in the schema's provider input properties.
func (p *unifiProvider) OnConfigure(_ context.Context, req *pulumirpc.ConfigureRequest) (*pulumirpc.ConfigureResponse, error) {
	vars := req.GetVariables()

	// inputs supplies the env-var fallbacks declared in the schema. handler is
	// always set in production (makeProvider runs before Configure); guarding the
	// nil case keeps OnConfigure unit-testable with config passed via Variables.
	var inputs map[string]pschema.PropertySpec
	if handler != nil {
		inputs = handler.GetSchemaSpec().Provider.InputProperties
	}

	apiKey, ok := vars[p.name+":config:apiKey"]
	if !ok || apiKey == "" {
		apiKey = firstEnv(inputs["apiKey"].DefaultInfo)
	}
	if apiKey == "" {
		return nil, errors.New("a UniFi API key is required (set unifi:apiKey or the UNIFI_APIKEY env var)")
	}
	p.apiKey = apiKey

	if siteID, ok := vars[p.name+":config:siteId"]; ok && siteID != "" {
		p.siteID = siteID
	} else if envSiteID := firstEnv(inputs["siteId"].DefaultInfo); envSiteID != "" {
		p.siteID = envSiteID
	}

	// Validate apiHost. The framework does a raw `baseURL.Host = apiHost`, so a
	// pasted URL (scheme or path included) silently corrupts the base URL and
	// fails opaquely downstream. Reject anything that is not a bare host[:port].
	// We read apiHost the same way the framework does (config var, then env) so
	// the two never disagree. A single "/" check covers both "scheme://" and a
	// trailing path.
	apiHost, ok := vars[p.name+":config:apiHost"]
	if !ok || apiHost == "" {
		apiHost = firstEnv(inputs["apiHost"].DefaultInfo)
	}
	if strings.Contains(apiHost, "/") {
		return nil, errors.Errorf("apiHost %q must be a bare host or host:port (no scheme, no path), e.g. \"192.168.1.1\" or \"unifi.example.com:443\"", apiHost)
	}

	// allowInsecure opts into skipping TLS verification (self-signed controller
	// certs on a trusted network). Pulumi serializes config bools as "true"/
	// "false" strings; an unparseable/absent value leaves verification on.
	allowInsecureStr, ok := vars[p.name+":config:allowInsecure"]
	if !ok || allowInsecureStr == "" {
		allowInsecureStr = firstEnv(inputs["allowInsecure"].DefaultInfo)
	}
	if b, err := strconv.ParseBool(allowInsecureStr); err == nil {
		p.allowInsecure = b
	}
	if p.allowInsecure && handler != nil {
		injectInsecureTransport(handler.GetHTTPClient())
		logging.V(3).Info("TLS certificate verification disabled (allowInsecure=true)")
	}

	logging.V(3).Infof("Configured UniFi provider (siteId=%s)", p.siteID)

	return &pulumirpc.ConfigureResponse{
		AcceptSecrets: true,
	}, nil
}

// GetGlobalPathParams supplies the {siteId} path parameter shared by all
// site-scoped endpoints so the framework can build request URLs.
func (p *unifiProvider) GetGlobalPathParams(_ context.Context, _ *pulumirpc.ConfigureRequest) (map[string]string, error) {
	return map[string]string{
		"siteId": p.siteID,
	}, nil
}

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
	if readPath == "" || handler == nil {
		return nil, nil
	}

	args, err := plugin.UnmarshalProperties(req.GetArgs(), state.DefaultUnmarshalOpts)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshaling invoke args for pagination")
	}

	fetch := func(offset, limit int) (map[string]interface{}, error) {
		httpReq, err := handler.CreateGetRequest(ctx, readPath, args, nil)
		if err != nil {
			return nil, errors.Wrap(err, "creating paginated get request")
		}
		q := httpReq.URL.Query()
		q.Set("offset", strconv.Itoa(offset))
		q.Set("limit", strconv.Itoa(limit))
		httpReq.URL.RawQuery = q.Encode()

		resp, err := handler.GetHTTPClient().Do(httpReq)
		if err != nil {
			return nil, errors.Wrap(err, "executing paginated get request")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, errors.Errorf("paginated get %s returned %s: %s", readPath, resp.Status, string(body))
		}
		var page map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			return nil, errors.Wrap(err, "decoding paginated response")
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

// injectInsecureTransport replaces the client's transport with one that skips
// TLS verification. The framework builds its client with an unexported
// rateLimitTransport wrapper and exposes no transport setter, so we overwrite
// the whole Transport — losing the 429 retry wrapper. We mirror the framework's
// inner *http.Transport settings (rest/provider.go) so only TLS verification
// changes. Acceptable for single-controller live testing against a self-signed
// cert; the clean fix is an upstream exported transport constructor/setter.
func injectInsecureTransport(c *http.Client) {
	c.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- opt-in allowInsecure for self-signed controller certs
	}
}

// firstEnv returns the value of the first set environment variable named by a
// schema property's DefaultInfo, or "" if none is set.
func firstEnv(def *pschema.DefaultSpec) string {
	if def == nil {
		return ""
	}
	for _, name := range def.Environment {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}
