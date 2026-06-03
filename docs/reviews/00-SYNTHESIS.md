# 00 — Synthesis: Adversarial Review → Actionable Work & Milestones

**Date:** 2026-06-03
**Inputs:** six adversarial reviews in this directory —
[01 Pulumi best-practices](01-pulumi-best-practices.md),
[02 Go best-practices](02-golang-best-practices.md),
[03 API shape & UX](03-api-shape-ux.md),
[04 Codegen maximization](04-codegen-maximization.md),
[05 Testing coverage](05-testing-coverage.md),
[06 Pipeline & future-proofing](06-pipeline-futureproofing.md).

This document deduplicates findings across all six, groups them into cross-cutting themes, and
sequences them into milestones with concrete work items. Each item is tagged with the source
finding(s) and a rough effort (S/M/L). Read the individual reviews for the full argument and
`file:line` evidence behind each item.

---

## Verdict in one paragraph

The architecture is right and faithfully executed: a genuinely thin, well-commented, deterministic
hand-written layer over `pulschema` + `pulumi-provider-framework`, with no hand-authored resource
list. The read-path MVP is sound and the codegen discipline (determinism, sorted iteration,
`ensureSchemaTitles`, the CRUD-coalescing repair) is thoughtful. **But the project has two systemic
gaps that dominate everything else: (1) there is no regression safety net for the one operation the
whole design is built around — bumping the spec — so spec/dependency bumps fail _silently_; and (2)
the discriminated-variant handling, while it now round-trips CRUD, leaks its mechanism into a
confusing and partly-unsafe public API.** Most of the rest is polish (token/description quality) and
a handful of real Go robustness bugs in `provider.go`. None of the criticals block the read-path MVP,
but several will produce broken or surprising behavior the moment writes run against a live
controller, or the moment the spec is bumped.

---

## Cross-cutting themes (where the reviews converge)

The same root issues surfaced independently in multiple reviews. The high-agreement themes are the
ones to fund first.

| Theme | Severity | Converging findings | Why it matters |
|---|---|---|---|
| **A. No safety net for spec/dep bumps (silent failure)** | 🔴 Critical | 05‑C1/C2/C3, 06‑F1/F2/F3/F4/F7/F14, 04‑F3/F4/F6, 02 go.mod | The product's core workflow is "bump spec → regenerate → trust." Nothing committed to diff, no CI, no token-drift guard, no `info.version` check, determinism gate covers 2 of 4 artifacts, tests _skip_ (look green) when the spec is missing. A bump that changes 40 tokens, leaks a junk resource, or under-covers a new writable entity passes every check. |
| **B. Discriminated-variant split leaks & is unsafe at the edges** | 🔴 Critical | 01‑C1/H4/H5, 03‑F1/F2/F3, 04‑F1/F2/F3, 06‑F1, 05‑H1 | `type`/`management` discriminator survives the split as a _required free-form string_ (no enum/const/default) — worst of both worlds. Variant tokens (`Standard`, `Mac`, `Ipv4`, `Ports`, `Gateway`) lose parent context and risk cross-entity token collisions on a future bump. `coalesceDiscriminatedCRUD`'s item-path heuristic is fragile and the variant write path is never tested end-to-end. |
| **C. Generated public API is unpolished / leaks internals** | 🟠 High | 01‑H3/H4/H5, 03‑F3/F4/F5/F6/F7/F11/F12 | Zero descriptions on all 21 resources + 50 functions; `getCountrie`/`getFirewallPolicie` (broken singularization); 15 `getIntegration*Dto` internal names; snake_case-in-camelCase tokens; `*Page` names + dead pagination knobs after auto-aggregation; near-duplicate functions. |
| **D. Resource modeling violates Pulumi semantics** | 🟠 High | 01‑C2/H2/M5, 03‑F8/F10 | No `replaceOnChanges` on any immutable/identity field (`type`, `siteId`, `vlanId`); `Voucher`/`AdoptDevice` are batch/action RPCs masquerading as CRUD; per-resource `siteId` input exists but is **not honored** (a trap). |
| **E. Go robustness bugs in `provider.go`** | 🟠 High | 02‑H1/M1/M3/M2/L1, 04‑F7 | Unbounded pagination loop (no page ceiling, no `ctx` check) can hang/OOM `pulumi up`; package-level mutable globals; unbounded `io.ReadAll` on error bodies; archived `pkg/errors`; swallowed `ParseBool`. |
| **F. Fix-layer assumptions break silently on upstream change** | 🟠 High | 06‑F5/F6/F8/F9/F10 | Security-scheme injection assumes the spec has none (silently layers a 2nd); server-URL rewrite assumes a relative `/integration`; pagination assumes `{data,totalCount}`/limit=200; sanitizer only knows today's defects. All overwrite-without-checking. |
| **G. Shrink hand-written surface via upstream PRs** | 🟡 Medium | 04‑U1…U7 | The most opinionated code (`coalesceDiscriminatedCRUD`, `injectInsecureTransport`, pagination, enum-drop, key-sanitize, title-fix) exists to work around `pulschema`/framework gaps. Upstreaming deletes it. |
| **H. Release/distribution unproven** | 🟡 Medium | 01‑M1/M2, 05‑M2/M3/M4, 06‑F11 | `PluginDownloadURL` unvalidated (DESIGN §7 wrongly describes it); version wiring is dead-flag + blank schema version + `0.0.0` pyproject; Tier-2 e2e unimplemented so live round-trip, secure TLS path, and variant CRUD are all unverified; `pulumi` CLI version unpinned for SDK gen. |

---

## The three single highest-leverage moves

If only three things get done, do these — each collapses a whole class of risk:

1. **Stand up the bump safety net (Theme A): a committed token-set golden + drift gate, an
   `excludedPaths`-still-exist guard, the `info.version`↔pin assertion, and CI to run them.** This one
   bundle catches Theme A _and_ the silent halves of Themes B and F (junk-resource leakage, variant
   token collisions, under-coverage of new writable entities) in a single reviewed diff. It is the
   prerequisite that makes every later change safe to land. (M0 below.)
2. **Fix the discriminator in-pipeline (Theme B): pin each variant's `type`/`management` to a
   const/default and drop it from required inputs; entity-prefix the variant tokens.** ~30 lines in
   the existing gen-fix layer turns the most confusing, partly-unsafe part of the API into something
   idiomatic. (M2.1–M2.2.)
3. **File the pulschema full-CRUD-discriminated-binding PR (U1).** It deletes the single most
   opinionated, most fragile component in the repo (`coalesceDiscriminatedCRUD` + `findItemPath`,
   ~116 lines) and is the structural fix for Theme B's CRUD half. Long-horizon, but start it now.

---

## Quick wins (low effort, do opportunistically)

- Replace `github.com/pkg/errors` with stdlib `errors`/`fmt.Errorf("%w")`; drop the dep. (02‑M2, S)
- `io.LimitReader(resp.Body, 4<<10)` on the paginated-error body. (02‑M3, S)
- Log (don't swallow) an unparseable `allowInsecure`. (02‑L1, S)
- Real package doc in `version.go` instead of blanket `// nolint: revive`. (02‑L7, S)
- De-duplicate the `Config`↔`InputProperties` descriptions from one source (they already drift). (01‑M3, 04‑F5, S)
- Drop the duplicate `"unifi"` keyword. (01‑L1, S)
- Mark voucher `code` (a credential) `secret`. (01‑L2, S)
- Fix DESIGN §7's `PluginDownloadURL` description and the false "pulschema emits apiKey from the scheme" claim in DESIGN §6 / `schema.go`. (01‑M1, 04‑F5, S)
- Update the stale `test/e2e/README.md` (claims `allowInsecure` is unimplemented; it isn't). (05‑M4, S)

---

## Milestones

Sequenced so each milestone de-risks the next. **M0 is a hard prerequisite** — do not bump the spec
or dependencies, or land the in-pipeline rewrites of M2/M3, without it, because today nothing would
catch a regression those changes introduce.

### M0 — Bump safety net & CI _(prerequisite; Theme A + silent halves of B/F)_

The goal: convert every silent spec/dep-bump failure into a loud, reviewed diff.

- **M0.1** Add `.github/workflows/ci.yml`: a no-Docker `make test` job (fetch + unit gate +
  determinism + `go vet` + `gofmt -l`) and a Docker `make test-mock` job; make them required checks.
  (05‑C1, 06‑F14) — **M**
- **M0.2** Commit a `testdata/tokens.txt` golden (sorted resource + function token set) and a
  no-Docker `drift_test.go` that diffs against it; updating it is a deliberate, reviewed PR step. This
  is the single highest-leverage guard — it catches token drift, junk-resource leakage (F4),
  variant token collisions (B/04‑F3), and new-writable-entity under-coverage (06‑F1) at once.
  (05‑C3, 04‑F3/F4, 06‑F1/F4) — **M**
- **M0.3** In the same `drift_test.go`: assert every `excludedPaths` entry still matches a path in the
  sanitized spec (dead-entry detection), and assert the resource/function token sets contain **no
  duplicate short names** (collision guard). (04‑F4, 06‑F4) — **S**
- **M0.4** Extend `TestPipelineDeterministic` to all four artifacts (add `openapi_generated.yml`; add a
  CI step that runs `make generate` twice and `git diff --exit-code` over `sdk/`). (05‑C2, 06‑F11) — **S/M**
- **M0.5** Add an `info.version`↔pin assertion in `main.go` (panic if the fetched spec's
  `Info.Version` ≠ the pinned version). (06‑F2) — **S**
- **M0.6** Single source of truth for the spec version: derive the filename from one place
  (`openapi/SOURCE`/a `pin.env` read by `fetch.sh`, the Makefile, and the test). Make codegen tests
  **fail, not `t.Skipf`**, when the spec is missing in CI. (04‑F6, 06‑F3) — **M**
- **M0.7** Negative-coverage guard: tighten the gen layer so a create-only resource with no resolvable
  item path is a **loud error** (not a silent stub), and keep `TestResourceCRUDBindsItemLevel`
  asserting `R != nil` for _every_ resource unconditionally. (06‑F1, 04‑F2) — **S**
- **M0.8** Record dependency-pin rationale in `go.mod`/a deps note (what each untagged `v0.0.0-<sha>`
  provides + what to re-verify on bump), and add a test asserting `coalesceDiscriminatedCRUD` is still
  _needed_ (pulschema still emits create-only stubs) so an upstream fix flags the now-wrong workaround.
  (02 go.mod, 06‑F7) — **S**

**Done when:** a deliberately wrong spec bump (extra token, dropped exclusion, version mismatch,
under-covered writable entity) fails CI loudly with a reviewable diff.

### M1 — Runtime correctness & fix-layer guards _(Themes E + F)_

- **M1.1** Pagination loop: add a page ceiling derived from `totalCount` (fallback constant), a
  non-advancing-page guard, and a per-iteration `ctx.Err()` check. (02‑H1) — **S**
- **M1.2** Move the framework `handler` (and drop the `callback` global) onto the `unifiProvider`
  struct; removes the data-race/multi-instance hazard and the test save/restore dance. (02‑M1) — **M**
- **M1.3** `io.LimitReader` the error body; quick-win error/bool fixes (02‑M3/L1); `pkg/errors`→stdlib
  (02‑M2); `interface{}`→`any` pass (02‑L4); config-resolution helper to collapse the 4× env-fallback
  duplication (02‑M4). — **S/M**
- **M1.4** Make the fix layer **loud on changed assumptions**: error if the spec already declares
  `securitySchemes`/`security` before injecting (F6); error if the incoming server URL is not the
  expected relative `/integration` before rewriting (F9); error if `findItemPath` finds >1 single-param
  sibling instead of picking sorted-first (F10/04‑F2). (06‑F6/F9/F10) — **S**
- **M1.5** Run kin-openapi `Validate()` after the fix layer to surface dangling/circular `$ref`s with a
  named location before pulschema's opaque panic. (06‑F8) — **S**
- **M1.6** Drive pagination page size from the spec's `limit` `maximum` (captured into metadata at
  codegen) instead of the hardcoded `200`; assert at codegen-time that every list endpoint uses the
  `{data,totalCount}` envelope (and detect cursor paging → explicit error). (06‑F5, 04‑F8) — **M**

**Done when:** a misbehaving controller can't hang/OOM a plan; an upstream change to auth/server/
pagination/envelope fails the build loudly rather than 401-ing or truncating at runtime.

### M2 — Public API polish (in-pipeline rewrites) _(Themes B-surface + C)_

All of these live in the same deterministic gen-fix layer that already hosts
`coalesceDiscriminatedCRUD`; none requires upstream changes. Land after M0 so the token golden
captures the (intended) renames.

- **M2.1** Discriminator fix: for each split variant, set `type`/`management` to a single-value
  `const`/`enum` + `default` and drop it from `requiredInputs` (framework injects it on the wire).
  Removes the worst day-one trap. (01‑C1, 03‑F1) — **M**
- **M2.2** Entity-prefix variant tokens for discoverability + collision-safety:
  `WifiBroadcastStandard`/`WifiBroadcastIotOptimized`, `ManagedNetworkGateway`/`…Switch`/`…Unmanaged`,
  `TrafficMatchIpv4`/`TrafficMatchIpv4Addresses`/`…Ipv6Addresses`/`…Mac`/`…Ports`, `DnsARecord`…
  (01‑H4, 03‑F2, 04‑F3) — **M**
- **M2.3** Token-normalization pass: strip `Integration*`/`*Dto`, fix singularization (with an
  exceptions table — `getCountrie`→`getCountry`, `getFirewallPolicie`→…), normalize casing
  (kill snake-in-camel), and settle a consistent `get<Entity>` (one) / `list<Entities>` (many) scheme.
  (01‑H4, 03‑F3/F4/F5) — **M/L**
- **M2.4** Synthesize a one-line `Description` for every resource and function from the OpenAPI
  operation `summary`/entity+verb. (01‑H3, 03‑F6) — **M**
- **M2.5** De-pageify list data sources: rename `*Page`→plural list and drop the now-meaningless
  `limit`/`offset` (keep `data`, optionally a true aggregate `count`/`totalCount`) from the aggregated
  result type. (03‑F7) — **M**
- **M2.6** Prune the redundant per-variant `getIntegration*Dto` getters (keep one canonical item getter
  + the list getter per endpoint). (01‑H5, 03‑F3) — **S/M**
- **M2.7** Lower-priority polish: de-dupe structurally identical enums (03‑F11); preserve numeric enums
  (or take U3) so `broadcastingFrequenciesGHz` keeps its {2.4,5,6} validation (03‑F9, 04‑F10);
  prune unreferenced empty types (01‑L1). — **M**

**Done when:** the SDK reads as a clean, idiomatic, documented provider — no `Dto`/`Integration`
leakage, no bare `Standard`/`Mac` tokens, no apparent-typo function names, no required magic strings.

### M3 — Pulumi resource-model correctness _(Theme D)_

- **M3.1** Set `replaceOnChanges` (and mark output-only / drop from inputs where the API never accepts
  them) for identity/immutable fields: the discriminator `type`, `siteId`, `vlanId`, and anything the
  spec marks read-only. (01‑H2, 01‑M5) — **M**
- **M3.2** Per-resource `siteId`: either honor it in the glue (preferred — the framework already
  prioritizes resource-level over global) with a confirming test + a `replaceOnChanges` + a
  description, **or** remove the non-functional input so it isn't a silent trap. (01‑M5, 03‑F10) — **M**
- **M3.3** `Voucher`/`AdoptDevice` are batch/action RPCs, not CRUD: at minimum rename `AdoptDevice` to
  a noun and document the batch/action semantics loudly; consider moving their collection POSTs into an
  "actions" exclusion set so they don't masquerade as ordinary declarative resources. (01‑C2, 03‑F8) — **M**

**Done when:** `pulumi preview` shows correct replace-vs-update on identity changes; no resource has
hidden action/batch semantics presented as plain CRUD.

### M4 — Live verification (Tier-2 e2e) _(Theme H — coverage)_

The whole write/discriminated surface is currently unverified end-to-end.

- **M4.1** Implement Tier-2 provisioning (the open infra task: headless UniFi OS Server + key minting
  via scripted UI or pre-baked volume snapshot). (BUILD-PLAN Tier-2) — **L**
- **M4.2** Live discriminated-variant CRUD round-trip on a throwaway test SSID (`Standard`/
  `IotOptimized`), asserting create→update→delete and a no-op second `up`. Short-term proxy: add a
  Prism write-dispatch case for `Standard` with a hand-iterated spec-valid body + a unit test that the
  discriminator lands in the request body. (05‑H1, 05‑M2) — **M/L**
- **M4.3** Verify the CA-pinned secure TLS path (`allowInsecure=false`) live; add a partial no-Docker
  `httptest`+`SSL_CERT_FILE` test (Linux) in the meantime. (05‑M3) — **M**
- **M4.4** `OnPostInvoke` real multi-page test via `httptest` (3-page collection; assert all rows +
  offset/limit query params + the non-200 error branch). (05‑H2) — **S/M**
- **M4.5** Python SDK smoke test in CI (`import pulumi_unifi` + assert a stable class set incl. a
  discriminated variant). (05‑H3) — **S**

**Done when:** BUILD-PLAN's unchecked verification boxes (live read, live SSID round-trip) are green.

### M5 — Release & distribution (Phase 5) _(Theme H — release)_

- **M5.1** Release workflow: build per-OS/arch plugin binaries with asset names matching the
  `github://` scheme; clean-machine `pulumi up` auto-install smoke test. (01‑M1, BUILD-PLAN Phase 5) — **M**
- **M5.2** Fix version wiring: use or remove the dead `-version` flag; ensure `GetSchema` returns a
  non-empty version matching the binary (add a test); set the SDK package version from the release tag
  (no `0.0.0` on PyPI). (01‑M2) — **M**
- **M5.3** Pin/record the required `pulumi` CLI version (it influences `gen-sdk` output and is currently
  whatever is on `PATH`). (06‑F11) — **S**
- **M5.4** Harden `make test-mock` teardown with a `trap … EXIT` so a bring-up failure can't leave the
  stack up. (05‑L1, BUILD-PLAN Phase 5) — **S**

**Done when:** a tagged release auto-installs on a clean machine and the SDK publishes with a real
version.

### M6 — Upstream contributions (parallel, long-horizon) _(Theme G)_

File these PRs; each deletes hand-written surface here once merged. Ranked by surface removed.

| # | Target | Change | Removes here | Effort |
|---|---|---|---|---|
| **U1** | pulschema | Bind **all** verbs to each discriminated-variant token (per-verb discriminator schema names + full CRUD per variant) | all of `coalesceDiscriminatedCRUD` + `findItemPath` (~116 lines) — Theme B's structural fix | L |
| **U6** | pulschema | Treat constraint-less/`null`/description-only schemas as free-form `any`; tolerate non-identifier component keys internally | most of `SanitizeSpecBytes` | M |
| **U3** | pulschema | Support numeric/integer/boolean enums | the enum-drop branch (restores 8 enums) | M |
| **U7** | pulschema | Assign stable unique titles to title-less discriminated schemas internally | `ensureSchemaTitles` | L(ow) |
| **U4** | framework | Export an insecure-transport / 429-retry-wrapper setter | `injectInsecureTransport` → 1 line, restores 429 retry | Low |
| **U5** | framework | Schema-driven list pagination from the page envelope | `OnPostInvoke`/`aggregatePages` (~100 lines) | M/L |
| **U2** | framework | Accept a relative/path-only server URL + host override | `rewriteServerURL` | M |

U1 + U6 + U7 together would reduce `gen/` to the two irreducible UniFi facts (inject `X-API-Key`,
name the base path) + the `PackageSpec` identity block.

---

## What is genuinely good — do **not** regress these

All six reviews independently credited the same strengths:

- **Thinness honored** — no business logic leaked into the hand-written glue; no resource list, no
  `grouping.{go,yaml}`. The architecture is the right shape. (02, 04)
- **`apiKey` secrecy is correct end-to-end** (`Secret: true` both blocks; SDK `Output.secret`;
  `AcceptSecrets: true`). (01)
- **Env-var defaults wired on all four config keys**, and `firstEnv` mirrors the schema's declared env
  list rather than hardcoding. (01)
- **`apiHost` validation** pre-empts the framework's silent `baseURL.Host =` corruption — exactly the
  right defensive check. (01, 02, 06)
- **Determinism discipline** — `ensureSchemaTitles`, sorted iteration, blanked schema version,
  `TestPipelineDeterministic`. The `sortedKeys[V any]` generic is clean. (02, 04, 06)
- **`SanitizeSpecBytes` collision detection is a hard error**, not a silent overwrite. (02, 06)
- **`crudmap_test.go` is the model to emulate** — re-derives the sibling-path invariant structurally,
  pins variants by name; not tautological. Apply this style to the new guards. (05)
- **Pagination is factored as a pure, testable seam** (`aggregatePages` with injected `fetch`). The
  bug it fixes (partial data that decodes cleanly) is real. (02, 01)
- **`panic`/`contract.Failf` in the build-time codegen path is the correct idiom** — do not convert
  those to error returns. (02)
- **`fetch.sh`** checksum-verifies with atomic write + loud mismatch — a solid integrity gate. (06)

---

## Open question for the maintainer (a fork in the road)

Theme B has two viable end-states for discriminated entities:

- **Keep per-variant resources** (status quo + M2.1/M2.2 polish): cheaper, in-pipeline now, but
  permanently ships N near-identical 80%-shared resources per entity and a shared read endpoint that
  can't disambiguate variants on refresh/import.
- **Collapse to one tagged-union resource per entity** (`WifiNetwork`, `DnsRecord`,
  `ManagedNetwork`, `TrafficMatchingList`): the idiomatic Pulumi shape that matches the REST entity and
  fixes refresh/import disambiguation — but it is an **upstream pulschema behavior change** (part of
  U1's design space) and a breaking API change if done after publish.

Because it's hard to change post-publish, decide this **before the first release**. The pragmatic path
is: ship M2.1/M2.2 now (makes per-variant tolerable), pursue U1 with the tagged-union shape as the
target, and gate the first public release on which model is committed.
