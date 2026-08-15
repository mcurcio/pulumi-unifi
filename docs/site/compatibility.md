# Compatibility

The provider is generated from a **pinned** UniFi Network Integration API spec.
The table below maps the provider version to the UniFi controller version it was
generated against and the SDKs available.

| Provider version | UniFi Network | UniFi OS | SDKs available |
| --- | --- | --- | --- |
| unreleased (pre-release) | 10.4.57 | 5.1.15 | Python (from source); Go planned |

Notes:

- **Pinned spec.** The provider is currently pinned to UniFi Network `10.4.57`
  (`openapi/pin.env`). This is the version whose OpenAPI spec drives codegen; the
  UniFi OS `5.1.15` pairing is the console version used by the end-to-end seed.
- **Pre-release.** No tagged release, plugin asset, or PyPI package exists yet.
  The `latest` alias on this documentation site will track the newest stable
  release once one is cut.
- **Write coverage grows with the spec.** The set of writable resources is
  exactly what the pinned controller version exposes. Bumping the pinned spec to
  a newer controller version auto-promotes read-only data sources to full
  resources as Ubiquiti ships more write endpoints — no per-resource hand-coding.
- **SDKs.** The Python SDK (`pulumi_unifi`) is generated today. A Go SDK
  (import base `github.com/mcurcio/pulumi-unifi/sdk/go/...`) is planned.

Forward and backward compatibility against other controller versions is **not
tested**. Reads target the documented, versioned Integration API and are
expected to be broadly compatible, but resource inputs/outputs track the pinned
spec.
