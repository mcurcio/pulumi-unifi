# WORK — Actionable backlog from the adversarial review

Derived from [00-SYNTHESIS.md](00-SYNTHESIS.md). Each ticket = one PR. **A PR is not done until it
ships its own regression guard** — that guard is usually the acceptance criterion.

This plan is organized **cohesion-first**: a Wave-0 "seam" lane establishes responsibility-aligned
module boundaries *before* the feature work that would otherwise pile into monolithic files
("make the change easy, then make the easy change"). Seams are prioritized by
**cohesion-debt × incoming-churn** — refactor exactly where new work will land, leave cohesive/stable
code alone.

**How to read this:** the [Master table](#master-table) is the at-a-glance view. The
[Cohesion model](#cohesion-model--module-maps) explains the target module structure. [Sequencing
constraints](#sequencing-constraints) explains what actually limits parallelism. [Tickets](#tickets)
has the detail, grouped by track (= owner = parallel lane).

### Legend

- **Track** — an ownable lane. **Track 0** = cohesion seams (do first). Tickets in different tracks
  rarely touch the same files; tickets in the same track should be done by one owner in listed order.
- **Wave** — earliest start respecting dependencies. **Wave 0** = seams (start now); Wave 1 = needs the
  token golden (A-M0.2) or is output-independent; Wave 2 = feature passes/runtime work on the seams;
  Wave 3 = needs the final API shape.
- **Effort** — S (≤½ day), M (≈1–3 days), L (multi-day / infra).
- **Status** — ☐ Todo · ◐ In progress · ☑ Done.

---

## Master table

| ID | Title | Track | Wave | Effort | Depends on | Status |
|---|---|---|---|---|---|---|
| S0.1 | Split `pkg/provider` by responsibility | 0 | 0 | M | pairs with B-M1.2 | ☑ |
| S0.2 | Gen post-process pass pipeline + migrate coalesce | 0 | 0 | M | — | ☑ |
| S0.3 | Split `gen/schema.go` identity vs orchestration (single-source config) | 0 | 0 | S/M | before A-M0.2 | ☑ |
| S0.5 | Mapping-data layer: `mappings.yaml` + loader (externalize editorial maps) | 0 | 2 | M | D-M2.1/2.3 (inventory done); before Phase-2 | ☑ |
| S0.4 | Promote passes to `pkg/gen/genpass` sub-package _(deferred)_ | 0 | 2 | S | ≈6 passes exist | ☐ |
| QW-1 | `version.go` package doc | QW | 1 | S | — | ☑ |
| QW-2 | De-dup `Config`↔`InputProperties` → **folded into S0.3** | QW | 0 | — | — | ☑ |
| QW-3 | Drop duplicate `"unifi"` keyword | QW | 1 | S | before A-M0.2 | ☑ |
| QW-4 | Mark voucher `code` secret | QW | 1 | S | before A-M0.2 | ☑ |
| QW-5 | Fix DESIGN §7 PluginDownloadURL + §6 apiKey claim | QW | 1 | S | — | ☑ |
| QW-6 | Update stale `test/e2e/README.md` | QW | 1 | S | — | ☑ |
| A-M0.2 | Token-set golden + `drift_test.go` | A | 1 | M | S0.3, QW-3/4 (baseline after) | ☑ |
| A-M0.3 | `excludedPaths` existence + dup-token guard | A | 1 | S | A-M0.2 | ☑ |
| A-M0.4 | Determinism gate → all 4 artifacts | A | 1 | S/M | — | ☑ |
| A-M0.5 | `info.version` ↔ pin assertion | A | 1 | S | — | ☑ |
| A-M0.6 | Single source of truth for spec version; fail-not-skip | A | 1 | M | — | ☑ |
| A-M0.7 | Negative-coverage guard (`R==nil` → loud error) | A | 1 | S | S0.2, A-M0.2 | ☑ |
| A-M0.8 | Dep-pin rationale + "coalesce still needed" test | A | 1 | S | S0.2 | ☑ |
| A-M0.1 | CI workflow (required checks) | A | 1 | M | soft: A-M0.2..0.8 | ☑ |
| B-M1.2 | Move `handler`/`callback` globals onto the struct | B | 0 | M | done **with** S0.1 | ☑ |
| B-M1.1 | Pagination loop: ceiling + `ctx` + non-advancing guard | B | 1 | S | S0.1 | ☑ |
| B-M1.3 | Go hygiene bundle (errors sweep rides in S0.1; rest after) | B | 0→1 | S/M | S0.1 | ☑ |
| B-M1.6 | Pagination page-size from spec `limit.maximum` | B | 2 | M | S0.1, metadata capture | ☐ |
| C-M1.4 | Loud-on-changed-assumptions guards (auth/server/itempath) | C | 1 | S | — | ☑ |
| C-M1.5 | kin-openapi `Validate()` after the fix layer | C | 1 | S | — | ☑ |
| D-M2.1 | `pass_discriminator.go` — const/default, drop required | D | 2 | M | S0.2, A-M0.2 | ☑ |
| D-M2.2 | `pass_token_rename.go` — entity-prefix variant tokens | D | 2 | M | S0.2, A-M0.2 | ☑ |
| D-M2.3 | `pass_token_rename.go` — normalization (Dto/singular/case/get-list) | D | 2 | M/L | D-M2.2 (same file) | ☑ |
| D-M2.4 | `pass_descriptions.go` — synthesize descriptions | D | 2 | M | S0.2, A-M0.2 | ☐ |
| D-M2.5 | `pass_depage.go` — de-pageify list data sources | D | 2 | M | S0.2, A-M0.2 | ☐ |
| D-M2.6 | `pass_prune_getters.go` — prune redundant DTO getters | D | 2 | S/M | S0.2, A-M0.2 | ☐ |
| D-M2.7 | `pass_enum.go` — dedup + preserve numeric + prune empty types | D | 2 | M | S0.2, A-M0.2 | ☐ |
| D-M3.1 | `pass_replace_on_changes.go` — identity/immutable fields | D | 2 | M | S0.2, A-M0.2 | ☐ |
| D-M3.2 | Per-resource `siteId`: honor or remove (cross-cutting) | D | 2 | M | S0.1, S0.2, A-M0.2 | ☐ |
| D-M3.3 | `Voucher`/`AdoptDevice` action/batch (cross-cutting) | D | 2 | M | S0.2, A-M0.2 | ☐ |
| E-M4.1 | Tier-2 provisioning (UniFi OS Server + key mint) | E | 1 | L | — | ☐ |
| E-M4.4 | `OnPostInvoke` multi-page `httptest` | E | 1 | S/M | S0.1 | ☑ |
| E-M4.5 | Python SDK smoke test | E | 1 | S | — | ☑ |
| E-M4.3 | Verify CA-pinned secure TLS path | E | 1→3 | M | partial now; live needs E-M4.1 | ◐ |
| E-M4.2 | Live variant CRUD round-trip (throwaway SSID) | E | 3 | M/L | E-M4.1, Track D | ☐ |
| F-M5.4 | `make test-mock` teardown trap | F | 1 | S | — | ☑ |
| F-M5.1 | Release workflow + clean-machine auto-install smoke | F | 1→3 | M | finalize after D | ☐ |
| F-M5.2 | Version wiring fix (dead flag / schema version / SDK ver) | F | 3 | M | — | ☐ |
| F-M5.3 | Pin/record required `pulumi` CLI version | F | 3 | S | — | ☐ |
| A-M0.2′ | Re-baseline token golden to shipped shape | A | 3 | S | Track D | ☐ |
| G-U1 | pulschema: full-CRUD discriminated binding | G | 1 | L | external | ☐ |
| G-U2 | framework: relative/path-only server URL | G | 1 | M | external | ☐ |
| G-U3 | pulschema: numeric/int/bool enums | G | 1 | M | external | ☐ |
| G-U4 | framework: exported insecure-transport / 429 setter | G | 1 | S | external | ☐ |
| G-U5 | framework: schema-driven pagination | G | 1 | L | external | ☐ |
| G-U6 | pulschema: tolerate typeless schemas + bad keys | G | 1 | M | external | ☐ |
| G-U7 | pulschema: stable titles for title-less schemas | G | 1 | S | external | ☐ |

---

## Cohesion model & module maps

### Cohesion audit (whole tree — splits *and* deliberate non-splits)

| File | Responsibilities today | Cohesion | Incoming churn | Verdict |
|---|---|---|---|---|
| `pkg/provider/provider.go` | assembly + config + auth + pagination + transport | **Low** (≈4) | Track B | **Split** (S0.1) |
| `pkg/gen/schema.go` | identity literal + orchestration + CRUD-repair algorithm | **Low/Med** (≈3) | Track D (7 passes) | **Split** (S0.2/S0.3) |
| `pkg/gen/openapi_fixes.go` | spec fixes on the parsed doc (one phase) | **High** | C-M1.4 | Keep (guards fit the phase) |
| `pkg/gen/spec_sanitize.go` | raw-bytes sanitization | **High** | none | **Leave alone** |
| `pkg/provider/serve.go` | serving | **High** | none | **Leave alone** |
| `pkg/version/version.go` | version constant | **High** | QW-1 | Keep (+package doc) |
| `cmd/pulumi-gen-unifi/main.go` | CLI + orchestration + IO | **Med** | A-M0.5/0.6 (small) | Optional (low ROI) |

Restraint is part of the discipline: `spec_sanitize.go`/`serve.go` are cohesive and out of the path of
new work — touching them would be churn for its own sake (the mirror smell). The two splits are
warranted because low cohesion coincides with incoming churn.

### Target: provider runtime (file split; package stays one cohesive unit)

```
provider.go    → struct, makeProvider, GetGlobalPathParams        (assembly/lifecycle)
config.go      → OnConfigure, firstEnv, apiHost validation, config-resolution helper
auth.go        → GetAuthorizationHeader                            (tiny; may fold into config)
pagination.go  → OnPostInvoke, aggregatePages, toSlice, toInt
transport.go   → injectInsecureTransport
```

### Target: gen (three-phase seam by input type — raw bytes → parsed doc → Pulumi artifacts)

```
spec_sanitize.go   → phase 1: raw-bytes sanitization            (exists, cohesive)
openapi_fixes.go   → phase 2: fixes on the parsed doc           (exists, cohesive)
packagespec.go     → static PackageSpec identity + config vars  (single-sourced; from schema.go)
genstate.go        → GenState{*Pkg, *Meta, *Doc} — the contract passes mutate
schema.go          → PulumiSchema: sanitize→fix→pulschema→run passes   (orchestration only)
pass_coalesce_crud.go      → migrated coalesceDiscriminatedCRUD (+test)   [S0.2 seed]
pass_discriminator.go      → D-M2.1
pass_token_rename.go       → D-M2.2 + D-M2.3   (naming = one responsibility → one file)
pass_prune_getters.go      → D-M2.6
pass_depage.go             → D-M2.5
pass_enum.go               → D-M2.7
pass_descriptions.go       → D-M2.4
pass_replace_on_changes.go → D-M3.1
```

A pass is `{name string; fn func(*GenState) error}` in an ordered slice — named (for logging +
deterministic ordering), independently testable, no interface ceremony. Justified now because the
instance count goes 1 → 8; it would be gold-plating for a single pass. (The 3-function `openapi_fixes`
phase is **not** pass-ified — that's the granularity dial.) `GenState` taking pointers also retires the
by-value-copy finding (02-L3).

**Suggested pass order** (the one order-sensitive shared edit — see constraint 1):
`coalesce_crud → discriminator → token_rename → prune_getters → depage → enum → descriptions →
replace_on_changes` (structural/CRUD → naming → pruning → shape-trims → annotation last on the final
set). Tunable; document the rationale at the registration site.

---

## Sequencing constraints

What actually governs fan-out width (after the Wave-0 seams):

1. **Gen post-process: source collisions mostly gone; two intrinsic points remain.** With the pass
   pipeline (S0.2), Track D owners add *separate `pass_*.go` files* instead of all editing one
   function — so the source merge surface shrinks to **(a) the ordered pass-list registration line**
   (order-sensitive; coordinate) and **(b) the `tokens.txt` golden rebase**. (b) is intrinsic to
   single-artifact deterministic codegen and cannot be parallelized away; (a) is one line. Net: Track D
   goes from "one owner, strictly serial" to "many owners on separate files, coordinating only on the
   pass order + golden." Tickets that share a *responsibility* (D-M2.2 + D-M2.3, both naming) correctly
   share `pass_token_rename.go` and are sequenced under one sub-owner — that's cohesion working as
   intended, not a defect.
2. **Provider runtime: hotspot relaxed by S0.1.** After the split, B-M1.1/B-M1.6 live in
   `pagination.go`, B-M1.3's helper in `config.go`, B-M1.2 in `provider.go` — different files, so the
   "one owner" rule collapses to "one owner does S0.1+B-M1.2 first, then pagination/config work
   parallelizes." The cross-cutting `pkg/errors`→stdlib sweep rides in the S0.1 PR window so it sweeps
   once.
3. **Baseline the golden after the output-affecting Wave-0/1 work.** S0.3 (single-sources config
   descriptions → subsumes QW-2), QW-3 (keyword), QW-4 (voucher secret) all change generated bytes.
   Land them, *then* snapshot A-M0.2. (S0.1, S0.2, A-M0.5/0.6/0.7/0.8, C, E, F, G do **not** change
   output and run anytime.)

**Critical path:** two short parallel seam tasks each unlock a fan-out —
`S0.2 → Track D passes` (gen) and `S0.1 → Track B fan-out` (provider) — then
`Track D → E-M4.2 → F (finalize)`. Off-path long poles to start in Wave 0/1: **E-M4.1 provisioning**
and **G-U1**.

**Decision gate (before first release):** keep per-variant resources (D-M2.1/2.2 polish) vs. collapse
to tagged-union resources (part of G-U1's design space) — hard to change post-publish. See
[00-SYNTHESIS § Open question](00-SYNTHESIS.md#open-question-for-the-maintainer-a-fork-in-the-road).

---

## Tickets

### Track 0 — Cohesion seams _(Wave 0; do first — they unlock the fan-outs)_

> Pure structural refactors (except S0.3's config single-sourcing). Verified behavior-neutral by the
> determinism test: same spec → byte-identical `schema.json`/`metadata.json` pre/post.

#### S0.1 — Split `pkg/provider` by responsibility · Wave 0 · M
- **Source:** cohesion review; enables B-M1.1/1.3/1.6 fan-out
- **Deliver:** split `provider.go` into `provider.go` (assembly/lifecycle) + `config.go` + `auth.go` +
  `pagination.go` + `transport.go` per the module map. Do it **in the same PR as B-M1.2** (the
  globals→struct change already restructures the file) and ride the `pkg/errors`→stdlib sweep
  (part of B-M1.3) through here so it sweeps once.
- **Accept:** each file is one responsibility; `go test ./... -race` green; no behavior diff.

#### S0.2 — Gen post-process pass pipeline + migrate coalesce · Wave 0 · M
- **Source:** cohesion review; keystone for Track D; retires 02-L3
- **Deliver:** add `genstate.go` (`GenState{*Pkg, *Meta, *Doc}`) and an ordered
  `[]pass{name, fn func(*GenState) error}` run after pulschema in `PulumiSchema`. Migrate
  `coalesceDiscriminatedCRUD` (+`findItemPath`/helpers) into `pass_coalesce_crud.go` as pass #1, taking
  `*GenState` instead of by-value `pkg`/`doc`.
- **Accept:** `schema.json`/`metadata.json` byte-identical pre/post (`TestPipelineDeterministic`);
  coalesce logic in its own file + test; `GenState` is the only contract passes touch.

#### S0.3 — Split `gen/schema.go` identity vs orchestration · Wave 0 · S/M
- **Source:** cohesion review; subsumes QW-2 (01-M3, 04-F5)
- **Deliver:** move the static `PackageSpec` literal + `excludedPaths` + config vars into
  `packagespec.go`; `schema.go` keeps `PulumiSchema` orchestration only. Single-source the config
  descriptions so `Config.Variables` and `Provider.InputProperties` derive from one map (the only delta
  is `DefaultInfo.Environment`).
- **Accept:** `schema.go` is orchestration-only; config descriptions defined once; this is the last
  output-affecting change before the A-M0.2 baseline.

#### S0.4 — Promote passes to a `pkg/gen/genpass` sub-package · Wave 2 · S _(deferred/conditional)_
- **Source:** granularity dial
- **Deliver:** once ≈6 `pass_*.go` files exist, move them to `pkg/gen/genpass` for navigability,
  exporting the `GenState` contract.
- **Accept:** do **not** do this preemptively; trigger only when the file count crosses the threshold.

#### S0.5 — Mapping-data layer: `mappings.yaml` + loader · Wave 2 · M
- **Source:** maintainer directive ([MAPPING-LAYER.md](MAPPING-LAYER.md)) — editorial api→pulumi
  mappings must be **data, not code**, for cross-UniFi-version stability.
- **Deliver:** a declarative `mappings.yaml` (the public-API contract) + a loader; migrate the four
  Go-literal maps the Track-D Phase-1 passes added — `entityPrefixes` (5), `irregularSingulars` (3),
  `acronymFixups` (1), `explicitFunctionRenames` (3) — into it, plus the discriminator config, and fold
  `excludedPaths` in. The passes become a generic engine that reads the data; derive-by-default,
  pin-by-exception. (Schema design questions in MAPPING-LAYER.md §"Open design questions".)
- **Accept:** zero naming/const/plural/exclusion literals remain in Go; `make generate` byte-identical
  before/after the migration (pure refactor — golden unchanged); a new entity with no mapping that
  isn't cleanly derivable fails **loud** ("unmapped entity").
- **Sequence:** do **before** Track-D Phase 2 (D-M2.4…D-M3.3) so those passes are data-driven from the
  start (their immutable-fields + description-overrides land in `mappings.yaml` too).

### Track A — Safety net & CI _(prerequisite lane)_

#### A-M0.2 — Token-set golden + `drift_test.go` · Wave 1 · M
- **Source:** 05-C3, 04-F3/F4, 06-F1/F4
- **Deliver:** commit `provider/pkg/gen/testdata/tokens.txt` (sorted resource + function token set);
  add `drift_test.go` (no Docker) that regenerates and diffs against it. Baseline **after** S0.3,
  QW-3, QW-4. Updating the golden is a deliberate, reviewed step.
- **Accept:** a synthetic junk token fails the test; a normal run is clean.

#### A-M0.3 — `excludedPaths` existence + dup-token guard · Wave 1 · S
- **Source:** 04-F4, 06-F4 · In `drift_test.go`: assert every `excludedPaths` entry resolves
  (`doc.Paths.Find(p) != nil`); assert resource+function token sets have no duplicate short names.
- **Accept:** a removed/renamed excluded path or a synthetic duplicate token fails.

#### A-M0.4 — Determinism gate → all 4 artifacts · Wave 1 · S/M
- **Source:** 05-C2, 06-F11 · Extend `TestPipelineDeterministic` to `openapi_generated.yml`; CI step
  runs `make generate` twice and `git diff --exit-code` over `sdk/`.
- **Accept:** nondeterminism in the OpenAPI doc or SDK fails the gate.

#### A-M0.5 — `info.version` ↔ pin assertion · Wave 1 · S
- **Source:** 06-F2 · In the codegen entrypoint, panic if `openAPIDoc.Info.Version` ≠ the pinned
  version. **Accept:** a different-version spec fails loudly.

#### A-M0.6 — Single source of truth for spec version; fail-not-skip · Wave 1 · M
- **Source:** 04-F6, 06-F3 · Derive the spec filename from one place read by `fetch.sh`, the Makefile,
  and tests; make codegen tests `t.Fatalf` (not `t.Skipf`) on a missing spec in CI.
- **Accept:** `grep` finds the version only in the pin file(s); a half-finished bump fails CI.

#### A-M0.7 — Negative-coverage guard (`R==nil` → loud error) · Wave 1 · S
- **Source:** 06-F1, 04-F2 · Make `pass_coalesce_crud` error when a create-only resource has no
  resolvable item path; keep `TestResourceCRUDBindsItemLevel` asserting `R != nil` for **every**
  resource. **Accept:** a synthetic unsplittable resource fails the build instead of shipping a stub.

#### A-M0.8 — Dep-pin rationale + "coalesce still needed" test · Wave 1 · S
- **Source:** 02 (go.mod), 06-F7 · One-line note per untagged `v0.0.0-<sha>` dep; a test asserting
  pulschema still emits create-only discriminated stubs. **Accept:** an upstream binding fix flags the
  now-redundant pass.

#### A-M0.1 — CI workflow · Wave 1 · M
- **Source:** 05-C1, 06-F14 · `.github/workflows/ci.yml`: no-Docker `make test` + determinism +
  `go vet` + `gofmt -l`, and a Docker `make test-mock` job; required checks. Scaffold early, expand as
  A-M0.2..0.8 land.

### Track B — Provider runtime (Go) _(de-serialized after S0.1)_

#### B-M1.2 — Move `handler`/`callback` globals onto the struct · Wave 0 · M
- **Source:** 02-M1 · Store the framework handle as a `unifiProvider` field; delete both package
  globals; drop the test save/restore dance. **Done with S0.1** in one PR.
- **Accept:** independent providers construct without touching package state; `go test -race` clean.

#### B-M1.1 — Pagination loop ceiling + `ctx` + non-advancing guard · Wave 1 · S
- **Source:** 02-H1 · In `pagination.go`: page ceiling from `totalCount` (fallback constant),
  non-advancing-page break, per-iteration `ctx.Err()`. **Accept:** a non-advancing server errors
  instead of looping. **Lands in `pagination.go`** (parallel with config work).

#### B-M1.3 — Go hygiene bundle · Wave 0→1 · S/M
- **Source:** 02-M2/M3/M4/L1/L4 · `pkg/errors`→stdlib (drop the dep) — **ride this through the S0.1
  window**; then `io.LimitReader` the paginated-error body, log unparseable `allowInsecure`,
  `interface{}`→`any`, extract the config-resolution helper into `config.go`.
- **Accept:** `go.mod` no longer requires `pkg/errors`; `go vet`/`gofmt` clean.

#### B-M1.6 — Pagination page-size from spec `limit.maximum` · Wave 2 · M
- **Source:** 06-F5, 04-F8 · Capture each list endpoint's `limit.maximum` into metadata at codegen
  (a small gen-side concern — likely its own pass or a metadata field); use it in `pagination.go`
  instead of hardcoded `200`. Assert at codegen-time that every list endpoint uses the
  `{data,totalCount}` envelope; detect cursor paging → explicit error.
- **Accept:** `hotspot/vouchers` paginates at its real cap; an envelope change fails the gate.

### Track C — Fix-layer guards _(gen `openapi_fixes.go`, not provider.go)_

#### C-M1.4 — Loud-on-changed-assumptions guards · Wave 1 · S
- **Source:** 06-F6/F9/F10 · Error if the spec already declares `securitySchemes`/`security` before
  injecting; error if the incoming server URL isn't the expected relative `/integration`; error if
  `findItemPath` finds >1 single-param sibling. **Accept:** synthetic specs that change each assumption
  fail with a clear message.

#### C-M1.5 — kin-openapi `Validate()` after the fix layer · Wave 1 · S
- **Source:** 06-F8 · Run `Validate()` after `FixOpenAPIDoc`. **Accept:** a dangling/circular `$ref`
  surfaces a precise error instead of an opaque pulschema panic.

### Track D — API polish + resource model _(pass-per-ticket; needs S0.2 + A-M0.2)_

> Each ticket = add one `pass_*.go` + `pass_*_test.go`, append one line to the pass list (mind the
> order), rebase the golden. Separate-file tickets fan out; same-responsibility tickets share a file.

#### D-M2.1 — `pass_discriminator.go` · Wave 2 · M
- **Source:** 01-C1, 03-F1 · For each split variant, set `type`/`management` to a single-value
  `const`/`enum` + `default`; drop from `requiredInputs`. **Accept:** schema shows the const; golden
  updated.

#### D-M2.2 — `pass_token_rename.go`: entity-prefix variant tokens · Wave 2 · M
- **Source:** 01-H4, 03-F2, 04-F3 · `WifiBroadcastStandard`, `ManagedNetworkGateway`,
  `TrafficMatchIpv4Addresses`, `DnsARecord`, … **Accept:** no bare `Standard`/`Mac`/`Ipv4`; A-M0.3
  passes; golden updated.

#### D-M2.3 — `pass_token_rename.go`: normalization · Wave 2 · M/L
- **Source:** 01-H4, 03-F3/F4/F5 · Strip `Integration*`/`*Dto`; fix singularization (exceptions
  table); normalize casing; settle `get<Entity>`/`list<Entities>`. **Same file as D-M2.2** (naming is
  one responsibility) → sequenced under one owner. **Accept:** no `Dto`/typo tokens; golden updated.

#### D-M2.4 — `pass_descriptions.go` · Wave 2 · M
- **Source:** 01-H3, 03-F6 · Backfill a one-line `Description` for every resource + function from
  operation `summary`/entity+verb. **Accept:** zero empty top-level descriptions.

#### D-M2.5 — `pass_depage.go` · Wave 2 · M
- **Source:** 03-F7 · Rename `*Page`→plural list; drop `limit`/`offset` from the aggregated result
  type. **Accept:** no `*Page` tokens; no stale paging knobs in the result.

#### D-M2.6 — `pass_prune_getters.go` · Wave 2 · S/M
- **Source:** 01-H5, 03-F3 · Keep one canonical item getter + list getter per endpoint; drop the
  per-variant DTO getters. **Accept:** function count drops; no two getters bind the identical endpoint.

#### D-M2.7 — `pass_enum.go` · Wave 2 · M
- **Source:** 03-F9/F11, 01-L1, 04-F10 · De-dup identical enums; preserve numeric enums (or rely on
  G-U3); prune unreferenced empty types. **Accept:** numeric-enum fields keep validation; no triplicate
  `*Action` enums.

#### D-M3.1 — `pass_replace_on_changes.go` · Wave 2 · M
- **Source:** 01-H2, 01-M5 · Set `replaceOnChanges` (and output-only/drop where the API never accepts)
  for `type`, `siteId`, `vlanId`, spec read-only fields. **Accept:** `preview` shows replace on identity
  change; schema test asserts the flags.

#### D-M3.2 — Per-resource `siteId`: honor or remove _(cross-cutting)_ · Wave 2 · M
- **Source:** 01-M5, 03-F10 · Either honor the per-resource `siteId` in `pkg/provider` glue (preferred)
  with a confirming test + description (via `pass_descriptions`) + `replaceOnChanges` (via
  `pass_replace_on_changes`), **or** remove the non-functional input. **Touches provider + ≥1 pass** —
  sequence after the passes it depends on. **Accept:** `siteId` either works (tested) or is absent.

#### D-M3.3 — `Voucher`/`AdoptDevice` action/batch _(cross-cutting)_ · Wave 2 · M
- **Source:** 01-C2, 03-F8 · Rename `AdoptDevice` to a noun (via `pass_token_rename`) + document
  batch/action semantics (via `pass_descriptions`); consider moving their collection POSTs into an
  "actions" exclusion set (`packagespec.go`/`excludedPaths`). **Accept:** no resource presents
  action/batch semantics as plain CRUD without docs.

### Track E — Live verification

#### E-M4.1 — Tier-2 provisioning · Wave 1 · L _(off-path long pole — start now)_
- **Source:** BUILD-PLAN Tier-2 · Headless UniFi OS Server + `X-API-Key` minting (scripted UI or
  pre-baked volume). **Accept:** `test/e2e/` brings up a controller + key unattended.

#### E-M4.4 — `OnPostInvoke` multi-page `httptest` · Wave 1 · S/M
- **Source:** 05-H2 · `httptest` server serving a 3-page collection; assert all rows, offset/limit on
  each follow-up, and a page-2 500 propagates. Lands against `pagination.go` (post S0.1).

#### E-M4.5 — Python SDK smoke test · Wave 1 · S
- **Source:** 05-H3 · `sdk/python/tests/test_smoke.py` (CI): install + `import pulumi_unifi` + assert a
  stable class set incl. a discriminated variant. **Accept:** a broken/uninstallable SDK fails CI.

#### E-M4.3 — Verify CA-pinned secure TLS path · Wave 1→3 · M
- **Source:** 05-M3 · Now: `httptest`+`SSL_CERT_FILE` (Linux) with `allowInsecure=false` asserting the
  handshake succeeds and the transport is **not** the insecure one. Live: confirm post E-M4.1.

#### E-M4.2 — Live variant CRUD round-trip · Wave 3 · M/L
- **Source:** 05-H1, 05-M2 · Throwaway SSID `Standard`/`IotOptimized` create→update→delete + no-op
  second `up`. Interim: a Prism `Standard` write-dispatch case + a unit test that the discriminator
  lands in the body. **Depends on:** E-M4.1 + Track D (shipped shape).

### Track F — Release

#### F-M5.4 — `make test-mock` teardown trap · Wave 1 · S
- **Source:** 05-L1 · Wrap bring-up+run+down in one block with `trap … EXIT`.

#### F-M5.1 — Release workflow + auto-install smoke · Wave 1 draft → 3 finalize · M
- **Source:** 01-M1 · Per-OS/arch binaries with `github://`-matching asset names; clean-machine
  `pulumi up` auto-install smoke. Finalize after Track D stabilizes the surface.

#### F-M5.2 — Version wiring fix · Wave 3 · M
- **Source:** 01-M2 · Use/remove the dead `-version` flag; `GetSchema` returns a non-empty version
  matching the binary (test); SDK package version from the release tag.

#### F-M5.3 — Pin/record required `pulumi` CLI version · Wave 3 · S
- **Source:** 06-F11 · Record + check the required `pulumi` CLI version (it influences `gen-sdk`).

### Track G — Upstream PRs _(fully parallel, external repos; start now)_

| ID | Target | Change | Removes here | Effort |
|---|---|---|---|---|
| G-U1 | pulschema | Bind all verbs to each discriminated-variant token | `pass_coalesce_crud` (~116 lines); Theme B structural fix | L |
| G-U6 | pulschema | Tolerate constraint-less/`null` schemas + bad component keys | most of `spec_sanitize.go` | M |
| G-U3 | pulschema | Numeric/int/bool enum support | the enum-drop branch | M |
| G-U7 | pulschema | Stable unique titles for title-less schemas | `ensureSchemaTitles` | S |
| G-U4 | framework | Exported insecure-transport / 429-retry setter | `transport.go`'s `injectInsecureTransport` → 1 line | S |
| G-U5 | framework | Schema-driven list pagination | `pagination.go` (~100 lines) | L |
| G-U2 | framework | Accept relative/path-only server URL + host override | `rewriteServerURL` | M |

### Quick wins _(tiny, standalone)_

- **QW-1** — `version.go` package doc (drop blanket nolint). 02-L7 · Wave 1 · S
- **QW-2** — De-dup `Config`↔`InputProperties` descriptions → **folded into S0.3**. 01-M3, 04-F5
- **QW-3** — Drop duplicate `"unifi"` keyword (in `packagespec.go` after S0.3). 01-L1 · Wave 1 · S ·
  output-affecting → before A-M0.2
- **QW-4** — Mark voucher `code` secret (gen). 01-L2 · Wave 1 · S · output-affecting → before A-M0.2
- **QW-5** — Fix DESIGN §7 PluginDownloadURL + §6 apiKey claim. 01-M1, 04-F5 · Wave 1 · S · docs
- **QW-6** — Update stale `test/e2e/README.md`. 05-M4 · Wave 1 · S · docs
