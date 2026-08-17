# Design: The Owned Stable API Contract

Status: PROPOSED (design only — no code in this document)
Author: code-architect (delivery/maintenance lens)
Scope: the public-surface stability contract for the `unifi` native codegen provider.
Revision: 2 (post adversarial review — see §8 Review response for the finding→resolution map).

---

## 1. Problem & the guarantee

The provider's public surface (the Pulumi package **schema**, and the SDKs
generated from it) is produced from a **volatile upstream OpenAPI spec**
(`beezly/unifi-apis`, pinned in `openapi/pin.env`). Today that surface is a
**build artifact**: `provider/cmd/pulumi-resource-unifi/schema.json`,
`metadata.json`, and `openapi_generated.yml` are all **gitignored** and
regenerated on every build (`.gitignore:16-18`). Nothing in the repo *is* the
contract — the contract is whatever the current spec + pipeline happen to emit.
An upstream churn (a renamed operation, a reordered enum, a newly-required
field) silently changes the provider's public API with no review surface and no
gate.

We already have determinism guards (run-to-run byte-stability of the artifacts,
`TestPipelineDeterministic`) and a coarse token-set golden
(`testdata/tokens.txt`, `drift_test.go`). Determinism proves the pipeline is
*reproducible*; it does **not** prove the surface is *stable across a spec
bump*. Those are different properties. `tokens.txt` is also **blind to breaking
changes that leave the token set intact** — an added required input, a removed
output property, a type narrowing, a `secret`/`replaceOnChanges` flip.

### The guarantee (narrowed to exactly what is enforced)

> The committed `schema.json` is the **source of truth for the provider's public
> API**. It is frozen byte-for-byte, owned by this project. Regenerating from the
> pinned spec must reproduce it exactly, so **upstream churn cannot alter the
> public surface without turning a required check red** — every change to the
> golden is therefore a diff a human must see and commit via `make schema`.
> **A breaking-class change that a spec rebase would otherwise slip through
> green is additionally caught by a hard delta-diagnostic and blocked until it is
> acknowledged in the changelog.** A hand-written **API Standards** doc on `main`
> states the naming/shape/stability rules, and a machine check binds the doc's
> machine-checkable claims (module set, secret and immutable properties) to the
> frozen schema so the prose cannot quietly become false.

What is **enforced** (a required check fails otherwise) versus what is
**convention** is stated precisely, because the earlier draft overclaimed:

| Property | Enforcement rung | Mechanism |
| --- | --- | --- |
| Any surface change is seen (byte freeze) | **test (required)** | `TestSchemaMatchesGolden` in `make schema-check` |
| A token-invariant breaking change is not merged silently | **test (required)** | `TestNoUnacknowledgedBreakingDelta` (`make schema-delta`, CI supplies the base) |
| Naming / shape / secret / immutable invariants hold on the shipped artifact | **test (required)** | `TestContractLint` |
| Doc claims match the golden | **test (required)** | `TestStandardsInventoryMatchesGolden` |
| Additive vs breaking *category* | **advisory** | `schema-tools` (`schema-contract` job, never fails the build) |
| The chosen **version bump** matches the classification | **convention** | policy prose in `api-standards.md`; a release-time gate is deferred (§4.2). The *acknowledgement* of a breaking change is enforced; the *number picked* is not. |

Concretely the contract makes four promises, in descending strength:

1. **Byte-exact freeze (strong gate).** Regenerating from the pinned spec must
   reproduce the committed `schema.json` and `metadata.json` byte-for-byte. Any
   drift — semantic or cosmetic — fails the required `contract` CI job. Rebasing
   the golden is an explicit `make schema` + human-reviewed commit.
2. **Breaking-delta acknowledgement (strong gate) + advisory classification.**
   A hard `go test` diffs the base-branch golden against the freshly regenerated
   schema for the breaking classes `tokens.txt` is blind to, and **fails unless
   the change is acknowledged** (§4.3). `schema-tools` separately annotates the
   PR with an additive/breaking *category* to inform the version bump (advisory).
3. **Convention conformance (strong gate).** A Go linter binds the frozen
   `schema.json` and asserts the project's naming/shape invariants (token
   grammar, non-empty descriptions, secret config **and secret type
   properties**, immutable/replace flags, no leaked excludes, enum
   canonicalization).
4. **Docs-truth (strong gate).** The API Standards doc's machine-checkable
   inventory is bound to the frozen schema — a **module bijection** plus an
   assertion of **every** immutable and secret claim, config-level *and*
   type-level. The **per-token census lives in the frozen `schema.json` golden
   itself** (every token, input, output and flag is a byte in the golden), not
   in a hand-authored list; if the standards doc wants to *display* an inventory
   table it is **generated from the golden**, never hand-maintained (§3.7).

### What the contract does NOT cover (explicit non-goals / defers)

- **Runtime/wire behavior.** This contract freezes the *schema* (the typed
  surface), not the provider's live CRUD behavior against a controller. That is
  the mock tier (`test-mock`) and e2e tier (`test-e2e`). A schema-stable release
  can still have a runtime regression; those tiers own that.
- **`openapi_generated.yml`.** The pulschema-updated intermediate OpenAPI doc is
  a **purely internal** artifact (it drives the runtime framework, not the SDK
  surface). It stays gitignored; its determinism is already covered in-process
  (`TestPipelineDeterministic`). It is a `//go:embed` input of the *provider*
  binary (`cmd/pulumi-resource-unifi/main.go:18`), so it must be **generated in
  CI before the cmd package compiles** (§3.5, prep bead) — but it is not frozen.
  Committing it would add a 100%-internal, large golden with no consumer of its
  stability. Deferred indefinitely.
- **SDK source goldens, and SDK naming across a codegen-toolchain bump.** The
  generated `sdk/` tree stays gitignored. Its *structure* is a function of
  `schema.json` **and of the pulumi codegen toolchain** (`pulumi package
  gen-sdk`, invoked via `pulumi/actions@v6`). Freezing the schema does **not**
  freeze the SDK surface transitively: a codegen-toolchain bump can rename
  Python classes/args or change generated signatures with a *green* schema
  golden. The contract deliberately does not freeze the toolchain, so
  **SDK-surface stability across a toolchain bump is an explicit uncovered
  risk** (§7). `sdk-smoke` asserts the SDK still imports with its expected class
  set, which catches gross breakage but not every rename; pinning `pulumi/actions`
  to an exact version (today it floats at `@v6`) is the cheap mitigation and is
  recommended in §7.
- **Per-input census in a hand-authored sidecar.** The machine inventory
  (`api-standards.yaml`) carries *rules, the module set, and the secret/immutable
  claim lists* — not a hand-maintained list of every resource/input. The full
  census is the frozen `schema.json` golden and its `tokens.txt` projection. See
  §4.3, §7 and §8/F4 for the rationale and the deliberate module-granularity
  limitation of the yaml bijection.
- **Cross-language SDK naming guarantees** beyond Python. Only Python is
  generated today; the contract is schema-level, so other languages inherit it
  when added, but no language-specific promise is made here.

---

## 2. Design principles applied

- **Make the failure class impossible, not the instance.** The failure class is
  "the public surface changed and nobody looked." A byte-exact committed golden
  makes *any* surface change a mandatory diff-review — the whole class, not a
  hand-picked list of "important" fields. The delta-diagnostic closes the
  residual hole in that class: the breaking changes that are *invisible to a
  green rebase* because they do not move the token set.
- **Guarantee ladder.** Where the pipeline already *fails the build loud*
  (`contract.Failf` on an unmapped entity, a dead exclusion, an ambiguous item
  path) the guarantee is already at the **boot/build-time** rung — the linter
  does **not** duplicate those. The new tests occupy the **test** rung for
  properties that have no cheaper representation (a JSON artifact has no Go type
  that makes "PascalCase token" unrepresentable). Version-bump correctness is
  left at the **convention** rung *explicitly* (§4.2), rather than dressed up as
  a guarantee — the previous draft's bead G claimed a guarantee it did not
  enforce.
- **Serialize once, freeze once.** Two code paths marshal the schema/metadata
  today (the entrypoint and the test). Bytes are frozen by this contract, so the
  two paths are unified into **one exported serializer** *before* the freeze
  (prep bead), eliminating the latent class "the golden test passes while the
  binary writes different bytes."
- **The schema is the source of truth.** Every new check reads *from* the
  committed `schema.json`. No check hand-encodes a duplicate of the surface.
- **Minimal slice first.** The tracer is schema-only. Metadata, linter,
  standards, delta-diagnostic, and the advisory classifier are separately
  dispatched units that each stand alone.
- **Decouple the contract gate from unrelated compile state.** The strong gates
  run in a dedicated `contract` job over `./pkg/gen/` (which embeds only
  `mappings.yaml`), so a break in the cmd package's embeds cannot make the
  contract gate lie in either direction.

---

## 3. Component decomposition & FROZEN INTERFACES

### 3.0 File layout (created / modified)

Committed golden artifacts (un-ignored):

| Path | Role | Writer (rebase) | Reader (gate) |
| --- | --- | --- | --- |
| `provider/cmd/pulumi-resource-unifi/schema.json` | public API golden | `make generate_schema` (entrypoint) | `TestSchemaMatchesGolden`, linter, bijection, delta-diagnostic |
| `provider/cmd/pulumi-resource-unifi/metadata.json` | CRUD/name-map golden (internal contract) | `make generate_schema` (entrypoint) | `TestMetadataMatchesGolden` |
| `provider/pkg/gen/testdata/tokens.txt` | coarse token census (already committed) | `UPDATE_GOLDEN=1 go test` | `TestTokenSetMatchesGolden` |
| `CHANGELOG.md` | release notes + **breaking-change acknowledgement** | human | `TestNoUnacknowledgedBreakingDelta` |

`docs/site/reference.md` is **NOT** in this table. It is the disposable,
gitignored, regenerated-every-run stopgap defined by the `docs/pages-site` work
and is **not part of the frozen contract** (see §8/B2). This contract has zero
dependency on `docs/pages-site` (`docs/gen_reference.py`, `mkdocs.yml`,
`docs/site/` do not exist on `main`).

New source files (all in `provider/pkg/gen/`, which compiles standalone):

| Path | Contents |
| --- | --- |
| `provider/pkg/gen/serialize.go` | `MarshalSchemaJSON`, `MarshalMetadataJSON` — the single serialization source of truth (prep bead, §3.2) |
| `provider/pkg/gen/contract_test.go` | `TestSchemaMatchesGolden`, `TestMetadataMatchesGolden` (byte goldens) |
| `provider/pkg/gen/contract_lint_test.go` | `TestContractLint` + named subtests (convention linter over the frozen golden) |
| `provider/pkg/gen/contract_delta_test.go` | `TestNoUnacknowledgedBreakingDelta` (hard breaking-class diff vs base golden) |
| `provider/pkg/gen/standards_test.go` | `TestStandardsInventoryMatchesGolden` (doc-truth bijection + claim assertions) |
| `docs/api-standards.md` | hand-written **API Standards & Stability** narrative (repo `docs/`, NOT `docs/site/`) |
| `docs/api-standards.yaml` | machine-checkable inventory sidecar (parsed by `standards_test.go`) |
| `CHANGELOG.md` | Keep-a-Changelog-style; `## [Unreleased]` with a `### Breaking` subsection is the ack surface |

Modified files:

| Path | Change |
| --- | --- |
| `.gitignore` | remove the `schema.json`, `metadata.json` ignore lines (lines 16–17). `openapi_generated.yml` (line 18) and `/sdk/` STAY ignored. |
| `Makefile` | add `schema`, `schema-check`, `schema-delta`, `lint-schema`, `schema-compare`; extend `.PHONY` |
| `provider/cmd/pulumi-gen-unifi/main.go` | `mustWritePulumiSchema` and metadata marshaling call the shared `gen.MarshalSchemaJSON` / `gen.MarshalMetadataJSON` (prep bead) |
| `provider/pkg/gen/gen_test.go` | `runPipeline` calls the same shared serializers (prep bead) |
| `.github/workflows/ci.yml` | (prep) run `make generate_schema` before `go vet`/`make test` so the cmd embeds exist; add required `contract` job; add advisory `schema-contract` job; `fetch-depth: 0` on jobs that read a base ref |

The standards doc is placed at `docs/api-standards.md` (not `docs/site/`) and is
a plain Markdown file that stands alone on `main`. Wiring it into an mkdocs nav
is **not part of this contract** and is deferred to whenever `docs/pages-site`
merges — the contract's enforcement (`api-standards.yaml` + the bijection test)
has no dependency on mkdocs.

### 3.1 Makefile targets (frozen names + behavior)

```
make schema
    Deliberate REBASE of every owned contract golden. Regenerates from the
    pinned spec and places:
      - schema.json + metadata.json   (via generate_schema — the entrypoint writer)
      - tokens.txt                    (UPDATE_GOLDEN=1 go test -run TestTokenSetMatchesGolden ./pkg/gen/)
    Mutates committed files. Prints "review the git diff before committing".
    This is the ONLY sanctioned way to move the public surface.
    (Does NOT touch reference.md — that artifact is not part of the contract.)

make schema-check                    [HARD GATE — read-only, no build, no Docker, no cmd embeds]
    Depends on the pinned spec ($(SPEC)). Runs the read-only golden + linter +
    bijection gate over the gen package ONLY (it embeds just mappings.yaml, so it
    compiles without schema.json/metadata.json/openapi_generated.yml present):
      cd provider && go test -count=1 -run \
        'TestSchemaMatchesGolden|TestMetadataMatchesGolden|TestTokenSetMatchesGolden|TestContractLint|TestStandardsInventoryMatchesGolden' ./pkg/gen/
    Regenerates the surface IN-PROCESS and asserts it equals the committed
    goldens; never mutates committed files. Fails on any drift.
    NOTE: it targets ./pkg/gen/, NOT ./... — the ./... build cannot compile in a
    fresh checkout because the cmd package embeds gitignored artifacts (§3.5).

make schema-delta                    [HARD GATE in CI — needs a base schema]
    cd provider && CONTRACT_BASE_SCHEMA=$(OLD_SCHEMA) \
      go test -count=1 -run TestNoUnacknowledgedBreakingDelta ./pkg/gen/
    Diffs the base-branch golden ($(OLD_SCHEMA), extracted by CI via `git show`)
    against the freshly regenerated schema for token-invariant breaking classes.
    Fails unless the change is acknowledged in CHANGELOG.md (§4.3). When
    CONTRACT_BASE_SCHEMA is unset (local run) or the base lacks the file (first
    introduction), the test SKIPS — it cannot compute a delta without a base, and
    is enforced by the required `contract` CI job that always supplies one.

make lint-schema                     [HARD GATE — subset of schema-check]
    cd provider && go test -count=1 -run 'TestContractLint' ./pkg/gen/
    The convention linter alone (§3.4). Kept as a named target for local use.

make schema-compare                  [ADVISORY — primarily a CI entrypoint]
    Semantic classification of the PR's golden against a base schema. Local form:
      schema-tools compare -p unifi \
        --old-path $(OLD_SCHEMA) --new-path $(SCHEMA_FILE) --summary
    OLD_SCHEMA defaults to a git-extracted base copy (see CI, §3.5). Never fails
    the build; emits the additive/breaking summary that informs the version bump.
```

Rationale for the `make schema` / `make schema-check` split: the committed
`schema.json` is *also* the `//go:embed` input for the provider binary
(`cmd/pulumi-resource-unifi/main.go:15`), so `make generate_schema` (which `make
build` already runs) must remain its canonical writer. `schema-check` is
therefore a **pure read-only** verifier — CI must fail on drift, never silently
rebase. The asymmetry (schema/metadata owned by the entrypoint; tokens.txt owned
by the test's `UPDATE_GOLDEN`) is forced by `go:embed` and is documented, not
hidden. Critically, `schema-check` runs against `./pkg/gen/` alone so it is
immune to the cmd-package embed compile problem (§3.5).

### 3.2 Shared serializer + byte-exact golden tests

**Prep bead — one serialization source of truth.** Today the schema is
serialized in **two** places that do **not** match:

- Entrypoint `mustWritePulumiSchema` (`cmd/pulumi-gen-unifi/main.go:112-121`):
  sets `pkgSpec.Version = ""`, then `json.MarshalIndent(pkgSpec, "", "    ")`.
- Test `runPipeline` (`pkg/gen/gen_test.go:67`): `json.MarshalIndent(pkg, "",
  "    ")` with **no `Version` reset**. Metadata: entrypoint uses
  `json.Marshal(metadata)` (`main.go:95`); `runPipeline` also `json.Marshal`
  (`gen_test.go:71`).

The bytes are identical **today** only because the test's `pkg.Version` happens
to be empty; a future `-version` change or a `Version` assignment in the test
would silently diverge — the golden test would pass while `make build` embeds
different bytes. This corrects the previous draft's **false** claim in §3.2 that
`runPipeline` marshals "MarshalIndent, Version=''": it does not reset `Version`.

Extract the entrypoint's serialization into two exported functions in
`provider/pkg/gen/serialize.go`, called by **both** the entrypoint and
`runPipeline`:

```go
// MarshalSchemaJSON is the single source of truth for how the Pulumi package
// schema is serialized to its committed/embedded byte form. It clears the
// version (injected at build time, never baked into the frozen golden) and
// indents deterministically. Both the gen entrypoint (mustWritePulumiSchema)
// and the golden test (runPipeline) MUST call this; there is no second path.
func MarshalSchemaJSON(pkgSpec pschema.PackageSpec) ([]byte, error) { ... }

// MarshalMetadataJSON is the single source of truth for metadata.json bytes.
// It indents (json.MarshalIndent) so the committed golden produces reviewable
// diffs — a change from today's compact json.Marshal, applied in this prep bead
// BEFORE the golden is frozen so no later reflow churn occurs.
func MarshalMetadataJSON(metadata openapigen.ProviderMetadata) ([]byte, error) { ... }
```

Frozen golden-test signatures (bodies are `...`; this is a design doc):

```go
// goldenSchemaPath / goldenMetadataPath are the committed public-surface goldens,
// located by walking up from the test working dir to the repo root (same idiom as
// findSpec in drift_test.go). They ARE the contract; the pipeline must reproduce
// them byte-for-byte.
const goldenSchemaPath = "cmd/pulumi-resource-unifi/schema.json"
const goldenMetadataPath = "cmd/pulumi-resource-unifi/metadata.json"

// TestSchemaMatchesGolden regenerates schema.json in-process from the pinned spec
// (runPipeline -> gen.MarshalSchemaJSON, the EXACT bytes the entrypoint writes,
// via the shared serializer) and asserts equality with the committed golden. A
// spec bump not followed by `make schema` + commit fails here. READ-ONLY: no
// UPDATE_GOLDEN branch — the golden is owned by `make generate_schema`. On
// mismatch it emits a bounded, readable diff summary.
func TestSchemaMatchesGolden(t *testing.T) { ... }

// TestMetadataMatchesGolden does the same for metadata.json via the shared
// gen.MarshalMetadataJSON. Byte-exact; owned by the entrypoint.
func TestMetadataMatchesGolden(t *testing.T) { ... }
```

Behavior contract:
- Both tests obtain bytes through the shared serializers, so "test bytes" and
  "entrypoint bytes" are the same code by construction (not by convention).
- **No mutation.** These tests never write the golden; rebasing is `make schema`.
- Failure mode: a bounded added/removed line diff (mirror the `tokens.txt`
  presentation in `drift_test.go`), not a 223 KB blob dump. For `schema.json` the
  companion `TestTokenSetMatchesGolden` diff gives the human-readable "which
  resources changed" summary (see §4.3).

### 3.3 Semantic classifier (schema-tools) — invocation contract (advisory)

- **Local mode only.** Never the remote `-o <branch>` form — its default
  `--repository` hardcodes the `pulumi` GitHub org and cannot find this repo's
  golden.
- **Pinned version.** `go install github.com/pulumi/schema-tools@vX.Y.Z` with a
  **pinned released tag** (NOT `@latest`). The exact tag is chosen at
  implementation time and recorded in `ci.yml`.
- **Base extraction (git, local paths) — REQUIRES FULL HISTORY:**
  ```
  # The job MUST use actions/checkout@v4 with fetch-depth: 0 (or an explicit
  # `git fetch --no-tags origin ${BASE_REF}`), matching docs.yml's precedent
  # (docs/pages-site .github/workflows/docs.yml:32). Under the default
  # fetch-depth: 1 the base ref is absent and `git show` fails.
  git show "origin/${BASE_REF}:provider/cmd/pulumi-resource-unifi/schema.json" > "$OLD_SCHEMA"
  # if that path does not exist on base (first introduction) -> skip, exit 0
  ```
- **Compare:**
  ```
  schema-tools compare -p unifi --old-path "$OLD_SCHEMA" --new-path "$SCHEMA_FILE" --summary
  schema-tools compare -p unifi --old-path "$OLD_SCHEMA" --new-path "$SCHEMA_FILE" --json
  ```
- **metadata normalization.** `schema-tools` reads `metadata.json` adjacent to
  each `schema.json` for name normalization when present. The advisory job places
  both base and PR `metadata.json` beside their respective schema copies. If a
  specific flag is required by the pinned version and is missing, that is an
  **errata** finding, not a local hack.
- **Never fails the build.** Output is posted as a PR annotation / job summary.
  It is the *categorizer*; the hard forcing function is the delta-diagnostic
  (§4.3), not this job.

### 3.4 Convention linter (`contract_lint_test.go`) — the rule set

The linter loads the **committed** `schema.json` into a `pschema.PackageSpec`
(via the already-vendored `pschema`) and asserts the named checks below. It
lints the *artifact we ship*, so it also catches a hand-edited golden that a
pipeline-only test would miss.

```go
// TestContractLint loads the committed golden schema and runs each named
// convention check as a subtest. Reads cmd/pulumi-resource-unifi/schema.json;
// no build, no spec fetch required.
func TestContractLint(t *testing.T) { ... }
```

Named checks (each a `t.Run(...)` subtest):

| Check | Asserts | Ladder note |
| --- | --- | --- |
| `TokenGrammar` | every resource/function token matches `^unifi:[a-z-]+/v\d+:[A-Z][A-Za-z0-9]*$`; functions' short name starts `get`/`list` | test (no cheaper rep for JSON) |
| `ModuleVersionAllowed` | every token's `<module>/<version>` ∈ the module set declared in `docs/api-standards.yaml` | test; binds doc→schema |
| `NonEmptyDescriptions` | every resource, function, and each of their input/output properties has a non-empty `description` | test; pipeline synthesizes, linter freezes the invariant |
| `SecretConfig` | every config name in `api-standards.yaml.guarantees.secretConfig` is `secret: true` in the golden config; and no config so-named is un-flagged | test; security-relevant |
| `SecretTypeProperties` | every `<typeToken>.<prop>` in `api-standards.yaml.guarantees.secretProperties` is `secret: true` on that property of that `#/types` entry in the golden; and cross-checked against `secretTypeProperties` (`pass_secret_fields.go:14`) so the shipped flag, the pipeline source, and the doc agree | test; security-relevant; **covers the type-level secret `unifi:sites/v1:HotspotVoucherDetails.code` that config-only checks missed** |
| `ImmutableInputs` | every input named in `guarantees.immutableInputs` is listed in `replaceOnChanges` on **every** resource that carries it | test; mirrors pass_replace_on_changes on the frozen surface |
| `NoExcludedResourceLeaked` | no token in `mappings.yaml.excludeResources` appears in the golden's `resources` | test; complements the pipeline pass on the artifact |
| `EnumCanonical` | within a module no two enum `#/types` share an identical value-set (dedup held); enum type short names are PascalCase | test; freezes pass_enum's output |

Deliberately **NOT** duplicated (already at a higher rung — build-time
`contract.Failf` in the pipeline): unmapped-entity failure, dead-exclusion
(`TestExcludedPathsResolve`), ambiguous item path, spec/pin version mismatch.
Deliberately **not** re-added: `TestNoDuplicateShortTokenNames` already exists as
an in-process pipeline guard; the golden linter does not re-implement it.

### 3.5 CI jobs (names + hard/advisory) — corrected wiring

**Two problems in the existing pipeline that this design must handle:**

1. **The `test` job is already RED on `main`.** It runs `go vet ./...` then
   `make test` (`cd provider && go test ./...`). Both compile
   `cmd/pulumi-resource-unifi`, which `//go:embed`s `schema.json`,
   `metadata.json`, **and** `openapi_generated.yml` (`main.go:15,18,21`) — all
   three gitignored. In a fresh checkout none exist, so `./...` fails to compile
   before any test runs. **Prep fix:** run `make generate_schema` (which builds
   the gen binary and writes all three embeds) **before** `go vet`/`make test`.
   This design fixes the pre-existing breakage as a side effect; it is carved out
   as the `prep:ci-embeds` bead so the fix lands even if the rest slips.
2. **The strong contract gates must NOT ride `./...`.** Because of (1), routing
   the golden/linter/bijection through `make test`/`./...` (as the previous draft
   did in §3.5) would couple the contract gate to the cmd-embed compile. Instead
   they run over `./pkg/gen/`, which embeds only `mappings.yaml` and compiles
   standalone — via `make schema-check` in a dedicated required job.

Existing required checks stay required: **`test`**, **`determinism`**,
**`sdk-smoke`**.

| Job / step | Type | Behavior |
| --- | --- | --- |
| `test` (existing) — add a `make generate_schema` step **before** `gofmt`/`go vet`/`make test` | **HARD** (required) | produces the three cmd embeds so `./...` compiles; fixes the pre-existing RED (`prep:ci-embeds`) |
| **`contract` (NEW job)** | **HARD** (required) | `actions/checkout@v4` with `fetch-depth: 0`; run `make schema-check` (byte-golden + linter + bijection, `./pkg/gen/` only — no Docker/Python/Pulumi, no cmd embeds); then extract the base golden via `git show origin/${BASE}:.../schema.json` and run `make schema-delta` (hard breaking-ack). Skips `schema-delta` cleanly when the base lacks the file. |
| **`schema-contract` (NEW job)** | **ADVISORY** (not required) | PR-only; `fetch-depth: 0`; installs pinned `schema-tools`, extracts base schema+metadata via `git show`, runs `schema-compare`, posts summary; `continue-on-error` / never sets a failing status |

The `contract` job needs neither Docker, Python, nor Pulumi — it is `go test`
over `./pkg/gen/` plus a `git show`. It is the fast-lane home of the whole strong
contract, deliberately independent of the cmd package so it cannot be skipped or
falsely satisfied.

### 3.6 Standards inventory sidecar (`docs/api-standards.yaml`) — frozen shape

```yaml
# Machine-checked contract inventory. Parsed by standards_test.go. This carries
# RULES + the module set + the secret/immutable CLAIM lists, NOT a per-token
# census (the census is the frozen schema.json itself, projected to
# testdata/tokens.txt). See docs/design/api-contract.md §4.3 / §7.
version: 1

# The closed set of <module>/<version> segments the public surface is allowed to
# use. A new module appearing in the golden without being listed here fails
# TestContractLint/ModuleVersionAllowed AND the bijection below — a new module is
# a deliberate, reviewed event.
modules:
  - sites/v1
  - dpi/v1
  - countries/v1
  - info/v1
  - pending-devices/v1

tokenGrammar: "unifi:<module>/<version>:<PascalCase>"

guarantees:
  # Every resource that HAS an input of this name must mark it replaceOnChanges.
  immutableInputs:
    - siteId
    - vlanId
  # Every named config must be secret in the golden config. Security-relevant.
  secretConfig:
    - apiKey
  # Every listed type-property must be secret on that #/types entry in the golden.
  # Keyed by type token; cross-checked against pass_secret_fields.secretTypeProperties.
  # Security-relevant; realizes §1 promise 4's "every secret claim" at TYPE level.
  secretProperties:
    "unifi:sites/v1:HotspotVoucherDetails":
      - code
```

Bijection/claim test:

```go
// TestStandardsInventoryMatchesGolden binds docs/api-standards.yaml to the frozen
// schema.json (doc-truth):
//   (1) MODULE BIJECTION — the set of modules in the golden's tokens equals
//       exactly the `modules:` list (neither an undeclared module nor a dead
//       declaration is allowed). This is MODULE granularity, deliberately (§4.3);
//       the per-token census truth rests on the byte golden, not this yaml.
//   (2) CLAIM ASSERTION — every guarantees.immutableInputs / secretConfig /
//       secretProperties entry is realized in the golden, delegating the
//       per-property predicate to the SAME functions the linter uses (§3.4), so
//       there is one implementation. secretProperties is additionally
//       cross-checked against pass_secret_fields.secretTypeProperties.
// It does NOT re-list tokens; the token census is the golden + tokens.txt.
func TestStandardsInventoryMatchesGolden(t *testing.T) { ... }
```

### 3.7 Standards prose doc (`docs/api-standards.md`) — structure

Hand-written, on `main`, a plain Markdown file (mkdocs nav wiring deferred, §3.0).
Sections (fixed outline):

1. **What is guaranteed** — the four promises from §1, in user terms, with the
   enforced-vs-convention split stated honestly.
2. **Naming conventions** — token grammar `unifi:<module>/<version>:<PascalCase>`;
   functions `get<Entity>` / `list<Entities>`. Points at `docs/api-standards.yaml`
   as the machine-checked source, not a duplicated list.
3. **Resource shape rules** — inputs vs outputs; `replaceOnChanges` (immutable
   identity: `siteId`, `vlanId`); `secret` at config level (`apiKey`) and type
   level (`HotspotVoucherDetails.code`); discriminated variants as distinct tokens.
4. **Stability & deprecation policy** — the breaking-change taxonomy (§4.1), the
   delta-diagnostic acknowledgement rule (§4.3), and the version policy (§4.2).
5. **How the contract is enforced** — a short map to the gates (§3), so a
   contributor knows *why* a red check fired and how to rebase (`make schema`).
6. **Inventory table (OPTIONAL, generated).** If a human-readable per-token
   inventory table is wanted in this doc, it is **generated from the frozen
   `schema.json`** by a small script and diff-checked — never hand-authored and
   never a frozen artifact of its own. This keeps a single source of truth (the
   golden) and avoids reintroducing a `reference.md`-style dependency. Deferred
   as a nicety; not required for the contract.

The prose deliberately states *rules and policy*; the *specifics* that could rot
(module set, immutable/secret claims) live in the yaml and are test-bound; the
*full census* lives in the golden.

---

## 4. The contract semantics

### 4.1 What "breaking" means for THIS provider

Classification of a `schema.json` delta (the taxonomy the human applies, informed
by `schema-tools`; the starred classes are the ones the **hard delta-diagnostic**
enforces because `tokens.txt` is blind to them):

**BREAKING (requires a major bump post-1.0):**
- Resource or function **token removal or rename**. *(token-set-visible; caught
  by the byte golden + `tokens.txt` already.)*
- Token **kind flip** (resource ↔ function). *(token-set-visible.)*
- ★ Adding a **new required input** to an existing resource/function (growth of
  `requiredInputs`), or making an existing optional input required.
- ★ **Removing an output property** from an existing resource/function.
- ★ **Type narrowing** of an existing property (`string` → enum; enum **value
  removal**; array → scalar).
- ★ **`replaceOnChanges` add** on an existing property (silently converts an
  in-place update into destroy/recreate — behaviorally breaking).
- ★ **`secret` removal** on an existing property (leaks a previously-redacted
  value — breaking **and** a security regression).

**ADDITIVE (minor):**
- New resource/function token; new **optional** input; new output property; new
  **enum value**; adding a `secret` flag (conservative; treat as minor).

**COSMETIC (patch):**
- Description text changes; ordering-only churn (the pipeline is sorted, so this
  should not occur — if it does, it is a determinism bug, not a patch).

The **byte-golden catches everything** (including cosmetic) *relative to the
committed golden*; the **delta-diagnostic catches the ★ classes relative to the
base branch** (which a green `make schema` rebase would otherwise hide);
**`schema-tools`** classifies the non-cosmetic deltas into additive/breaking to
advise the bump.

### 4.2 Version policy (normative text for the standards doc)

- **Pre-1.0 (current, `0.x`).** The surface is explicitly *unstable by SemVer
  convention*, but the contract still applies mechanically: breaking deltas bump
  the **minor** (`0.Y.0`), additive **and** cosmetic bump the **patch**
  (`0.0.Z`). **Explicit, intended consequence:** pre-1.0, *a brand-new resource
  ships as a patch*, because additive and cosmetic share the patch slot. This is
  accepted — pre-1.0 additive-vs-cosmetic distinction carries no SemVer promise;
  only the breaking/non-breaking boundary is load-bearing, and it maps to the
  minor. Post-1.0 this collapses no longer (below).
- **Post-1.0.** Strict SemVer: breaking → major, additive → minor, cosmetic →
  patch, mapped from §4.1.
- **Deprecation.** A token/property slated for removal is first marked with
  `deprecationMessage` for **at least one minor release** before removal.
  Enforcement (a "no removal of a non-deprecated token" linter) is **deferred**
  (needs a prior-release schema reference).
- **Version-bump correctness is CONVENTION-tier, stated as such.** No gate
  today verifies that the number a maintainer picks matches the classification.
  What **is** enforced is the *acknowledgement* of a breaking change (§4.3); the
  *mapping to a specific version* is documented policy a human follows. A
  release-time gate (fail a release whose bump is smaller than the accumulated
  `### Breaking` changelog entries demand) is a worthwhile **deferred**
  enhancement — recorded here so the gap is visible, not dressed up as a
  guarantee. (This corrects the prior draft's bead G, which claimed a guarantee
  it did not enforce.)
- **Spec bumps are not automatically releases.** Bumping `openapi/pin.env` that
  changes the surface produces a golden diff → a reviewed `make schema` commit →
  the classifier + delta-diagnostic decide whether an ack is required. An
  upstream bump with **no** surface delta is a patch (or no release).

### 4.3 The delta-diagnostic + acknowledgement (the breaking-change forcing function)

The hole the previous design left open: after an upstream bump, the maintainer
runs `make schema`, the byte-golden goes **green** (the committed golden now
matches the new surface), and `tokens.txt` is unchanged if the breaking change
did not add/remove a token — so a *real* breaking change (added required input,
removed output, type narrowing, `secret`/`replaceOnChanges` flip) merges green.
`schema-tools` would flag it, but it is advisory and non-blocking.

**Forcing function (hard `go test`):**

```go
// TestNoUnacknowledgedBreakingDelta compares the base-branch golden (path in
// CONTRACT_BASE_SCHEMA, extracted by CI via `git show origin/${BASE}:...`) to the
// freshly regenerated schema and detects the token-INVARIANT breaking classes
// marked ★ in §4.1: added requiredInputs, removed output properties, type
// narrowing (incl. enum-value removal), added replaceOnChanges, removed secret.
// If ANY such class is present it REQUIRES an acknowledgement and fails otherwise.
//
// SKIPS when CONTRACT_BASE_SCHEMA is unset (local dev) or the base lacks the
// file (first introduction). Enforced by the required `contract` CI job, which
// always supplies a base (fetch-depth: 0).
func TestNoUnacknowledgedBreakingDelta(t *testing.T) { ... }
```

**Acknowledgement mechanism — chosen: an in-tree `CHANGELOG.md` entry.** When a
★ class is present, the test requires the `## [Unreleased]` section of
`CHANGELOG.md` to contain a non-empty `### Breaking` subsection.

Why this mechanism, justified against the alternatives the brief offered:
- **PR label** — rejected. Not in the git tree; a `go test` cannot see it without
  the GitHub event JSON / API, making the check CI-shaped and untestable in
  isolation (the exact anti-pattern this contract avoids).
- **Dedicated ack file** — rejected. Testable but meaningless; a `touch` games it
  and it serves no other purpose.
- **`CHANGELOG.md` `### Breaking` entry** — chosen. In-tree (diffable, reviewable
  in the same PR), testable by a plain file read in `./pkg/gen/`, and it doubles
  as release notes that feed §4.2's version decision.

**Stated limitation (module/presence granularity).** The ack is *presence*, not
*precision*: any non-empty `### Breaking` entry satisfies any breaking delta in
the same PR — the test does not verify the entry *describes this specific*
change. Tightening to per-change matching is deferred; presence already converts
"a breaking change slips in unseen" (the failure class) into "a breaking change
is impossible without the author writing a breaking-changelog line," which is the
class-level win. Costs stated, not sold.

`schema-tools` stays the advisory *categorizer* (it may spot classes the
diagnostic does not, and it names additive-vs-breaking for the bump); the
diagnostic is the *gate*.

### 4.4 Relationship to `tokens.txt` — keep, subordinate, do not duplicate

The full `schema.json` byte-golden **strictly subsumes** the token set for
*failure detection against the committed golden*. `tokens.txt` is **kept** for a
distinct reason: **diagnostics**. A token add/remove/rename surfaces as a clean
one-line diff in `tokens.txt` and in `TestTokenSetMatchesGolden`'s added/removed
summary, whereas in the 223 KB `schema.json` it is buried. `tokens.txt` is the
**coarse, human-readable canary**, explicitly subordinate to the schema golden —
and explicitly **blind to the ★ breaking classes** the delta-diagnostic covers
(§4.3). No enforcement logic is duplicated: both are golden comparisons;
`tokens.txt` is a *projection* of the schema.

---

## 5. Dependency DAG (ordering artifact)

Serial dispatch, one implementer at a time. **The tracer is a SPINE-tracer** — it
exercises the freeze spine (regenerate → serialize → byte-compare to a committed
golden, gated by a required job), not literally every layer of a fan-out DAG. It
is **implemented and reviewed before any fan-out.** Blocking edges mirror data
dependencies (a unit's gate needs the committed artifact/serializer its
predecessor produced).

```
   ┌──────────────────────┐     ┌──────────────────────────────┐
   │ P1 prep: serializer  │     │ P2 prep: ci-embeds           │
   │ (unify marshaling)   │     │ (generate_schema before vet; │
   │                      │     │  fixes pre-existing RED)     │
   └──────────┬───────────┘     └──────────────┬───────────────┘
              └───────────────┬────────────────┘
                              v
                 ┌─────────────────────────────┐
                 │ T  SPINE-TRACER (schema)     │  <-- tracer-review gate
                 │ DONE anchored to schema-check│      (`make schema-check`,
                 └──────────────┬───────────────┘       ./pkg/gen/, no cmd embeds)
     ┌───────────┬──────────────┼──────────────┬──────────────┐
     v           v              v              v              v
 ┌────────┐ ┌───────────┐  ┌──────────┐  ┌────────────┐  ┌────────────┐
 │ A meta │ │ B linter  │  │ C compare│  │ K delta-   │  │ E standards│
 │ golden │ │ (contract)│  │ (advisory)  │ diagnostic │  │ doc + yaml │
 └───┬────┘ └─────┬─────┘  └────┬─────┘  │ (HARD ack) │  └─────┬──────┘
     │            │             │        └─────┬──────┘        │
     └─────┬──────┘             │              │               v
           │                    │              │         ┌────────────┐
           │                    │              │         │ F bijection│
           │                    │              │         │ (E yaml +  │
           │                    │              │         │  A/B preds)│
           │                    │              │         └─────┬──────┘
           └──────────┬─────────┴──────────────┴───────────────┘
                      v
               ┌────────────┐
               │ G policy   │  (version/deprecation prose; wires the changelog
               │            │   ack + advisory summary; needs C + K + E)
               └─────┬──────┘
                     v
               ┌────────────┐
               │ H coherence│  (README/cross-links, .PHONY, help, lockstep check)
               └────────────┘
```

**Changes from Revision 1 (called out explicitly):**
- **NEW `P1 prep:serializer`** — unify the two marshaling paths into
  `gen.MarshalSchemaJSON` / `gen.MarshalMetadataJSON`; **precedes** the tracer so
  bytes are frozen once (§3.2, addresses B4).
- **NEW `P2 prep:ci-embeds`** — run `make generate_schema` before `go vet`/`make
  test` so `./...` compiles; fixes the pre-existing RED `test` job (§3.5, B1).
- **NEW `K` delta-diagnostic** — the hard breaking-change forcing function
  (§4.3, B3), a required CI gate.
- **DROPPED bead `D` (reference.md diff-gate)** — reference.md is not part of the
  contract; removes the `docs/pages-site` cross-PR dependency (B2).
- **Tracer DONE re-anchored** from "the `test` job turns red" to "`make
  schema-check` turns red" (runs `./pkg/gen/`, immune to the cmd-embed compile).
- **Bead A** no longer owns the marshaling-consistency concern (moved into P1);
  it is now just: un-ignore metadata, commit golden, add the read-only test.
- **Bead E** places the prose at `docs/api-standards.md` (not `docs/site/`) and
  drops the `mkdocs build --strict` DONE criterion (no mkdocs on `main`).
- **Bead G** narrowed to enforce the *changelog ack* (delegated to K) + document
  convention-tier version policy; it no longer claims an unenforced guarantee.

Unit specs (each passes "fresh session, this text alone"):

- **P1 — prep: unify the serializer.**
  Add `provider/pkg/gen/serialize.go` with exported `MarshalSchemaJSON(pschema.PackageSpec)`
  (clears `Version`, `MarshalIndent`) and `MarshalMetadataJSON(openapigen.ProviderMetadata)`
  (`MarshalIndent`). Change `mustWritePulumiSchema` and the metadata marshaling in
  `cmd/pulumi-gen-unifi/main.go` to call them; change `runPipeline`
  (`pkg/gen/gen_test.go`) to call them. No golden exists yet, so no diff to
  review. DONE = entrypoint and `runPipeline` share one serializer;
  `TestPipelineDeterministic` still passes; metadata now indents.

- **P2 — prep: make CI compile.**
  In `.github/workflows/ci.yml` `test` job, add a `make generate_schema` step
  (produces the three cmd embeds) before `gofmt`/`go vet ./...`/`make test`. DONE
  = `go vet ./...` and `make test` compile the cmd package in a fresh checkout;
  the pre-existing RED is green. (Independent of the contract goldens; may land
  first.)

- **T — spine-tracer: freeze the schema spine.**
  Remove the `schema.json` ignore line from `.gitignore`. Run `make
  generate_schema` and commit the resulting
  `provider/cmd/pulumi-resource-unifi/schema.json` as the initial golden. Add
  `provider/pkg/gen/contract_test.go` with a **read-only** `TestSchemaMatchesGolden`
  that regenerates in-process via `runPipeline` → `gen.MarshalSchemaJSON` (P1's
  shared serializer) and asserts byte-equality, emitting a bounded diff on
  mismatch. Add Makefile targets `make schema` (rebase: `generate_schema` +
  tokens `UPDATE_GOLDEN`) and `make schema-check` (read-only `go test` over
  `./pkg/gen/`, depends on `$(SPEC)`). Add the required `contract` CI job running
  `make schema-check`.
  DONE = a spec-pin change without a golden rebase turns **`make schema-check`**
  (the `contract` job) red — verified by `go test ./pkg/gen/`, independent of the
  cmd embeds. **Gate: tracer-review before any fan-out.**

- **A — metadata golden.**
  Remove the `metadata.json` ignore line. Commit the (now indented, via P1)
  `metadata.json` golden. Add `TestMetadataMatchesGolden` (read-only, same shape
  as `TestSchemaMatchesGolden`, via `gen.MarshalMetadataJSON`). Add it to `make
  schema-check`. DONE = metadata drift fails the `contract` job.

- **B — convention linter.**
  Add `provider/pkg/gen/contract_lint_test.go` with `TestContractLint` loading the
  committed `schema.json` and the eight named subtests in §3.4 (incl.
  `SecretTypeProperties`). Add `make lint-schema`; include in `make schema-check`.
  DONE = each named check fails on a golden that violates it; a mutation probe
  (flip a `secret`, drop a `replaceOnChanges`, un-secret `HotspotVoucherDetails.code`)
  turns it red.

- **C — advisory semantic classifier.**
  Add `make schema-compare` and the PR-only `schema-contract` CI job
  (**`fetch-depth: 0`**): install pinned `schema-tools`, `git show` the base
  `schema.json`+`metadata.json`, run `compare --summary`/`--json`, post the
  summary, never fail the build; skip cleanly when the base lacks the file. DONE =
  a breaking PR gets an annotated "breaking" summary without blocking merge.

- **K — hard delta-diagnostic (breaking-change forcing function).**
  Add `provider/pkg/gen/contract_delta_test.go` with
  `TestNoUnacknowledgedBreakingDelta` (§4.3): read `CONTRACT_BASE_SCHEMA`, diff
  the ★ breaking classes against the regenerated schema, fail unless
  `CHANGELOG.md`'s `## [Unreleased]`/`### Breaking` is non-empty; skip without a
  base. Add `make schema-delta`. Wire it into the required `contract` job
  (**`fetch-depth: 0`**, `git show origin/${BASE}:.../schema.json` → `OLD_SCHEMA`,
  skip on first-introduction). Add `CHANGELOG.md` with an `## [Unreleased]`
  scaffold. DONE = a PR that adds a required input / removes an output / narrows a
  type / flips a `secret` or `replaceOnChanges`, with an empty `### Breaking`,
  turns the `contract` job red; adding the changelog entry turns it green; a
  mutation probe confirms each ★ class trips it.

- **E — standards doc + inventory.**
  Add `docs/api-standards.yaml` (shape in §3.6, incl. `secretProperties`) and
  `docs/api-standards.md` (outline in §3.7). DONE = the yaml parses; the doc is a
  self-contained Markdown file on `main` (no mkdocs dependency).

- **F — doc-truth bijection.**
  Add `provider/pkg/gen/standards_test.go` with `TestStandardsInventoryMatchesGolden`
  (§3.6): module bijection golden↔yaml, and claim assertions (immutable, secret
  config, **secret type properties** cross-checked vs `secretTypeProperties`)
  reusing the linter's predicates. Add to `make schema-check`. DONE = adding a
  module to the golden without listing it (or vice-versa) fails; a false
  immutable/secret claim fails; a type-secret claim not realized in the golden
  fails.

- **G — version/deprecation policy + ack wiring.**
  Write §4.2's normative policy into `api-standards.md` (explicitly labeling
  version-bump correctness convention-tier and new-resource-as-patch pre-1.0).
  Wire the `schema-contract` job summary to reference the policy and the `###
  Breaking` changelog requirement. DONE = the policy is on `main`; the advisory
  output points to it; the *enforced* part (the ack) is bead K, not prose.

- **H — coherence.**
  Cross-link README ↔ api-standards ↔ this design doc; ensure `.PHONY` lists the
  new targets; add a `make help`/target-comment line for each; confirm the single
  serializer (P1) is the only marshaling call-site for schema/metadata (lockstep
  check); confirm no `docs/pages-site` dependency crept in. DONE = the coherence
  review passes with no open errata.

---

## 6. Test architecture (isolation per unit)

| Unit | Isolation | Conformance boundary |
| --- | --- | --- |
| Serializer (P1) | pure function; `TestPipelineDeterministic` + golden tests exercise it | one serialization path ↔ both callers |
| Byte goldens (T, A) | in-process `runPipeline` (via shared serializer) vs committed file; no Docker/Python/Pulumi; `./pkg/gen/` only | pipeline-output ↔ committed-artifact boundary |
| Linter (B) | loads committed `schema.json` only; no spec fetch | frozen-artifact ↔ conventions boundary |
| Delta-diagnostic (K) | committed/regenerated schema vs base golden (env-supplied) + `CHANGELOG.md`; skips without a base; `./pkg/gen/` | base-branch ↔ PR surface, gated by ack |
| Advisory (C) | CI-only; git-extracted base vs PR file; `fetch-depth: 0` | base-branch ↔ PR surface (informational) |
| Bijection (F) | committed `schema.json` + committed yaml; reuses B predicates | doc-claims ↔ frozen-surface boundary |

Every strong gate is a plain `go test` over `./pkg/gen/` reading committed files
(K additionally reads one env-supplied base file in CI) — no harness, no network,
no Docker, and crucially **no dependency on the cmd package's embeds**. This is
deliberate: the contract must be verifiable in the fast lane so it cannot be
"skipped for speed," and it must not be coupled to an unrelated compile failure.

Mutation-probe requirement (verifier): for B, F, and K, break one flag/shape in
the golden (flip a `secret` incl. the type-level `HotspotVoucherDetails.code`,
drop a `replaceOnChanges`, add a required input against a synthetic base) and
confirm the corresponding named check/diagnostic fails. A check that passes a
broken golden is a failed check.

---

## 7. Stated costs, deferrals, and residual risks

**Top costs / risks after this revision:**

1. **SDK surface is NOT frozen across a codegen-toolchain bump.** The contract
   freezes `schema.json`, not the pulumi codegen toolchain (`pulumi package
   gen-sdk` via `pulumi/actions@v6`, which floats). A toolchain bump can rename
   Python classes/args or change generated signatures with a *green* schema
   golden — a public SDK break the contract does not catch. `sdk-smoke` catches
   gross breakage (import + expected class set) but not every rename. **Cheap
   mitigation, recommended:** pin `pulumi/actions` (and the Pulumi CLI) to an
   exact version so the toolchain is deterministic, and treat a toolchain bump as
   a reviewed event like a spec bump. Until pinned, this is an accepted uncovered
   risk. (Corrects the prior draft's false "freezing the schema freezes the SDKs
   transitively" claim — F2.)

2. **The breaking-ack is presence-granular, not per-change.** The
   delta-diagnostic requires *some* `### Breaking` changelog entry when a ★ class
   is present; it does not verify the entry describes *this* change, and multiple
   breaking changes in one PR need only one entry. This converts the failure class
   ("a breaking change merges unseen") into an author-visible forcing function,
   but a lazy author can write a vacuous entry. Per-change matching and a
   release-time version-bump gate are deferred (§4.2, §4.3).

3. **Cosmetic churn trips the hard byte-gate.** Byte-exact freeze means an
   upstream description reword fails `make schema-check` and forces a `make
   schema` rebase + review. Intended cost (every surface change is seen), but it
   adds friction on spec bumps. Mitigation: `tokens.txt` diff + advisory
   `schema-tools` summary tell the reviewer at a glance the change is cosmetic, so
   the rebase is a fast, confident click.

**Other stated costs:**
- **`schema-tools` is an external, evolving classifier.** Pinned to a released
  tag; kept **advisory** (the byte-golden and the delta-diagnostic are the real
  gates), so a classifier disagreement never blocks or mis-blocks a merge.
- **Module-granularity yaml bijection.** `api-standards.yaml`↔golden is a
  *module*-level bijection plus explicit secret/immutable claim lists; it does
  **not** encode a per-token census (that truth is the golden itself). Deliberate:
  a hand-maintained token census in yaml would be a second list that rots (F4a).

**Deferrals (explicitly out of the initial slice):**
- Deprecation-window enforcement (needs a prior-release schema reference).
- A release-time gate binding the version bump to the classification (§4.2).
- Per-change (not merely presence) breaking acknowledgement (§4.3).
- Pinning the codegen toolchain / freezing SDK naming (cost 1).
- `openapi_generated.yml` and SDK-source goldens (internal / not frozen).
- Generated inventory table in `api-standards.md` (§3.7 — nicety).
- Multi-language SDK naming guarantees beyond schema-level.

---

## 8. Review response (finding → resolution)

Two adversarial reviews returned FAIL. Each finding below maps to the section
that resolves it or to a stated, justified deferral. File:line claims were
re-verified against `main` (the prior draft's assertions were not trusted).

### B1 — CI gates infeasible as routed — **CLOSED.**
Verified: `cmd/pulumi-resource-unifi/main.go` embeds `schema.json` (15),
`openapi_generated.yml` (18), `metadata.json` (21); all three gitignored
(`.gitignore:16-18`). The `test` job runs `go vet ./...` then `make test`
(`go test ./...`), both of which compile the cmd package and therefore **fail in
a fresh checkout today** — the `test` job is already RED on `main`. Resolution:
(a) strong gates route through **`make schema-check` = `go test ./pkg/gen/`**
(the `gen` package embeds only `mappings.yaml`, verified via `mappings.go:26`,
and compiles standalone), run in a dedicated required **`contract` job** decoupled
from the cmd embeds (§3.1, §3.5). (b) The existing `test`/`build` path is fixed by
a **`prep:ci-embeds`** bead that runs `make generate_schema` before `go
vet`/`make test`; noted as fixing the pre-existing breakage as a side effect
(§3.5, bead P2). (c) The tracer's DONE is **re-anchored to `make schema-check`**
(§5, bead T). The prior §3.5 ("gates ride `make test`/`./...`") is replaced.

### B2 — hidden `docs/pages-site` dependency / reconcile `reference.md` — **CLOSED.**
Verified: `docs/gen_reference.py`, `mkdocs.yml`, `docs/site/` are **absent on
`main`** (present only on `docs/pages-site`); `reference.md` is not committed
anywhere. Resolution (STEER adopted): **`reference.md` stays the disposable,
gitignored, regenerated stopgap and is dropped from the contract.** Bead D
(reference.md diff-gate), `make docs-check`, `make docs-gen`, the `docs/site/`
paths, and the `mkdocs.yml` modification are **all removed** (§3.0, §3.1, §5).
Docs-truth now rests on the frozen `schema.json` golden (the per-token census is
the golden) **plus** the `api-standards.yaml` module/claim bijection; an optional
inventory table is **generated from the golden**, never frozen (§3.7). The
standards prose moves to `docs/api-standards.md` (a plain file on `main`). **The
revised contract layer builds on `main` with zero dependency on `docs/pages-site`**
— confirmed; no residual cross-PR edge remains.

### B3 — breaking changes need a forcing function — **CLOSED.**
Verified: only `schema-tools` classifies breaking-vs-additive and it is
advisory; `tokens.txt` (`drift_test.go`) is blind to token-invariant breaking
classes. Resolution: a **hard `go test` `TestNoUnacknowledgedBreakingDelta`**
(`make schema-delta`, run in the required `contract` job) diffs the base golden
against the regenerated schema for the ★ classes and **fails unless a
`CHANGELOG.md` `### Breaking` acknowledgement is present** (§4.3). `schema-tools`
stays the advisory categorizer. §1 language is narrowed to an enforced-vs-convention
table. Version-bump binding is **explicitly downgraded to convention-tier** with a
release-time gate deferred (§4.2), so bead G no longer claims an unenforced
guarantee. Acknowledgement mechanism (changelog vs label vs ack-file) is chosen
and justified in §4.3; the presence-granularity limitation is stated as cost 2.

### B4 — unify the serializer before freezing bytes — **CLOSED.**
Verified: `mustWritePulumiSchema` (`main.go:112-121`) sets `pkgSpec.Version = ""`
then `MarshalIndent`; `runPipeline` (`gen_test.go:67`) does `MarshalIndent`
**without** a `Version` reset (metadata: both use `json.Marshal`). The prior
§3.2 claim that `runPipeline` uses "MarshalIndent, Version=''" was **false** and
is corrected. Resolution: a **`prep:serializer`** bead (P1, **precedes the
tracer**) extracts `gen.MarshalSchemaJSON` / `gen.MarshalMetadataJSON` as the
single serialization source of truth, called by both the entrypoint and
`runPipeline` (§3.2, §5). Metadata moves to `MarshalIndent` in the same prep bead,
before the golden is frozen, so no later reflow churn.

### F1 — cover secret TYPE-properties — **CLOSED.**
Verified: `pass_secret_fields.go:14-15` marks
`unifi:sites/v1:HotspotVoucherDetails.code` secret at the **type** level;
`apiKey` is config-level (`packagespec.go:38`). Resolution: added
`guarantees.secretProperties` keyed by type token to `api-standards.yaml` (§3.6),
cross-checked against `secretTypeProperties`; a new linter subtest
`SecretTypeProperties` (§3.4) and the bijection claim assertion (§3.6) extend the
"every secret claim" assertion to type-level flags. §1 promise 4's "every" now
holds for config **and** type secrets.

### F2 — correct the SDK non-goal justification — **CLOSED.**
The false "pure function of schema.json … freezing the schema freezes the SDKs
transitively" is replaced: SDK surface also depends on the codegen toolchain
(`pulumi/actions@v6`, floating), so a toolchain bump can rename classes with a
green schema. Recorded as an **explicit uncovered risk** (§7 cost 1) with the
cheap mitigation (pin `pulumi/actions`/CLI) recommended; the non-goal bullet in
§1 is rewritten with the corrected reasoning.

### F3 — advisory base extraction needs full history — **CLOSED.**
Verified: `docs.yml:32` uses `fetch-depth: 0`; `main`'s `ci.yml` checkout uses
the default (depth 1). Resolution: both the `schema-contract` advisory job **and**
the required `contract` job (which `git show`s the base for the delta-diagnostic)
require **`fetch-depth: 0`** (or an explicit `git fetch origin ${BASE}`), matching
the `docs.yml` precedent (§3.3, §3.5, §5 beads C and K).

### F4 — state the couplings — **CLOSED.**
(a) Stated that the per-token census docs-truth **rests on the golden schema**
(post-B2), not the module-granularity yaml bijection, which is a deliberate stated
limitation (§3.6, §4.4, §7). (b) The tracer is labeled a **spine-tracer** for a
fan-out DAG (§5). (c) The pre-1.0 version policy now **explicitly states** that
additive and cosmetic share the patch slot, so a new resource ships as a patch
pre-1.0, and that this is intended (§4.2).

**Net:** all four blockers (B1–B4) and all four findings (F1–F4) are **fully
closed**; no finding is deferred. The *enhancements* the findings surfaced but
that are out of the minimal slice (per-change ack matching, release-time version
gate, toolchain pin, deprecation-window enforcement) are recorded as explicit
deferrals in §7, distinct from the findings themselves.
