# pulumi-unifi

A **code-generated Pulumi provider** for Ubiquiti's official **UniFi Network Integration API**
(`/proxy/network/integration/v1/...`, `X-API-KEY` auth).

> **Status: read + write paths complete; Phase 4 proven at Tier-1.** See **Project scope** below for
> what exists today vs. what's in development. Full sequence in [docs/BUILD-PLAN.md](docs/BUILD-PLAN.md).

## Documentation

Full docs are published at **<https://mcurcio.github.io/pulumi-unifi/>** (MkDocs
Material, versioned with `mike` — the `latest` alias tracks the newest stable
release). The site covers installation, authentication, a getting-started
example, and an auto-generated API reference. Build it locally with `make docs`.

## What this is

The provider is generated from the OpenAPI 3.1 specs published by
[beezly/unifi-apis](https://github.com/beezly/unifi-apis), which are auto-extracted from UniFi
controllers (one JSON per controller version). Those specs are machine-consumable — full `paths`,
typed parameters, request/response schemas via `$ref`, `oneOf`+`discriminator`, clean
`operationId`s, and a REST-ful `/v1/sites/{siteId}/<entity>/{id}` shape — which makes a real,
regenerable Pulumi provider feasible.

The pipeline (see [docs/DESIGN.md](docs/DESIGN.md)):

```
beezly OpenAPI spec ─▶ FixOpenAPIDoc ─▶ pulschema ─▶ Pulumi schema + CRUD metadata
                                          │  (grouping auto-derived from path + verbs)
                                          ▼
   pulumi-provider-framework (Go REST-CRUD plugin) ─▶ pulumi package gen-sdk ─▶ Python SDK
```

## Scope

- **Official API only.** Targets Ubiquiti's documented Network Integration API with `X-API-KEY`
  auth — not the legacy private controller API.
- **Self-scoping to write coverage.** The official API is partially writable today (Ubiquiti is
  rolling write endpoints out through 2026). The provider classifies each spec entity by the verbs
  the pinned spec exposes: entities with create/update/delete become managed **resources**, GET-only
  entities become **data sources**. The pinned 10.4.57 spec yields **21 resources + 50 data
  sources**; bumping the spec auto-promotes data sources to resources as Ubiquiti ships writes.

## Project scope (built vs. in development)

The end goal is a regenerable, native Pulumi provider with full read + write coverage of the
official API, released with a `PluginDownloadURL` and a published Python SDK. Where that stands:

**Built (Phases 1–3 + Phase 4 at Tier-1):**
- Deterministic codegen: pinned spec → `schema.json` / `metadata.json` / `openapi_generated.yml` →
  Python SDK. Re-running `make generate` produces no diff (guarded by `TestPipelineDeterministic`).
- Plugin binary (`make build`), provider config (`apiKey` / `apiHost` / `siteId` / `allowInsecure`),
  and the read **and write** paths wired through the framework.
- **Full CRUD for all 21 resources.** pulschema fragments each discriminated entity's verbs across
  per-variant tokens (only `create` binds); `coalesceDiscriminatedCRUD` — a deterministic gen
  post-process — fills the missing read/update/delete from the shared item path, so every resource
  round-trips instead of dying on the second `pulumi up`.
- **List reads auto-aggregate pages** (`OnPostInvoke` follows `offset`/`limit` to completion, since
  the framework issues only one GET) and **`allowInsecure`** swaps in a verification-skipping
  transport for self-signed controllers.
- Two-tier test harness: a no-Docker unit gate (`make test`) and a Prism-TLS-mock read + write-dispatch
  test (`make test-mock`).

**Proven (Tier-1):** against a **spec-driven Prism mock** over real TLS — URL composition, response
decode, and `{siteId}` substitution for both a no-`siteId` and a site-scoped data source; create →
update → delete **dispatch** for a managed resource (`FirewallZone`). The bare `X-API-Key` header and
the `siteId=default` default are asserted on the actual wire by an httptest unit test; codegen
determinism and spec sanitization are unit-tested.

**In development / not yet proven:**
- **Live controller (Tier-2).** No passing verification against a real UniFi OS Server yet — the open
  infra task is provisioning (no headless first-run / key-minting API). This gates the true write
  round-trip (Prism is stateless, so the mock proves dispatch, not persistence) and the live read with
  `allowInsecure` against a self-signed controller.
- **Per-resource `siteId` override.** The framework already prioritizes resource-level over global; a
  confirming test + docs line remain.
- **CI / release (Phase 5).** No tagged release or `PluginDownloadURL` asset yet.

## Why it exists

It's the prerequisite for migrating the `unifi/` Terraform stack in the separate `iac` repo to
Pulumi. Consuming the generated SDK from `iac` is a **future, separate effort** — out of scope for
this repo, which only produces the provider + SDK.

## API stability

The provider's public surface (the Pulumi package `schema.json`) is **frozen
byte-for-byte** and owned by this repo, so an upstream spec bump cannot change the
public API without turning a required check red. The naming/shape/secret/immutable
rules, the enforced-vs-convention split, and the version & deprecation policy are
documented in **[docs/api-standards.md](docs/api-standards.md)** (machine-checked
against the golden via `docs/api-standards.yaml`). To move the surface
deliberately, run `make schema`, review the diff, and commit. See
[docs/design/api-contract.md](docs/design/api-contract.md) for the design.

## Repo layout

```
README.md
docs/
  api-standards.md # API naming/shape/stability contract (machine-checked via api-standards.yaml)
  DESIGN.md        # technical design: toolchain, pipeline, grouping, auth, distribution
  BUILD-PLAN.md    # phased build sequence + verification checklist
openapi/           # pin manifest (SOURCE + fetch.sh); the spec itself is fetched at build (gitignored)
provider/          # Go module: plugin binary, codegen entrypoint, pkg/{gen,provider,version}
sdk/python/        # generated Python SDK (pulumi_unifi) — build artifact, gitignored
test/
  mock/            # Tier-1: Prism mock + TLS front (make test-mock)
  e2e/             # Tier-2: UniFi OS Server, seed-restore harness (make e2e-bootstrap / test-e2e)
```

The Tier-2 e2e seed (`test/e2e/unifi-seed.tgz`) is a **git-lfs** object; run
`git lfs install && git lfs pull` once per clone before `make test-e2e`. See
[test/e2e/README.md](test/e2e/README.md) for the full runbook.

## Getting started (for the build session)

1. Read [docs/DESIGN.md](docs/DESIGN.md) for the architecture.
2. Follow [docs/BUILD-PLAN.md](docs/BUILD-PLAN.md) phase by phase.
