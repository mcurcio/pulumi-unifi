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
> codegen *input*: vendor a copy, pin the exact upstream commit SHA in `openapi/`, and record it.
> Worst case, the same OpenAPI document can be re-extracted from an owned controller.

## 2. Toolchain

The path Pulumi documents for generating a provider from an OpenAPI spec:

| Tool | Role |
|---|---|
| [pulschema](https://github.com/cloudy-sky-software/pulschema) | OpenAPI spec → Pulumi Package Schema (`schema.json`) + CRUD endpoint map (`metadata.json`). Handles types, `$ref`, `oneOf`/`discriminator`. **Does not** decide resource grouping. |
| [pulumi-provider-framework](https://github.com/cloudy-sky-software/pulumi-provider-framework) | Generic Go HTTP CRUD runtime that executes `metadata.json` against the REST API. This is the gRPC provider plugin binary. `X-API-KEY` + base URL injected here. |
| `pulumi package gen-sdk` | Emits language SDKs (Python first; TS/Go/.NET available) from `schema.json`. |

**Why Go:** Pulumi provider plugins are gRPC servers distributed as binaries. Go compiles to a
standalone binary with no runtime dependency and has the most mature codegen tooling. The
hand-written Go in this repo is confined to thin glue (auth, the codegen entrypoint, the grouping
config); the bulk of the provider is generated.

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
   pulschema  ──────────────▶  schema.json  +  metadata.json
        │                              │
        │      resource-grouping config (operationId → resource token + verbs)
        ▼                              ▼
  pulumi-provider-framework runtime (Go plugin binary: pulumi-resource-unifi)
        │
        ▼
  pulumi package gen-sdk --language python  ──────▶  sdk/python  (pulumi_unifi)
```

The pipeline is **deterministic**: same pinned spec + same grouping config → identical
`schema.json`, `metadata.json`, and SDK. Regeneration is driven by bumping the vendored spec
version.

## 4. Resource grouping (the one editorial layer)

OpenAPI is **operation-centric** (a flat list of path+method operations); Pulumi is
**resource-centric** (one resource = a CRUD lifecycle around one entity). pulschema translates
*types* but does not decide which operations form a resource. A small config supplies that:

- Group an entity's operations into one resource token, e.g.
  `createWifiBroadcast` / `getWifiBroadcastDetails` / `updateWifiBroadcast` / `deleteWifiBroadcast`
  → `unifi:network:WifiBroadcast`, binding create/read/update/delete to the right endpoints.
- Specify ID extraction (the response field — typically `id` — that becomes the Pulumi resource ID).
- Entities exposing only `GET` (list/details) → **data sources** (Pulumi functions), not resources.

The config is keyed off the spec's **stable** `operationId` patterns and the
`/v1/sites/{siteId}/<entity>/{id}` path shape, so it survives spec version bumps with minimal churn.

## 5. Self-scoping to the official API's write coverage

As of mid-2026 the official API key is **largely read-only** — Ubiquiti is rolling write endpoints
out through 2026. Today only ~20–30% of UniFi config is writable via the official API (centered on
**WiFi broadcasts / SSIDs** and any Early-Access write endpoints present in the pinned controller
version); **reads cover all entities**.

The provider does not hardcode a resource list. It classifies each entity by the verbs the pinned
spec exposes:

- has create/update/delete → managed **resource** (full CRUD lifecycle)
- GET-only → **data source**

So the provider's writable surface is exactly what the pinned controller version supports.
**Bumping the pinned spec to a newer version auto-promotes** read-only entities to full resources
as Ubiquiti ships their writes — no per-resource hand-coding, just a spec bump + regenerate.

## 6. Provider configuration & auth

Provider config (mirrors the knobs in the legacy `iac/unifi/providers.tf`):

| Key | Notes |
|---|---|
| `apiUrl` | e.g. `https://<console>/proxy/network/integration/v1` |
| `apiKey` | **secret**; sent as the `X-API-KEY` request header |
| `allowInsecure` | accept a self-signed controller TLS cert |
| `siteId` | default site (e.g. `default`/`home`); per-resource override allowed |

Auth + base-URL injection live in the `pulumi-provider-framework` runtime wiring — the one place
the provider knows the wire protocol.

## 7. Distribution

- **Plugin binary:** built per OS/arch, attached to GitHub Releases. `schema.json` carries a
  `PluginDownloadURL` templated with `${VERSION}/${OS}/${ARCH}`, so `pulumi up` auto-installs the
  plugin — no manual `pulumi plugin install`.
- **Python SDK:** generated by `pulumi package gen-sdk` and published to PyPI (or installed from a
  pinned git ref). Consumers `pip install pulumi-unifi` and `import pulumi_unifi`.

## 8. Open questions / risks

- **Write coverage is a schedule risk, not a tooling one.** The writable resource set grows as
  Ubiquiti ships endpoints through 2026.
- **Polymorphic schemas** (`oneOf` + `discriminator`) must map to Pulumi tagged-union types —
  validate pulschema's output here specifically (the WiFi broadcast standard/IoT variants are the
  first test case).
- **`siteId`** — confirm single-site (`home`) vs multi-site provider config; default + override.
- **License/provenance** of the vendored spec — pin a commit SHA, document it.
