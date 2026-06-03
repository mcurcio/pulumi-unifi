package provider

// GetAuthorizationHeader returns the bare API key. The header *name* (X-API-Key)
// comes from the injected security scheme; UniFi expects the raw key as the
// value, with no "Bearer "/scheme prefix.
func (p *unifiProvider) GetAuthorizationHeader() string {
	return p.apiKey
}
