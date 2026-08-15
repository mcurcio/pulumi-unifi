# pulumi-unifi

A **code-generated, native [Pulumi](https://www.pulumi.com/) provider** for
Ubiquiti's official **UniFi Network Integration API**
(`/proxy/network/integration/v1/...`, `X-API-KEY` auth).

!!! warning "Pre-release"
    This provider is **pre-release / unreleased**. There is no tagged release,
    published plugin asset, or PyPI package yet. Interfaces, tokens, and config
    keys may change. See [Compatibility](compatibility.md) for the current
    state.

## What it is

The provider is **generated** from the OpenAPI 3.1 specs published by
[beezly/unifi-apis](https://github.com/beezly/unifi-apis), which are
auto-extracted from UniFi controllers (one JSON document per controller
version). It is a **native** provider — a standalone Go gRPC plugin binary — not
a Terraform bridge and not an in-repo `pulumi.dynamic` provider.

The pipeline (see the repository's `docs/DESIGN.md`):

```
beezly OpenAPI spec ─▶ FixOpenAPIDoc ─▶ pulschema ─▶ Pulumi schema + CRUD metadata
                                          │
                                          ▼
   pulumi-provider-framework (Go REST-CRUD plugin) ─▶ pulumi package gen-sdk ─▶ SDKs
```

The pipeline is **deterministic**: the same pinned spec regenerates identical
`schema.json`, CRUD metadata, and SDK.

## Status and scope

- **Official API only.** Targets Ubiquiti's documented Network Integration API
  with `X-API-KEY` auth — not the legacy private controller API.
- **Self-scoping to write coverage.** Each spec entity is classified by the
  verbs the pinned spec exposes: create/update/delete entities become managed
  **resources**, GET-only entities become **data sources**. The pinned
  `10.4.57` spec currently yields **19 resources** and **50 data sources**.
  Bumping the pinned spec auto-promotes data sources to resources as Ubiquiti
  ships more write endpoints.
- **Requires a real UniFi OS console/Server.** The Integration API is coupled to
  UniFi OS; the key is minted in the Network application on a real console. See
  [Authentication](authentication.md).

## Full API reference

At the first real release the full, canonical API reference will live on the
**[Pulumi Registry](https://www.pulumi.com/registry/)**. The
[Reference](reference.md) page in this site is an **auto-generated, disposable
stopgap** built from the provider schema — accurate but terse, and superseded by
the Registry once published.

## Where to go next

- [Installation](installation.md) — plugin + SDKs
- [Authentication](authentication.md) — minting and configuring an API key
- [Getting Started](getting-started.md) — a worked `DnsARecord` example
- [Examples](examples.md) — a data-source read and a managed resource
- [Compatibility](compatibility.md) — provider ↔ UniFi ↔ SDK matrix
- [Reference](reference.md) — auto-generated schema reference
