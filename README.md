# pulumi-unifi

A **code-generated Pulumi provider** for Ubiquiti's official **UniFi Network Integration API**
(`/proxy/network/integration/v1/...`, `X-API-KEY` auth).

> **Status: docs-only scaffold.** This repo currently contains the design and build plan. No
> provider code, codegen, or SDK has been built yet. Start from [docs/BUILD-PLAN.md](docs/BUILD-PLAN.md).

## What this is

The provider is generated from the OpenAPI 3.1 specs published by
[beezly/unifi-apis](https://github.com/beezly/unifi-apis), which are auto-extracted from UniFi
controllers (one JSON per controller version). Those specs are machine-consumable — full `paths`,
typed parameters, request/response schemas via `$ref`, `oneOf`+`discriminator`, clean
`operationId`s, and a REST-ful `/v1/sites/{siteId}/<entity>/{id}` shape — which makes a real,
regenerable Pulumi provider feasible.

The pipeline (see [docs/DESIGN.md](docs/DESIGN.md)):

```
beezly OpenAPI spec ─▶ pulschema ─▶ Pulumi schema + CRUD metadata
                          │
       resource-grouping config (operationId → resource + verbs)
                          ▼
   pulumi-provider-framework (Go REST-CRUD plugin) ─▶ pulumi package gen-sdk ─▶ Python SDK
```

## Scope

- **Official API only.** Targets Ubiquiti's documented Network Integration API with `X-API-KEY`
  auth — not the legacy private controller API.
- **Self-scoping to write coverage.** The official API is largely read-only today (Ubiquiti is
  rolling write endpoints out through 2026). The provider classifies each spec entity by the verbs
  the pinned spec exposes: entities with create/update/delete become managed **resources**, GET-only
  entities become **data sources**. Bumping the pinned spec to a newer controller version
  auto-promotes data sources to full resources as Ubiquiti ships writes.

## Why it exists

It's the prerequisite for migrating the `unifi/` Terraform stack in the separate `iac` repo to
Pulumi. Consuming the generated SDK from `iac` is a **future, separate effort** — out of scope for
this repo, which only produces the provider + SDK.

## Repo layout

```
README.md
docs/
  DESIGN.md        # technical design: toolchain, pipeline, resource grouping, auth, distribution
  BUILD-PLAN.md    # phased build sequence + verification checklist (start here to build)
openapi/           # vendored, pinned beezly spec + fetch.sh        (to be added)
provider/          # Go module: plugin binary, codegen, grouping config  (to be added)
sdk/               # generated Python SDK (pulumi_unifi)            (to be added)
```

## Getting started (for the build session)

1. Read [docs/DESIGN.md](docs/DESIGN.md) for the architecture.
2. Follow [docs/BUILD-PLAN.md](docs/BUILD-PLAN.md) phase by phase.
