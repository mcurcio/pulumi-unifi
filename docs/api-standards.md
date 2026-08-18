# UniFi Provider — API Standards & Stability

This document states the naming, shape, and stability rules for the public
surface of the `unifi` Pulumi provider — the package **schema** and the SDKs
generated from it. It is hand-written and lives on `main`; the machine-checkable
specifics (the module set, the secret and immutable claim lists) live in
[`docs/api-standards.yaml`](api-standards.yaml) and are bound to the frozen
schema by tests, so this prose cannot quietly become false.

The design and rationale behind this contract are in
[`docs/design/api-contract.md`](design/api-contract.md).

## 1. What is guaranteed

The provider's public surface is generated from a **volatile upstream OpenAPI
spec** (`beezly/unifi-apis`, pinned in `openapi/pin.env`). To stop upstream churn
from silently changing the public API, the committed
`provider/cmd/pulumi-resource-unifi/schema.json` is **the source of truth for the
public API**, frozen byte-for-byte and owned by this project.

The contract makes four promises, in descending strength. What is **enforced** (a
required CI check fails otherwise) versus what is **convention** is stated
honestly:

| Property | Enforcement rung | Mechanism |
| --- | --- | --- |
| Any surface change is seen (byte freeze) | **enforced (test)** | `TestSchemaMatchesGolden` / `TestMetadataMatchesGolden` (`make schema-check`) |
| A token-invariant breaking change is not merged unacknowledged | **enforced (test)** | `TestNoUnacknowledgedBreakingDelta` (`make schema-delta`, CI supplies the base) |
| Naming / shape / secret / immutable invariants hold on the shipped artifact | **enforced (test)** | `TestContractLint` (`make lint-schema`) |
| Doc claims (module set, secret/immutable) match the golden | **enforced (test)** | `TestStandardsInventoryMatchesGolden` |
| Additive vs breaking *category* | **advisory** | `schema-tools` (`schema-contract` job, never fails the build) |
| The chosen **version bump** matches the classification | **convention** | policy prose in this doc (§4); a release-time gate is deferred |

1. **Byte-exact freeze.** Regenerating from the pinned spec must reproduce the
   committed `schema.json` and `metadata.json` byte-for-byte. Any drift — semantic
   or cosmetic — fails the required `contract` job. Moving the surface is an
   explicit `make schema` + human-reviewed commit.
2. **Breaking-delta acknowledgement.** A hard `go test` diffs the base-branch
   golden against the regenerated schema for the breaking classes a green rebase
   would otherwise hide (added required inputs, removed outputs, type narrowing,
   `replaceOnChanges`/`secret` flips) and **fails unless the change is
   acknowledged** in `CHANGELOG.md`.
3. **Convention conformance.** A Go linter binds the frozen schema and asserts the
   naming/shape invariants (token grammar, non-empty top-level descriptions,
   secret config and secret type properties, immutable/replace flags, no leaked
   excludes, enum canonicalization).
4. **Docs-truth.** This doc's machine-checkable inventory (`api-standards.yaml`)
   is bound to the frozen schema — a module bijection plus an assertion of every
   immutable and secret claim, config-level *and* type-level. The **per-token
   census lives in the golden itself**, not in a hand-authored list.

What the contract does **not** cover: runtime/wire CRUD behavior (owned by the
mock and e2e tiers), the gitignored `openapi_generated.yml` internal artifact, and
SDK-surface stability across a Pulumi codegen-toolchain bump. See
[`docs/design/api-contract.md`](design/api-contract.md) §1 and §7 for the full
list of stated non-goals and residual risks.

## 2. Naming conventions

Every resource and function token follows the grammar
`unifi:<module>/<version>:<PascalCase>`:

- **Module/version** — the `<module>/<version>` segment (e.g. `sites/v1`). The
  closed set of allowed modules is declared in
  [`api-standards.yaml`](api-standards.yaml) `modules:` and is bound to the golden
  by a bijection; a new module is a deliberate, reviewed event, never an accident
  of an upstream bump.
- **Resources** use a `PascalCase` short name (e.g. `FirewallPolicy`,
  `DnsARecord`).
- **Functions** (data sources) use a `get<Entity>` / `list<Entities>` short name
  (e.g. `getFirewallPolicy`, `listFirewallPolicies`).

The module set and the secret/immutable specifics are **not** duplicated in this
prose — they live in the machine-checked `api-standards.yaml`.

## 3. Resource shape rules

- **Immutable identity → `replaceOnChanges`.** Inputs that form a resource's
  identity are marked `replaceOnChanges`, so changing one forces a replace rather
  than an in-place update the API would reject or silently drop. The declared
  immutable inputs are `siteId` (the per-resource site-scope override — the API
  has no move-to-another-site edit) and `vlanId` (a network's VLAN is identity).
  Declared in `api-standards.yaml` `guarantees.immutableInputs`.
- **Secret config + secret type properties.** Credential-bearing fields are marked
  `secret` so Pulumi redacts them in state and CLI output. This holds at **config
  level** (`apiKey`) and at **type level** (`unifi:sites/v1:HotspotVoucherDetails`
  property `code`, a guest-access voucher credential). Declared in
  `api-standards.yaml` `guarantees.secretConfig` and `guarantees.secretProperties`.
- **Discriminated variants are distinct tokens.** A discriminated entity (e.g. a
  managed network's gateway/switch/unmanaged variants) is fragmented into distinct
  PascalCase resource tokens, each with its discriminator pinned to a `Const` and
  marked immutable. There is no single polymorphic resource.

## 4. Stability & deprecation policy

### Breaking-change taxonomy

A change to the surface is classified against the committed golden (see
[`docs/design/api-contract.md`](design/api-contract.md) §4.1 for the full list):

- **Breaking** — token removal/rename, a token kind flip (resource ↔ function),
  an **added required input** (or an optional input made required), a **removed
  output property**, a **type narrowing** (`string` → enum, enum value removal,
  array → scalar), an **added `replaceOnChanges`**, or a **removed `secret`**.
- **Additive** — a new resource/function token, a new optional input, a new output
  property, a new enum value, or a newly-added `secret` flag.
- **Cosmetic** — description text changes. (Ordering churn should never occur — the
  pipeline is fully sorted; if it does, it is a determinism bug.)

### Breaking-change acknowledgement (enforced)

The byte-golden catches *every* change relative to the committed golden. The
token-invariant breaking classes (added required input, removed output, type
narrowing, `replaceOnChanges`/`secret` flip) are **invisible to a green
`make schema` rebase** because they do not move the token set, so they are
additionally gated by `TestNoUnacknowledgedBreakingDelta` (`make schema-delta`):
when such a change is present, the `## [Unreleased]` section of `CHANGELOG.md`
**must** carry a non-empty `### Breaking` bullet, or the required `contract` CI job
fails. This converts "a breaking change merges unseen" into "a breaking change is
impossible without the author writing a breaking-changelog line." The
acknowledgement is *presence*-granular, not per-change; tightening is deferred.

## 5. How the contract is enforced

| Gate | Target | Type |
| --- | --- | --- |
| Byte freeze of `schema.json` + `metadata.json`, token set, linter, doc bijection | `make schema-check` | required (`contract` job) |
| Breaking-delta acknowledgement vs the base branch | `make schema-delta` | required (`contract` job) |
| Convention linter alone | `make lint-schema` | required (subset of `schema-check`) |
| Additive/breaking category annotation | `make schema-compare` | advisory (`schema-contract` job) |

To move the public surface deliberately (a reviewed spec bump, a new resource, a
shape change), run **`make schema`** — the only sanctioned writer of the goldens —
review the resulting `git diff`, and commit it. A red `contract` job means the
surface changed without that reviewed rebase, or a breaking change lacks its
`### Breaking` acknowledgement.

The machine-checked specifics this doc refers to (the module set, the immutable and
secret claim lists) are declared once in
[`docs/api-standards.yaml`](api-standards.yaml) and bound to the frozen schema by
`TestContractLint` and `TestStandardsInventoryMatchesGolden`.
