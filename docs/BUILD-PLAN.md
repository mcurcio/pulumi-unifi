# Build plan

Execution sequence. Read [DESIGN.md](DESIGN.md) first for the architecture.

**Status:** Phases 1–3 (the read-path MVP) are **complete** — module scaffold, deterministic
codegen, Python SDK, provider config + read path, and a two-tier test harness all exist. **Phase 4
(writes) is implemented at Tier-1** (codegen CRUD coalescing, `allowInsecure`, page aggregation,
write-dispatch mock); its live Tier-2 round-trip and Phase 5 (CI/release) are the follow-on.

The work happens **entirely in this repo** (`~/Code/pulumi-unifi`). Do not modify the separate
`iac` repo — consuming the SDK from `iac` is a later, separate effort.

## Prerequisites

- **Go 1.26** (matches `provider/go.mod`; the framework requires a recent toolchain).
- Pulumi CLI (`pulumi version`) — for `pulumi package gen-sdk`.
- Python 3 + a venv tool for SDK smoke tests.
- **Docker** (Compose v2) — for the test harness: the Prism mock (Tier 1) and the UniFi OS Server
  e2e controller (Tier 2).
- For live/e2e: a reachable **UniFi OS Server** controller with an `X-API-Key` minted (Network app →
  Settings → Integrations). Only UniFi OS Server serves `/proxy/network/integration/v1`; the legacy
  controller and the `linuxserver/unifi-network-application` image do not.

## Phase 1 — Bootstrap & vendor the spec ✅

- `openapi/fetch.sh` downloads `unifi-network/<version>.json` from a **pinned beezly commit SHA** and
  **checksum-verifies** it; `openapi/SOURCE` is the pin manifest (repo + SHA + sha256 + "no license —
  codegen input"). The fetched spec is a **build artifact — gitignored, never committed**.
- Go module under `provider/`: `cmd/pulumi-resource-unifi/` (plugin binary), `cmd/pulumi-gen-unifi/`
  (codegen entrypoint), `pkg/{gen,provider,version}`.
- `Makefile` targets: `ensure`, `fetch`, `gen`, `generate_schema`, `python_sdk`, `generate`, `build`,
  `install`, `test`, `test-mock`, `clean`. `build`/`test`/`generate` auto-fetch the spec if absent.

**Done:** `make build` from a clean (artifact-free) tree fetches the spec, regenerates, and produces
`bin/pulumi-resource-unifi`.

## Phase 2 — Wire codegen (spec → schema → SDK) ✅

- `pulumi-gen-unifi`: `SanitizeSpecBytes` → `FixOpenAPIDoc` → pulschema (`GatherResourcesFromAPI`) →
  `schema.json` + `metadata.json` + `openapi_generated.yml` (generated at build; gitignored; the plugin
  `//go:embed`s them at compile time, so `make build` runs codegen first).
- `PluginDownloadURL` set in the schema.
- `make python_sdk` → `pulumi package gen-sdk --language python --out sdk` → `sdk/python`
  (`pulumi_unifi`). Note `gen-sdk` appends the language subdir, so `--out sdk` yields `sdk/python`.

**Done:** `make generate` is deterministic (re-run → no diff), guarded by `TestPipelineDeterministic`;
`import pulumi_unifi` works.

## Phase 3 — Read path end-to-end (3a ✅ wiring+mock / 3b ⬜ live)

- Provider config wired in `provider/pkg/provider/provider.go`: `apiKey` (secret → bare `X-API-Key`
  value), `apiHost` (host swap), `siteId` (`{siteId}` global path param), `allowInsecure` (TLS-verify
  skip — added in Phase 4). See DESIGN §6 for the apiHost (not `apiUrl`) correction.
- All read-only entities are exposed as **data sources** (50 of them).

**Done — 3a (wiring + mock):** `TestReadPathAgainstMock` reads a **no-`siteId`** data source
(`getCountrie`) through the full provider + framework stack against the Prism TLS mock
(`make test-mock`), proving URL composition + TLS + decode. Auth header name/value are covered by
unit tests; Prism does not enforce auth, so the mock test does not assert the header on the wire.

**Done — 3b mock/wire gaps (siteId + auth over the wire):** `TestWirePath` (no-Docker, `httptest`
TLS) asserts the bare `X-API-Key` and `siteId=default` substitution on the actual wire — both things
Prism structurally cannot prove (it does not enforce auth). `TestReadPathAgainstMock` additionally
reads a `sites/v1` data source (`getWifiBroadcastPage`) against Prism, keeping `getCountrie` as the
no-`siteId` control.

**Pending — 3b live:** no passing read against a real UniFi OS Server yet (Tier-2 provisioning, below).

## Phase 4 — Writes (Tier-1 ✅ / live ⬜)

**Status — Tier-1 complete.** Increments A1–A6 + B1 landed: `apiHost` validation, a crudMap
consistency guard, the `allowInsecure` transport, list page auto-aggregation, expanded mock/wire
coverage, a Prism write-dispatch test, and the **codegen CRUD-coalescing fix** (B1). The live
round-trip (Tier-2) stays infra-gated.

- Resource grouping is **already auto-derived** by pulschema (DESIGN §4) — there is **no
  `grouping.{go,yaml}` to author**. The 9 writable endpoints surface as 21 CRUD resources.
- **The fragmentation surprise (B1).** pulschema splits each `oneOf`+`discriminator` union into
  per-variant resources (`Standard`/`IotOptimized`, etc.) but binds only **create** to each variant —
  so 18 of the 21 resources generated as create-only stubs that would die on the second `pulumi up`
  ("read endpoint unknown"). `coalesceDiscriminatedCRUD` (gen layer, deterministic) fills the missing
  R/U/D/P from the shared item path and prunes orphan keys, so all 21 now round-trip full CRUD
  (DESIGN §8).
- Per-resource `siteId` override — the framework already prioritizes resource-level over global, so
  this is a confirming test + docs line, not a framework change.

### Phase 4 decisions (from the triad review) — all implemented at Tier-1

- **`allowInsecure` is a HARD GATE for live-read "done."** UniFi controllers ship self-signed certs
  by default; the framework builds its HTTP client from the default transport with no insecure hook,
  so today the only path to a live read is OS-level CA trust / `SSL_CERT_FILE`. Phase 4 must wire a
  real `allowInsecure` transport. **First verify the framework permits transport injection** — it
  exposes `GetHTTPClient()` but not transport replacement; this may require wrapping/replacing the
  client post-`MakeProvider` or an upstream PR. The new flag must **not** regress the secure
  CA-trust path. *(Outcome: `GetHTTPClient()` transport injection works; the mock tier dogfoods
  `allowInsecure` because macOS's default transport ignores `SSL_CERT_FILE`, so CA-pinned trust
  moved to the Tier-2 live test.)*
- **List data sources MUST auto-aggregate pages.** The API paginates (`offset`/`limit`, page DTOs)
  and the framework issues exactly one GET — so a collection larger than one page silently returns
  partial data that *decodes cleanly* (a correctness bug that looks like success). Phase 4 adds page
  aggregation (e.g. an `OnPostInvoke` loop following `offset`/`limit` until exhausted) so reads
  return the full collection. Applies to the read path too; prioritize alongside live-read.
- **`apiHost` validation.** `OnConfigure` must reject any `apiHost` containing `://` or `/` — the
  framework does a raw `baseURL.Host = apiHost`, so a pasted URL silently corrupts the base URL
  (`https://https:%2F%2F.../...`) and fails opaquely. ~5 lines of provider-side glue.
- **crudMap consistency guard.** `metadata.json` carries ~68 inert orphan tokens (singular bases,
  `…Dto`/`…CreateUpdateDto` pairs) that are harmless on the read path but a wrong-endpoint-binding
  hazard once writes dispatch resource tokens. Add a test asserting every `crudMap` key is a live
  schema token and resource tokens bind to the item-level (`{id}`) endpoint.

**Done when:** the scratch program round-trips create → update → delete on a **throwaway test SSID**
and a no-op `pulumi up` follows a successful apply (clean read/diff); list reads return all pages;
`allowInsecure` enables a live read against a self-signed controller. *(Tier-1 proxies for all three
are green — page aggregation is unit-tested, the `allowInsecure` transport is unit-tested, and write
dispatch is proven against the Prism mock with **FirewallZone** as a flat-body stand-in. The live
SSID round-trip itself — which B1 unblocks for the `Standard`/`IotOptimized` variants — waits on
Tier-2 provisioning.)*

## Phase 5 — CI & release

- GitHub Actions: on `openapi/` version bump, regenerate schema + SDK and open a PR.
- Build plugin binaries per OS/arch; cut a release with the `PluginDownloadURL`-matching asset names.
- Publish the Python SDK (PyPI or tagged git ref).

### Phase 5 hardening (from the triad review)

- **CI determinism gate must cover all four artifacts.** Nothing is committed to diff against, so the
  guard is re-run-equality, not committed-match: `make generate` twice and byte-compare `schema.json`,
  `metadata.json`, `openapi_generated.yml`, and `sdk/` across the two runs. `TestPipelineDeterministic`
  covers schema + metadata; extend it (or a CI step) to the OpenAPI doc and SDK.
- **Token-set / exclusion drift guard.** On spec bump, diff the resource + function token set and
  require human sign-off on additions (catches `ExcludedPaths` drift and surprise resource promotion).
  Add a no-Docker unit test asserting (a) the integration token still exists in `schema.json` and
  (b) every `excludedPaths` entry matches a path in the sanitized spec — converts two silent
  spec-bump failures into loud ones in the default gate.
- **Harden `test-mock` teardown** (`Makefile`): a `compose up --wait` failure currently leaves the
  stack up (teardown is welded to the test command's exit, not bring-up). Wrap bring-up + run +
  down in one block with a trap so cleanup is unconditional.
- **Record dependency-pin rationale.** `pulschema` / `pulumi-provider-framework` are untagged
  `v0.0.0-<sha>` pseudo-versions; add a one-line note per pin (what each SHA provides) so the next
  bump isn't archaeology.

**Done when:** a tagged release's `PluginDownloadURL` resolves and `pulumi up` auto-installs the
plugin on a clean machine/container.

## Test architecture (two tiers)

- **Tier 1 — unit + mock (no real controller).**
  - `make test` (no Docker): Go tests covering codegen determinism, spec sanitization,
    `FixOpenAPIDoc` (auth/server/titles), and the provider callbacks. This is the default gate.
  - `make test-mock` (Docker): `test/mock/` runs a **Prism** mock of the *generated*
    `openapi_generated.yml` behind a **Caddy TLS** front; `TestReadPathAgainstMock` and
    `TestWritePathAgainstMock` (`//go:build integration`) drive a data-source read and a
    create/update/delete dispatch through the real provider over TLS, trusting the self-signed
    cert via the provider's own `allowInsecure=true` flag (A3).
- **Tier 2 — e2e (real controller).** `test/e2e/` scaffolds a **UniFi OS Server** container (the
  only thing serving the Integration API). The open infra task is **provisioning**: no headless
  first-run, no documented key-minting API → script the UI (Playwright) or restore a pre-baked
  volume snapshot. Same `TestReadPathAgainstMock` body, pointed at `:11443`. See `test/e2e/README.md`.

## Verification checklist

- [x] `make generate` deterministic — re-run produces no diff (`TestPipelineDeterministic`).
- [x] `make build` produces the plugin binary; `make test` (Go) green.
- [x] `import pulumi_unifi` works; data-source classes present and typed.
- [x] Read path proven against the Prism mock (`make test-mock`).
- [x] `{siteId}` + bare `X-API-Key` proven on the wire (`TestWirePath`); a `sites/v1` data source read against the mock.
- [x] Write path: all 21 resources round-trip full CRUD in `metadata.json` (B1); create→update→delete dispatch proven against the Prism mock (`FirewallZone`). *(Phase 4, Tier-1)*
- [x] `allowInsecure` transport + list page aggregation unit-tested. *(Phase 4, Tier-1)*
- [ ] Read path proven against a **live** UniFi OS Server (Tier-2 provisioning — open infra task).
- [ ] Scratch program round-trips create → update → delete on a throwaway test SSID; second `up` is a no-op. *(Phase 4, Tier-2 — live)*
- [ ] Tagged-release `PluginDownloadURL` auto-installs the plugin on a clean environment. *(Phase 5)*

## Notes / gotchas

- **Spec license:** beezly repo has no license — the spec is **fetched at build** from a pinned commit
  SHA (`openapi/SOURCE` + `fetch.sh`, checksum-verified) and never committed; treat it strictly as an
  attributed codegen input.
- **Low-quality capture:** the fetched spec has invalid component keys, type-less schemas, and
  `null` items that crash pulschema; `SanitizeSpecBytes` normalizes them (see its unit tests).
- **`oneOf`+`discriminator`** → pulschema emits **per-variant resources**, not Pulumi tagged unions
  (DESIGN §8); their fragmented CRUD is re-coalesced in the gen layer (`coalesceDiscriminatedCRUD`).
  Build consumer code around the concrete variant resources.
- **Determinism hazard:** title-less component schemas collapse discriminated-getter names —
  `ensureSchemaTitles` assigns unique titles. Keep it.
- **Write coverage** grows through 2026; bump the pinned spec to auto-promote read-only entities.
- **`allowInsecure`** — implemented (Phase 4): replaces the transport to skip TLS verify, at the cost
  of the framework's unexported 429-retry wrapper. **Per-resource `siteId`** — runtime already honors
  it (resource-level beats global); a confirming test + docs line remain.
