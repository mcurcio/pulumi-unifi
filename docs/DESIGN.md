# Design

A standalone, regenerable, native **Pulumi provider** for Ubiquiti's official UniFi Network
Integration API, code-generated from the [beezly/unifi-apis](https://github.com/beezly/unifi-apis)
OpenAPI 3.1 specs.

## 1. API source

[beezly/unifi-apis](https://github.com/beezly/unifi-apis) publishes OpenAPI 3.1 specs
auto-extracted from UniFi controllers — one JSON file per controller version, under
`unifi-network/<version>.json` (and `unifi-protect/<version>.json`, out of scope here). They
describe the **official Network Integration API**:

- Base path: `https://<console>/proxy/network/integration/v1/...`
- Auth: `X-API-KEY: <key>` header (key minted in the Network app: Settings → Integrations).
- REST-ful entity paths: `/v1/sites/{siteId}/<entity>` (collection) and
  `/v1/sites/{siteId}/<entity>/{id}` (item), with pagination (`offset`/`limit`).
- Clean `operationId`s (e.g. `listWifiBroadcasts`, `getWifiBroadcastDetails`,
  `createWifiBroadcast`, `updateWifiBroadcast`, `deleteWifiBroadcast`).
- Schemas via `$ref` in `components`, including polymorphic `oneOf` + `discriminator`
  (e.g. standard vs IoT-optimized WiFi broadcast).

The specs are machine-consumable enough to drive code generation of both a provider schema and a
REST CRUD runtime.

> **Provenance / license:** the beezly repo has **no license**. Treat the spec strictly as a
> codegen *input*: pin the exact upstream commit SHA + checksum in `openapi/SOURCE` and **fetch it at
> build** (`openapi/fetch.sh`) — it is a gitignored build artifact, never committed. Worst case, the
> same OpenAPI document can be re-extracted from an owned controller.

## 2. Toolchain

The path Pulumi documents for generating a provider from an OpenAPI spec:

| Tool | Role |
|---|---|
| [pulschema](https://github.com/cloudy-sky-software/pulschema) | OpenAPI spec → Pulumi Package Schema (`schema.json`) + CRUD endpoint map (`metadata.json`). Handles types and `$ref`, and **auto-derives resource grouping** from the REST path shape + verbs (see §4). A discriminated request body is split into **one resource per variant** — it does *not* emit a Pulumi `oneOf` tagged union (see §8). |
| [pulumi-provider-framework](https://github.com/cloudy-sky-software/pulumi-provider-framework) | Generic Go HTTP CRUD runtime that executes `metadata.json` against the REST API. This is the gRPC provider plugin binary. It derives the **auth header name from the spec's security scheme** and overrides only the server **host** (`apiHost`) at runtime (see §6). |
| `pulumi package gen-sdk` | Emits language SDKs (Python first; TS/Go/.NET available) from `schema.json`. |

**Why Go:** Pulumi provider plugins are gRPC servers distributed as binaries. Go compiles to a
standalone binary with no runtime dependency and has the most mature codegen tooling. The
hand-written Go in this repo is confined to thin glue (auth callbacks, the codegen entrypoint, spec
fixes + an `ExcludedPaths` list); the bulk of the provider is generated.

**Why not alternatives:**
- *In-repo `pulumi.dynamic` provider* — ships provider code inside the consumer, Python-only, not
  a reusable plugin. Rejected: we want a real, standalone, multi-language-capable provider.
- *terraform-bridge* — needs an existing Terraform provider and re-introduces a TF dependency.
  Rejected: the whole point is to leave Terraform.
- *Hand-written `pulumi-go-provider` infer types* — viable, but you hand-write the Go structs, so
  it isn't regenerable from the spec. The pulschema path keeps the spec as the source of truth.

## 3. Regenerable pipeline

```
beezly spec (pinned commit SHA + controller version)
        │
        ▼
   FixOpenAPIDoc (inject X-API-Key scheme, rewrite server URL) + ExcludedPaths
        │
        ▼
   pulschema  ──────────────▶  schema.json  +  metadata.json
        │   (grouping auto-derived from path shape + verbs)
        ▼
  pulumi-provider-framework runtime (Go plugin binary: pulumi-resource-unifi)
        │
        ▼
  pulumi package gen-sdk --language python  ──────▶  sdk/python  (pulumi_unifi)
```

The pipeline is **deterministic**: same pinned spec + same fixes/exclusions → identical
`schema.json`, `metadata.json`, and SDK. Every generated/fetched output is a **gitignored build
artifact** (the spec, the three codegen outputs, and `sdk/python`) — `make build`/`make generate`
fetch + regenerate them on demand and the plugin `//go:embed`s the three codegen outputs at compile
time. Regeneration is driven by bumping the pinned spec version. (Determinism required one fix: every
component schema is given a unique title so discriminated-getter names don't collapse — see
`ensureSchemaTitles`.)

## 4. Resource grouping (auto-derived; the editorial layer is exclusions)

OpenAPI is **operation-centric** (a flat list of path+method operations); Pulumi is
**resource-centric** (one resource = a CRUD lifecycle around one entity). **pulschema auto-derives
this grouping** from the `/v1/sites/{siteId}/<entity>[/{id}]` path shape and the verbs present:
collection `GET`/`POST` + item `GET`/`PUT`/`DELETE` on the same entity path collapse into one
resource token, with create/read/update/delete bound to the matching endpoints and the resource ID
taken from the response `id`. There is **no hand-authored `grouping.{go,yaml}`** — that step was in
an earlier design and does not match the toolchain.

The actual editorial surface is therefore narrow:

- **`ExcludedPaths`** (in `provider/pkg/gen/schema.go`) drops endpoints that are not clean CRUD —
  RPC-style `*/actions`, `*/ordering` mutations, and read-only sub-resource lookups — which would
  otherwise produce junk resources. The list grows empirically as codegen output is inspected.
- **`FixOpenAPIDoc`** (Risks A/B in §6) and **`SanitizeSpecBytes`** (legalizing component keys,
  normalizing type-less/`null` schemas the low-quality capture ships) make the spec consumable.

**Token shape:** because grouping derives from the path, the module is `sites/v1`-based, e.g.
`unifi:sites/v1:Gateway`, `unifi:sites/v1:getNetworksOverviewPage`. Entities exposing only `GET`
become **data sources** (Pulumi functions), not resources.

## 5. Self-scoping to the official API's write coverage

As of mid-2026 the official API key is **partially writable** — Ubiquiti is rolling write endpoints
out through 2026. The pinned 10.4.57 spec exposes **9 writable entity endpoints**
(`acl-rules`, `dns/policies`, `firewall/policies`, `firewall/zones`, `networks`,
`traffic-matching-lists`, `hotspot/vouchers`, `wifi/broadcasts`, and device adopt), which fan out
to **21 Pulumi resources** after per-variant splitting (§8), alongside **50 read-only data
sources**. Reads cover effectively all entities.

The provider does not hardcode a resource list. It classifies each entity by the verbs the pinned
spec exposes:

- has create/update/delete → managed **resource** (full CRUD lifecycle)
- GET-only → **data source**

So the provider's writable surface is exactly what the pinned controller version supports.
**Bumping the pinned spec to a newer version auto-promotes** read-only entities to full resources
as Ubiquiti ships their writes — no per-resource hand-coding, just a spec bump + regenerate.

## 6. Provider configuration & auth

Provider config:

| Key | Env | Notes |
|---|---|---|
| `apiKey` | `UNIFI_APIKEY` | **secret**; sent as the bare `X-API-Key` request header value |
| `apiHost` | `UNIFI_API_HOST` | controller host (and optional `:port`), e.g. `192.168.1.1` or `unifi.example.com:443`. Overrides **only the host** of the generated server URL |
| `siteId` | `UNIFI_SITEID` | site ID filling the `{siteId}` path param; defaults to `default` |
| `allowInsecure` | `UNIFI_ALLOW_INSECURE` | bool; skip TLS verification for self-signed controller certs (see caveat below). Defaults to off |

Two non-obvious constraints drive this (both handled in `FixOpenAPIDoc`):

- **Risk A — auth (header name comes from the spec).** The fetched spec has **no
  `securitySchemes`**. `FixOpenAPIDoc` injects an `apiKey`/`in: header` scheme named **`X-API-Key`**;
  the framework derives the auth *header name* from that scheme, and the provider's
  `GetAuthorizationHeader()` returns the **bare key** as the value (no `Bearer`/scheme prefix).
  The `apiKey` config property itself is **hand-authored** in the provider's static `PackageSpec`
  (`packagespec.go`, single-sourced with the other config keys) — it is not derived from the
  injected scheme; the scheme only tells the framework the header name.
- **Risk B — base URL (host swap only).** The spec ships a **relative** `/integration` server and
  the framework can override only the **host**, not the full URL. `FixOpenAPIDoc` rewrites the
  server to an absolute `https://localhost/proxy/network/integration`; at configure time the
  framework swaps the host for `apiHost`, leaving scheme + path intact.

> **`allowInsecure` (TLS verification).** Off by default, and on the off-path the framework's HTTP
> client is byte-for-byte unchanged: the provider trusts the controller CA through the OS trust store.
> That store is platform-specific — on Linux Go's default transport honors `SSL_CERT_FILE`; on macOS it
> verifies against the Security.framework keychain and **ignores `SSL_CERT_FILE`** (it also rejects
> certs valid >398 days). Setting `allowInsecure=true` accepts self-signed controller certs by replacing
> the framework's transport with one that skips verification (`InjectInsecureTransport`, mirroring the
> framework's transport shape). **Caveat:** the framework's rate-limit (HTTP 429) retry wrapper is
> unexported, so the insecure path replaces it and loses automatic 429 retry — acceptable for
> single-controller use on a trusted network; the clean fix is an upstream exported transport setter.
>
> The Tier-1 mock tier dogfoods `allowInsecure=true` (so it runs identically on Linux and macOS); the
> CA-pinned secure path (OS trust with `allowInsecure=false`) is validated by the Tier-2 live test.

## 7. Distribution

- **Plugin binary:** built per OS/arch, attached to GitHub Releases. `schema.json` carries a
  `PluginDownloadURL` using the `github://` scheme
  (`github://api.github.com/mcurcio/pulumi-unifi`); Pulumi itself computes the release asset name
  (`pulumi-resource-unifi-vX.Y.Z-<os>-<arch>.tar.gz`) — there is no `${OS}/${ARCH}` template — so
  `pulumi up` auto-installs the plugin with no manual `pulumi plugin install`. This requires the
  release job to emit assets named exactly as Pulumi expects and the repo's releases to be public
  (Phase 5, F-M5.1).
- **Python SDK:** generated by `pulumi package gen-sdk` and published to PyPI (or installed from a
  pinned git ref). Consumers `pip install pulumi-unifi` and `import pulumi_unifi`.

## 8. Open questions / risks

- **Write coverage is a schedule risk, not a tooling one.** The writable resource set grows as
  Ubiquiti ships endpoints through 2026.
- **Polymorphic schemas — resolved, but not as expected.** pulschema does **not** emit Pulumi
  `oneOf` tagged unions for the spec's `oneOf` + `discriminator` bodies. It **splits each
  discriminated union into one resource/type per variant**: WiFi broadcast → `Standard` /
  `IotOptimized`; traffic-matching → `Mac` / `Ports` / `Ipv4`; managed network → `Gateway` /
  `Switch` / `Unmanaged`; DNS records → `ARecord` / `AaaaRecord` / `CnameRecord` / … This is why 9
  writable endpoints (§5) become 21 resources. Consumers pick the concrete variant resource rather
  than setting a discriminator field. (One determinism hazard this exposed — variant getter names
  collapsing — is handled by `ensureSchemaTitles`.)
  - **CRUD fragmentation — repaired in the gen layer.** The same split scatters an entity's verbs
    across per-variant tokens: pulschema binds only **create** to each variant token (read/update/
    delete land on separate per-verb schema names), so 18 of the 21 resources generate as
    create-only stubs that would die on the next `pulumi up` ("resource read endpoint is unknown").
    `coalesceDiscriminatedCRUD` (`provider/pkg/gen/schema.go`) is a deterministic post-process,
    analogous to the `FixOpenAPIDoc` fix layer: for each resource token it fills the missing
    R/U/D/P from the entity's shared item path (`P_coll + "/{param}"`; the discriminator rides in
    the request body, so the item path is correct for every variant) and prunes the orphan per-verb
    keys. Result: all 21 resources round-trip full CRUD, `crudMap` keys = exactly the live
    resource + function tokens. The long-term fix is upstream (per-verb discriminator schema names).
- **`siteId`** — defaults to `default`; per-resource override is a Phase-4 item (the global path
  param is set once at configure time today).
- **License/provenance** of the fetched spec — pinned commit SHA + checksum recorded in
  `openapi/SOURCE`; the spec is fetched at build, not committed.
