package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	"github.com/pulumi/pulumi/sdk/v3/go/common/util/logging"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// OnConfigure reads the API key and site ID from Pulumi config, falling back to
// the environment variables declared in the schema's provider input properties.
func (p *unifiProvider) OnConfigure(_ context.Context, req *pulumirpc.ConfigureRequest) (*pulumirpc.ConfigureResponse, error) {
	vars := req.GetVariables()

	// inputs supplies the env-var fallbacks declared in the schema. p.handler is
	// always set in production (makeProvider runs before Configure); guarding the
	// nil case keeps OnConfigure unit-testable with config passed via Variables.
	var inputs map[string]pschema.PropertySpec
	if p.handler != nil {
		inputs = p.handler.GetSchemaSpec().Provider.InputProperties
	}

	apiKey := p.configValue(vars, inputs, "apiKey")
	if apiKey == "" {
		return nil, errors.New("a UniFi API key is required (set unifi:apiKey or the UNIFI_APIKEY env var)")
	}
	p.apiKey = apiKey

	if siteID := p.configValue(vars, inputs, "siteId"); siteID != "" {
		p.siteID = siteID
	}

	// Validate apiHost. The framework does a raw `baseURL.Host = apiHost`, so a
	// pasted URL (scheme or path included) silently corrupts the base URL and
	// fails opaquely downstream. Reject anything that is not a bare host[:port].
	// We read apiHost the same way the framework does (config var, then env) so
	// the two never disagree. A single "/" check covers both "scheme://" and a
	// trailing path.
	apiHost := p.configValue(vars, inputs, "apiHost")
	if strings.Contains(apiHost, "/") {
		return nil, fmt.Errorf("apiHost %q must be a bare host or host:port (no scheme, no path), e.g. \"192.168.1.1\" or \"unifi.example.com:443\"", apiHost)
	}

	// allowInsecure opts into skipping TLS verification (self-signed controller
	// certs on a trusted network). Pulumi serializes config bools as "true"/
	// "false" strings; an absent value leaves verification on, and an unparseable
	// non-empty value is logged (fail-secure but not silent) so a typo'd setting
	// does not look like it took effect.
	allowInsecureStr := p.configValue(vars, inputs, "allowInsecure")
	b, err := strconv.ParseBool(allowInsecureStr)
	if err != nil && allowInsecureStr != "" {
		logging.V(3).Infof("ignoring unparseable allowInsecure=%q (%v); TLS verification stays on", allowInsecureStr, err)
	}
	p.allowInsecure = err == nil && b
	if p.allowInsecure && p.handler != nil {
		injectInsecureTransport(p.handler.GetHTTPClient())
		logging.V(3).Info("TLS certificate verification disabled (allowInsecure=true)")
	}

	logging.V(3).Infof("Configured UniFi provider (siteId=%s)", p.siteID)

	return &pulumirpc.ConfigureResponse{
		AcceptSecrets: true,
	}, nil
}

// configValue resolves one provider config key: the Pulumi config variable
// (unifi:config:<key>) if set and non-empty, else the schema-declared env-var
// fallback (firstEnv over the key's DefaultInfo). Collapses the per-key
// var-then-env dance so all four keys resolve uniformly.
func (p *unifiProvider) configValue(vars map[string]string, inputs map[string]pschema.PropertySpec, key string) string {
	if v, ok := vars[p.name+":config:"+key]; ok && v != "" {
		return v
	}
	return firstEnv(inputs[key].DefaultInfo)
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
