package gen

import (
	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
)

const packageName = "unifi"

// excludedPaths lists endpoints that are not clean CRUD resources (RPC-style
// "actions", list "ordering" mutations, and read-only sub-resource lookups).
// Feeding them to the auto-grouper produces junk resources, so they are dropped.
// This list grows empirically as codegen output is inspected.
var excludedPaths = []string{
	"/v1/sites/{siteId}/acl-rules/ordering",
	"/v1/sites/{siteId}/firewall/policies/ordering",
	"/v1/sites/{siteId}/clients/{clientId}/actions",
	"/v1/sites/{siteId}/devices/{deviceId}/actions",
	"/v1/sites/{siteId}/devices/{deviceId}/interfaces/ports/{portIdx}/actions",
	"/v1/sites/{siteId}/devices/{deviceId}/statistics/latest",
	"/v1/sites/{siteId}/networks/{networkId}/references",
}

// configKey describes one provider config property in a single place, so the
// schema's Config.Variables and Provider.InputProperties cannot drift apart. The
// only field that differs between the two views is the env-var fallback, which
// belongs solely on InputProperties (DefaultInfo.Environment) — see configKeys.
type configKey struct {
	description string
	typ         string   // "string" | "boolean"
	secret      bool     // redact in state/CLI output
	env         []string // env-var fallback(s); InputProperties only
}

// configKeys is the single source of truth for the four provider config
// properties. packageSpec() derives both Config.Variables (no DefaultInfo) and
// Provider.InputProperties (with DefaultInfo.Environment) from this map.
var configKeys = map[string]configKey{
	"apiKey": {
		description: "The UniFi Network Integration API key (sent as the X-API-Key header).",
		typ:         "string",
		secret:      true,
		env:         []string{"UNIFI_APIKEY"},
	},
	"apiHost": {
		description: "The UniFi controller host (and optional :port), e.g. \"192.168.1.1\" or \"unifi.example.com:443\". Overrides the host in the generated server URL.",
		typ:         "string",
		env:         []string{"UNIFI_API_HOST"},
	},
	"siteId": {
		description: "The UniFi site ID used to fill the {siteId} path parameter on site-scoped resources. Defaults to \"default\".",
		typ:         "string",
		env:         []string{"UNIFI_SITEID"},
	},
	"allowInsecure": {
		description: "Skip TLS certificate verification when connecting to the controller. Use only for controllers presenting self-signed certificates on a trusted network. Note: this also disables the HTTP 429 rate-limit retry wrapper.",
		typ:         "boolean",
		env:         []string{"UNIFI_ALLOW_INSECURE"},
	},
}

// configVariables builds the Config.Variables view: description + type + secret,
// no env-var DefaultInfo.
func configVariables() map[string]pschema.PropertySpec {
	out := make(map[string]pschema.PropertySpec, len(configKeys))
	for name, k := range configKeys {
		out[name] = pschema.PropertySpec{
			Description: k.description,
			TypeSpec:    pschema.TypeSpec{Type: k.typ},
			Secret:      k.secret,
		}
	}
	return out
}

// providerInputProperties builds the Provider.InputProperties view: the same
// description + type + secret, plus the env-var DefaultInfo fallback.
func providerInputProperties() map[string]pschema.PropertySpec {
	out := make(map[string]pschema.PropertySpec, len(configKeys))
	for name, k := range configKeys {
		out[name] = pschema.PropertySpec{
			DefaultInfo: &pschema.DefaultSpec{Environment: k.env},
			Description: k.description,
			TypeSpec:    pschema.TypeSpec{Type: k.typ},
			Secret:      k.secret,
		}
	}
	return out
}

// packageSpec returns the static Pulumi PackageSpec identity for the provider:
// metadata, keywords, the single-sourced config block, and the plugin download
// URL. The resource/type/function maps start empty and are filled by pulschema
// (PulumiSchema, schema.go).
func packageSpec() pschema.PackageSpec {
	return pschema.PackageSpec{
		Name:        packageName,
		Description: "A Pulumi package for managing UniFi Network resources via the official Integration API.",
		DisplayName: "UniFi",
		License:     "Apache-2.0",
		Keywords: []string{
			"pulumi",
			packageName,
			"ubiquiti",
			"category/network",
			"kind/native",
		},
		Homepage:   "https://github.com/mcurcio/pulumi-unifi",
		Publisher:  "Matt Curcio",
		Repository: "https://github.com/mcurcio/pulumi-unifi",

		Config: pschema.ConfigSpec{
			Variables: configVariables(),
		},

		Provider: pschema.ResourceSpec{
			ObjectTypeSpec: pschema.ObjectTypeSpec{
				Description: "The provider type for the unifi package.",
				Type:        "object",
			},
			InputProperties: providerInputProperties(),
		},

		PluginDownloadURL: "github://api.github.com/mcurcio/pulumi-unifi",
		Types:             map[string]pschema.ComplexTypeSpec{},
		Resources:         map[string]pschema.ResourceSpec{},
		Functions:         map[string]pschema.FunctionSpec{},
		Language:          map[string]pschema.RawMessage{},
	}
}
