# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

**Read path + write-path codegen complete AND the live Tier-2 round-trip works (BUILD-PLAN Phases
1–4).** The Go module, deterministic codegen (`schema.json`/`metadata.json`/`openapi_generated.yml`),
Python SDK, provider config, read path, and a test harness all exist. Phase 4 write enablement landed
at Tier-1 (`coalesceDiscriminatedCRUD` — all 21 resources round-trip full CRUD — `allowInsecure`, and
list-pagination auto-aggregation, verified by unit tests + the Prism write-dispatch test) AND is now
proven live: `make test-e2e` runs a real authenticated Create→Read→Diff→Update→Delete round-trip
(`DnsARecord`) + a live data-source read against a **real UniFi OS Server** restored from a committed
seed. **Key finding:** the Integration API is UniFi-OS-coupled — the standalone Network app refuses it
(403, mode-refusal), so the live tier MUST use the heavy `hieutq/unifi-os-server` image and is
therefore an **offline/local tier, not stock CI** (see `test/e2e/README.md`). Remaining: Phase 5
(release) and CI for Tier-1 only. Read [docs/DESIGN.md](docs/DESIGN.md) for architecture,
[docs/BUILD-PLAN.md](docs/BUILD-PLAN.md) for the sequence. All work happens in this repo; **do not
modify the separate `iac` repo** — consuming the SDK there is a later, separate effort.

## What this builds

A standalone, regenerable, native **Pulumi provider** for Ubiquiti's official **UniFi Network
Integration API** (`/proxy/network/integration/v1/...`, `X-API-KEY` auth), code-generated from
[beezly/unifi-apis](https://github.com/beezly/unifi-apis) OpenAPI 3.1 specs. Targets the documented
official API only — not the legacy private controller API or terraform-bridge.

## Codegen pipeline

The provider is **generated, not hand-written** — the pinned spec is the source of truth. Hand-written
Go is confined to thin glue (auth callbacks, codegen entrypoint, spec fixes + an `ExcludedPaths` list).

```
beezly spec (pinned SHA, fetched at build by openapi/fetch.sh — gitignored)
   └─▶ FixOpenAPIDoc (inject X-API-Key scheme, rewrite server URL) + ExcludedPaths
          └─▶ pulschema ─▶ schema.json + metadata.json + openapi_generated.yml
                 │   (grouping auto-derived from path shape + verbs)
                 ▼
          pulumi-provider-framework (Go gRPC plugin: pulumi-resource-unifi)
                 └─▶ pulumi package gen-sdk --language python ─▶ sdk/python (pulumi_unifi)
```

**Nothing generated or fetched is committed.** The pinned spec, `schema.json`, `metadata.json`,
`openapi_generated.yml`, and `sdk/python/` are all build artifacts (gitignored). `make build` and
`make test` fetch + regenerate them on demand; the plugin `//go:embed`s the three generated files at
compile time. Only source is tracked: the pin manifest (`openapi/SOURCE` + `fetch.sh`), codegen Go,
provider glue, and `ExcludedPaths`/fixes.

Toolchain roles:
- [pulschema](https://github.com/cloudy-sky-software/pulschema) — OpenAPI → Pulumi schema + CRUD endpoint map. Translates types/`$ref`, and **auto-derives resource grouping** from the path shape + verbs. A `oneOf`+`discriminator` body is split into **one resource per variant**, not a Pulumi tagged union.
- [pulumi-provider-framework](https://github.com/cloudy-sky-software/pulumi-provider-framework) — generic Go HTTP CRUD runtime executing `metadata.json`. This is the plugin binary. It derives the **auth header name from the spec's security scheme** and swaps **only the server host** (`apiHost`) at runtime. The framework issues a single GET per list read; the provider's `OnPostInvoke` glue auto-aggregates `offset`/`limit` pages (limit 200) into one result.
- `pulumi package gen-sdk` — emits language SDKs (Python first).

The pipeline is **deterministic**: same pinned spec + same fixes/exclusions → identical
`schema.json`, `metadata.json`, and SDK. Regeneration is driven by bumping the pinned spec version,
not by editing generated output. (Determinism required `ensureSchemaTitles`: every component schema
gets a unique title so discriminated-getter names don't collapse.) After pulschema runs, a
deterministic gen-layer post-process `coalesceDiscriminatedCRUD` repairs the discriminated resources'
CRUD bindings (see Gotchas).

## Two key design concepts

**Resource grouping (auto-derived; the editorial layer is exclusions)** — OpenAPI is operation-centric;
Pulumi is resource-centric. **pulschema auto-derives** the grouping from the
`/v1/sites/{siteId}/<entity>[/{id}]` path shape and the verbs present — there is **no hand-authored
`grouping.{go,yaml}`**. The narrow editorial surface is `ExcludedPaths` (in `provider/pkg/gen/schema.go`,
dropping non-CRUD RPC/ordering endpoints) plus `FixOpenAPIDoc` / `SanitizeSpecBytes` (making the
low-quality spec consumable). Derived tokens are `sites/v1`-based, e.g. `unifi:sites/v1:Gateway`.

**Self-scoping to write coverage** — the provider does not hardcode a resource list. Each entity is
classified by the verbs the pinned spec exposes: create/update/delete → managed **resource**; GET-only
→ **data source**. The official API is **partially writable** today (Ubiquiti ships writes through 2026);
the pinned 10.4.57 spec yields **21 resources + 50 data sources**. Bumping the pinned spec auto-promotes
data sources to resources as writes land. No per-resource hand-coding.

## Provider config

(See DESIGN §6.) `apiKey` (env `UNIFI_APIKEY`, **secret** → bare `X-API-Key` header value), `apiHost`
(env `UNIFI_API_HOST`, host swap only — there is no full-URL override), `siteId` (env `UNIFI_SITEID`,
fills `{siteId}`, defaults to `default`), `allowInsecure` (env `UNIFI_ALLOW_INSECURE`, bool, default
off). With `allowInsecure` off, trust the controller CA at the OS level or via `SSL_CERT_FILE` (HTTP
client byte-for-byte unchanged). `allowInsecure=true` replaces the transport to skip TLS verification
for self-signed certs — **caveat:** the framework's 429 rate-limit retry wrapper is unexported, so the
insecure path loses automatic 429 retry.

## Commands

`make fetch` (download the pinned spec — build artifact, gitignored), `make generate` (fetch → schema/
metadata/openapi_generated.yml + Python SDK; deterministic — re-run yields no diff), `make build`
(fetch → generate → plugin binary `bin/pulumi-resource-unifi`), `make test` (Go unit gate, no Docker;
fetches the spec first), `make test-mock` (Tier-1 read + write dispatch tests against the Prism TLS
mock; needs Docker), `make install` (plugin into local Pulumi cache). Also: `ensure`, `gen`, `generate_schema`,
`python_sdk`, `clean`. `make build`/`test`/`generate` all auto-fetch the spec if absent.

Prereqs: Go 1.26, Pulumi CLI, Python 3 + venv, network access to GitHub (build fetches the pinned
spec), Docker (Compose v2) for the mock/e2e tiers, and for live testing a reachable **UniFi OS
Server** with a minted `X-API-Key` (Network app → Settings → Integrations).

## Tests (two tiers)

- **Tier 1 — `make test`** (no Docker): codegen determinism, spec sanitization, `FixOpenAPIDoc`, provider
  callbacks. Default gate. **`make test-mock`** (Docker): reads a data source and dispatches
  create/update/delete through the real provider over TLS against a Prism mock of
  `openapi_generated.yml` behind a Caddy TLS front (cert trusted via `allowInsecure`).
- **Tier 2 — `make test-e2e`** (Docker + caps; OFFLINE/local, not stock CI): restores a committed seed
  (a full `uos_data` volume snapshot — Mongo + Postgres-with-the-key-secret + configs, since `/data`
  symlinks into `/unifi`) into a real **UniFi OS Server** (`hieutq/unifi-os-server`, pinned by digest —
  the only image that authenticates the Integration API), JSON-gates readiness, then runs a real
  authenticated CRUD round-trip + live read. The one-time bake (`make e2e-bootstrap`) needs a manual
  first-run wizard + key mint (no headless API); thereafter every run is deterministic. See
  `test/e2e/README.md`.

## Gotchas

- **Spec provenance:** beezly repo has **no license**. The spec is **fetched at build** from a pinned
  commit SHA (recorded in `openapi/SOURCE` + `fetch.sh`, checksum-verified) and **not committed** —
  treated strictly as a codegen input. Re-extractable from an owned controller. Builds require network
  access to GitHub; bump the SHA + checksum to update.
- **Low-quality capture:** the spec has invalid component keys, type-less schemas, and `null` items that
  crash pulschema; `SanitizeSpecBytes` normalizes them.
- **`oneOf` + `discriminator`** → pulschema emits **per-variant resources** (e.g. WiFi broadcast →
  `Standard` / `IotOptimized`), **not** Pulumi tagged unions. Build consumer code on the concrete variants.
- **CRUD fragmentation (repaired in gen):** that same split scatters an entity's verbs across per-verb
  tokens — pulschema binds only **create** to each variant, leaving 18 of 21 resources as create-only
  stubs that die on the next `pulumi up` (read endpoint unknown). `coalesceDiscriminatedCRUD`
  (`provider/pkg/gen/schema.go`, deterministic, analogous to `FixOpenAPIDoc`) fills the missing
  R/U/D/P from the shared item path (`P_coll + "/{param}"`; the discriminator rides in the request
  body) and prunes orphan keys → all 21 resources round-trip full CRUD; `crudMap` keys = exactly the
  live resource + function tokens. Long-term fix is upstream.
- **Determinism:** re-running `make generate` must produce no diff in `schema.json` / `metadata.json` /
  `sdk/`; keep `ensureSchemaTitles`.
- Test write round-trips on a **throwaway test SSID** only; never commit a real `X-API-Key`.
