# Authentication

The provider authenticates to the UniFi Network Integration API with an
**`X-API-Key`** header. This section covers minting the key and configuring the
provider.

## Minting an API key

!!! important "Requires a real UniFi OS console/Server"
    The Integration API is coupled to **UniFi OS**. The key is minted in the
    Network application running on a real UniFi OS console or UniFi OS Server —
    the standalone/self-hosted Network application does not serve the
    authenticated Integration API.

1. Open the **UniFi Network** application on your console.
2. Go to **Settings → Control Plane → Integrations** (labelled **Settings →
   Integrations** on some versions).
3. Create an API key and copy it. It is shown once — store it securely.

The key is sent as the **bare** `X-API-Key` header value (no `Bearer` or other
scheme prefix).

## Configuring the provider

The provider exposes four configuration keys. Each also reads a fallback
environment variable.

| Config key | Environment variable | Secret | Description |
| --- | --- | --- | --- |
| `apiKey` | `UNIFI_APIKEY` | yes | The Integration API key, sent as the `X-API-Key` header. |
| `apiHost` | `UNIFI_API_HOST` | no | Controller host and optional `:port`, e.g. `192.168.1.1` or `unifi.example.com:443`. Overrides the host in the generated server URL. |
| `siteId` | `UNIFI_SITEID` | no | Site ID filling the `{siteId}` path parameter on site-scoped resources. Defaults to `default`. |
| `allowInsecure` | `UNIFI_ALLOW_INSECURE` | no | Skip TLS verification for controllers presenting self-signed certificates. Defaults to `false`. |

### Via Pulumi config

```bash
pulumi config set unifi:apiHost 192.168.1.1
pulumi config set --secret unifi:apiKey <your-api-key>
# Optional:
pulumi config set unifi:siteId default
pulumi config set unifi:allowInsecure true
```

### Via environment variables

```bash
export UNIFI_API_HOST=192.168.1.1
export UNIFI_APIKEY=<your-api-key>
export UNIFI_SITEID=default
export UNIFI_ALLOW_INSECURE=true
```

## `allowInsecure` and self-signed certificates

Most UniFi consoles present a self-signed certificate. Set `allowInsecure=true`
to skip TLS verification on a trusted network.

!!! warning
    `allowInsecure=true` also **disables the HTTP 429 rate-limit retry
    wrapper** (the framework's retrying transport is replaced by a
    verification-skipping one). This is acceptable for single-controller use on a
    trusted network. Leave it off (the default) when the controller presents a
    CA-trusted certificate.
