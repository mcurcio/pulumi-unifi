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
therefore an **offline/local tier, not stock CI** (see `test/e2e/README.md`). The private-API
binding's slice 2 is built through §S2.12 step 6b, including the step-2 layer move (one engine, per-API
bindings, `surface.json`; docs/design/private-binding.md §S2.14 row 40); step 6c (serving IPv6 ACL
rules) waits for a Network version that accepts them. Remaining: Phase 5 (release) and CI for Tier-1
only. Read [docs/DESIGN.md](docs/DESIGN.md) for architecture,
[docs/BUILD-PLAN.md](docs/BUILD-PLAN.md) for the sequence. All work happens in this repo; **do not
modify the separate `iac` repo** — consuming the SDK there is a later, separate effort.

## What this builds

A standalone, regenerable, native **Pulumi provider** for Ubiquiti's official **UniFi Network
Integration API** (`/proxy/network/integration/v1/...`, `X-API-KEY` auth), code-generated from
[beezly/unifi-apis](https://github.com/beezly/unifi-apis) OpenAPI 3.1 specs. Targets the documented
official API by default, plus an **opt-in private-API binding** (`unifi:network:*` kinds) built
locally with `PRIVATE=1` from the pinned UniFi OS Server installer and not published (see
`docs/design/private-binding.md`). Not terraform-bridge.

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
`make test` fetch + regenerate them on demand; the plugin `//go:embed`s five generated files at
compile time: `schema.json`, `metadata.json`, `openapi_generated.yml` (the framework's inputs) plus
`schema.merged.json` (what GetSchema serves) and `surface.json` (the engine's surface: every
engine-served kind, tagged with the API that serves it). At `PRIVATE=0` `schema.merged.json` is
byte-equal to the golden (`TestNoModelIsGolden`) and `surface.json` holds no private kinds, only the
official engine-served kinds (today `unifi:sites/v1:AclRuleOrdering`). Only source is tracked: the pin manifest (`openapi/SOURCE` + `fetch.sh`), codegen Go,
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
the pinned 10.4.57 spec yields **21 framework resources + 50 data sources**, plus **1 engine-served
singleton** (`AclRuleOrdering`, from `mappings.yaml` `singletons:`; 22 resources in the golden). Bumping
the pinned spec auto-promotes data sources to resources as writes land. No per-resource hand-coding.

## Provider config

(See DESIGN §6.) `apiKey` (env `UNIFI_APIKEY`, **secret** → bare `X-API-Key` header value), `apiHost`
(env `UNIFI_API_HOST`, host swap only — there is no full-URL override), `siteId` (env `UNIFI_SITEID`,
fills `{siteId}`, defaults to `default`), `allowInsecure` (env `UNIFI_ALLOW_INSECURE`, bool, default
off). With `allowInsecure` off, trust the controller CA at the OS level or via `SSL_CERT_FILE` (HTTP
client byte-for-byte unchanged). `allowInsecure=true` replaces the transport to skip TLS verification
for self-signed certs — **caveat:** the framework's 429 rate-limit retry wrapper is unexported, so the
insecure path loses automatic 429 retry (framework resources only: the engine-served kinds use the
`provider/pkg/controller` client, which retries 429 on both TLS paths). At `PRIVATE=1` the private kinds
use the **same** `apiKey`/`apiHost`/`allowInsecure` through the authenticated edge
`https://<apiHost>/proxy/network` (v2 and classic, no login); `networkBaseUrl` (private fragment only)
overrides that root for the pre-wizard Network app, and a version guard fails the private kinds (only)
when the controller's Network version is not the pin's.

## Commands

`make fetch` (download the pinned spec — build artifact, gitignored), `make generate` (fetch → schema/
metadata/openapi_generated.yml + Python SDK; deterministic — re-run yields no diff), `make build`
(fetch → generate → plugin binary `bin/pulumi-resource-unifi`), `make test` (Go unit gate, no Docker;
fetches the spec first), `make test-mock` (Tier-1 read + write dispatch tests against the Prism TLS
mock; needs Docker), `make install` (plugin into local Pulumi cache). `PRIVATE=1` (with the pinned UniFi OS Server installer) merges
the private-API kinds (`unifi:network:*`, docs/design/private-binding.md) into the gitignored `schema.merged.json`, the
plugin and the SDK; the default `PRIVATE=0` leaves every artifact identical to the golden. Also: `ensure`, `gen`, `generate_schema`,
`python_sdk`, `clean`. `make build`/`test`/`generate` all auto-fetch the spec if absent.
Private API (local/offline, needs the pinned 875 MB UniFi OS Server installer from `uos/pin.env`;
`UOS_INSTALLER=<path>` reuses a local copy): `make uos-fetch` (installer → cache outside the repo),
`make uos-model` (→ gitignored `build/uos/model.json`), `make private-check` (model digest, pin ties,
`kinds.yaml` resolution, extract-twice determinism), `make uos-parity` (Go extractor vs the
`tools/uos-extract` prototype and the shipped official spec). See `docs/design/private-binding.md`.

Prereqs: Go 1.26, Pulumi CLI, Python 3 + venv, network access to GitHub (build fetches the pinned
spec), Docker (Compose v2) for the mock/e2e tiers, and for live testing a reachable **UniFi OS
Server** with a minted `X-API-Key` (Network app → Settings → Integrations).

## Tests (three tiers)

- **Tier 1 — `make test`** (no Docker): codegen determinism, spec sanitization, `FixOpenAPIDoc`, provider
  callbacks. Default gate. **`make test-mock`** (Docker): reads a data source and dispatches
  create/update/delete through the real provider over TLS against a Prism mock of
  `openapi_generated.yml` behind a Caddy TLS front (cert trusted via `allowInsecure`).
- **Tier 2 — `make test-e2e`** (Docker + caps; OFFLINE/local, not stock CI): restores a committed seed
  (a full `uos_data` volume snapshot — Mongo + Postgres-with-the-key-secret + configs, since `/data`
  symlinks into `/unifi`) into a real **UniFi OS Server** (`hieutq/unifi-os-server`, pinned by digest —
  the only image that authenticates the Integration API), JSON-gates readiness, then runs a real
  authenticated CRUD round-trip + live read, then a Pulumi CLI driver (`test/e2e/cli`, PRIVATE=0 SDK,
  pinned CLI 3.230.0 via `PULUMI=`) for the ACL rules, their ordering and the TrafficMatch* alias
  migration. The boot recipe is `test/live/lib.sh`, with sandbox workarounds (privileged, a discovery
  stub, IPv6 listen stripping) that `E2E_SANDBOX=auto` turns on only when the kernel has no IPv6. The one-time bake (`make e2e-bootstrap`) needs a manual
  first-run wizard + key mint (no headless API); thereafter every run is deterministic. See
  `test/e2e/README.md`.
- **Private tier — `make test-e2e-private`** (privileged Docker + the pinned installer; OFFLINE/local):
  builds the `PRIVATE=1` plugin + SDK, boots UniFi OS Server from the **same pinned installer's** image
  (no wizard, no key, no `hieutq`), and drives the Pulumi CLI through create/read/no-op/update/drift/
  import/destroy for the 4 `unifi:network:*` kinds. See `test/e2e-private/README.md`.
- **Edge tier — `make test-e2e-edge`** (Tier-2 seed + the pinned installer; OFFLINE/local): the same
  private driver in edge mode on the seeded, wizard-completed controller, through the authenticated
  edge with the seed's key (`E2E_DRIVERS=edge test/e2e/run.sh`): the 4 v2 kinds, then (edge only) the
  classic `UserGroup` and the `SettingNtp` settings singleton (adopt on create, release on destroy; the
  original ntp settings are put back and checked). `make test-live` runs the gRPC, CLI and edge drivers
  on one boot.

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
  body) and prunes orphan keys → all 21 framework resources round-trip full CRUD; `crudMap` keys =
  exactly the live resource + function tokens, minus the engine-served singletons (which have none).
  Long-term fix is upstream.
- **ACL rules (slice 2):** the official ACL rule variants are `AclRuleIpv4`/`AclRuleMac` (renamed from
  `TrafficMatchIpv4`/`TrafficMatchMac`; `mappings.yaml` `aliases:` keeps the old tokens as type aliases so
  state migrates in place). `index` is output only (`outputOnly:`); the order is `AclRuleOrdering`, served
  by `provider/pkg/engine` through `provider/pkg/binding/official` over `provider/pkg/controller`, not by
  the framework; in `surface.json` it is Kind data (`shape: singleton`, `keyedBySite`, a field with
  `merge: leadingBlock`), never a name check. A variant's own `required` list is lifted into `requiredInputs` (`pass_variant_required.go`).
  See docs/design/private-binding.md Part 2.
- **Private kinds at `PRIVATE=1` (slice 2):** 4 v2 kinds, the classic `UserGroup` and the settings
  singleton `SettingNtp` (`shape: singleton`). `AclRuleIpv6` (the G-Q4 variant exception: only
  `type: IPV6`, which the official API does not serve) is built and Tier-1 tested but **NOT SERVED**:
  Network 10.4.57 and 10.5.67 both refuse IPv6 ACL rules, so `kinds.yaml` leaves `servedFrom` unset.
  `CheckTwinVariants` (in `make test`) fails once the official API serves IPV6 itself. Classic kinds
  need measured `verbs:`/`listPath:`/`itemGet:` entries (the model leaves them unknown).
- **Layers (private-binding.md §S2.2):** `provider/pkg/dispatch` routes (a token in `surface.json` →
  the engine; another `unifi:network:*` → refused; else → framework); `provider/pkg/engine` is the one
  lifecycle for every engine-served kind and knows Kinds, not wire formats; `provider/pkg/binding/
  {official,v2,classic}` know wire formats, not Pulumi; `provider/pkg/controller` is the one client (TLS
  policy, `X-API-Key`, 429 retry). The stored-id prefix is `Binding.API()` (`private` for v2 and
  classic, `official`), so `private:<id>` ids in state resolve unchanged. `TestLayerBoundaries` checks
  the imports. `provider/pkg/private` keeps only the overrides, the private gen and the model checks.
- **Determinism:** re-running `make generate` must produce no diff in `schema.json` / `metadata.json` /
  `sdk/`; keep `ensureSchemaTitles`.
- Test write round-trips on a **throwaway test SSID** only; never commit a real `X-API-Key`.
