# Review 06 — Codegen Pipeline Robustness & Future-Proofing Against Upstream API Changes

**Reviewer role:** adversarial — codegen pipeline robustness vs. upstream UniFi controller
changes.
**Central question:** when Ubiquiti ships a new controller version (new endpoints, renamed
schemas, new write verbs, changed pagination, new discriminated unions, fixed/new spec-quality
issues), does this pipeline keep working — or **silently** break / produce wrong output?
**Scope:** `openapi/`, `provider/cmd/`, `provider/pkg/gen/`, `provider/pkg/provider/`, `Makefile`.
**Date:** 2026-06-03. Pinned spec: `unifi-network/10.4.57.json` @ beezly
`ea6a5bc3…` (sha256 `ee1492cf…`).

---

## Executive summary

The pipeline is **deterministic and well-tested for the spec it is pinned to**, but it is
**fragile across spec bumps in ways that mostly fail SILENTLY** — i.e. they produce a clean
build and a clean `make test`/`make generate` that nonetheless ships a wrong or incomplete
provider. There is **no CI** (Phase 5 not started), so the only thing standing between a spec
bump and a broken release is a human reading codegen output diffs that **are not committed and
therefore cannot be diffed**.

The single most dangerous class of failure is **silent under-coverage**: a spec bump that adds a
writable entity whose shape pulschema's coalescing doesn't recognize yields a create-only resource
stub that builds, passes tests, and *dies on the consumer's second `pulumi up`*. The DESIGN's
headline claim — "bumping the pinned spec auto-promotes read-only entities to resources as Ubiquiti
ships writes, no per-resource hand-coding" — is **only true for entities shaped exactly like the 9
that exist today**. New shapes silently degrade.

Severity tally: **2 Critical, 6 High, 5 Medium, 3 Low.** The Criticals and most Highs are
SILENT-failure modes. Phase-5 hardening items in BUILD-PLAN address roughly a third of them; this
review widens that list and prioritizes by silence.

### Top silent-failure modes (fix these first)

1. **Spec-bump under-coverage** — a new writable/discriminated entity whose CRUD doesn't coalesce
   becomes a create-only stub; builds & tests green, breaks at consumer apply time. (Critical, F1)
2. **No `info.version` ↔ pin cross-check** — `fetch.sh`/`main.go`/`gen_test.go` all hardcode the
   filename `10.4.57`; nothing asserts the *fetched bytes* actually contain that version. A
   mis-edited SHA that resolves to a different version generates silently from the wrong spec.
   (Critical, F2)
3. **Pagination `limit=200` is hardcoded and non-uniform in the spec** — `hotspot/vouchers` already
   caps at 1000; a future sub-200 cap makes the follow-up GET 400 (loud) but a changed envelope
   (`items` instead of `data`, cursor instead of offset) makes aggregation **silently return one
   page**. (High, F5)
4. **`excludedPaths` drift** — a removed excluded path = dead entry (silent no-op); a *new*
   RPC/ordering/actions path = junk resource emitted into the schema and SDK (silent until a human
   inspects the token set). No guard. (High, F4)

---

## Future spec change → pipeline impact table

| # | Future upstream change | Stage hit | Outcome | Loud / **Silent** | Sev |
|---|---|---|---|---|---|
| 1 | New writable entity, **same shape** as today's 9 (oneOf+discriminator, `/coll` + `/coll/{id}`) | pulschema + `coalesceDiscriminatedCRUD` | Auto-promotes to full-CRUD resource(s). **Works as designed.** | n/a | — |
| 2 | New writable entity, **novel shape** (composite key, 2 item params, no clean `/{id}` sibling, unsplittable discriminator) | `coalesceDiscriminatedCRUD` Phase 1 | `m.R/U/D` stay nil → **create-only stub**; created once, dies on next `up` ("read endpoint unknown") | **Silent** (build+test green) | **Critical** |
| 3 | SHA bumped to a commit whose file is a **different version** than the filename implies | `fetch.sh` checksum | Checksum mismatch → loud fail **iff** checksum also bumped correctly. If both bumped to the wrong file, generates from wrong spec silently | **Silent** (if checksum co-bumped wrong) | Critical |
| 4 | New `*/actions`, `*/ordering`, `*/references`, `*/statistics/*` RPC path | pulschema grouping | Junk resource/function emitted (not in `excludedPaths`) | **Silent** | High |
| 5 | An existing excluded path is **removed/renamed** upstream | `excludedPaths` match | Dead entry; if renamed, the renamed RPC path now leaks as junk | **Silent** | High |
| 6 | Pagination envelope changes (`data`→`items`, `totalCount` dropped, cursor paging) | `OnPostInvoke` envelope sniff | Sniff misses → returns **first page only**; large collections silently truncated | **Silent** | High |
| 7 | A `limit` cap **below 200** introduced on a list endpoint | pagination follow-up GET | Server 400s the `limit=200` query | Loud (per-read error) | Medium |
| 8 | Upstream **adds** `components.securitySchemes` (e.g. real OAuth/bearer) | `injectAPIKeySecurityScheme` | Adds a *second* `ApiKeyAuth` scheme; framework may pick either; auth header may be wrong | **Silent** (until a 401) | High |
| 9 | Upstream changes the auth header name (`X-API-Key`→`Authorization`/`X-Api-Token`) | `apiKeyHeaderName` const | Provider sends the old header → 401 on every call | Loud (live), **Silent** at build | High |
| 10 | Upstream ships an **absolute** or different server URL (not relative `/integration`) | `rewriteServerURL` | Overwrites it with the hardcoded `…/proxy/network/integration`; if upstream moved the base path, every request 404s | **Silent** at build (loud live) | Medium |
| 11 | New spec-quality issue **not** in `SanitizeSpecBytes`'s known set (circular `$ref`, `$ref` to nonexistent, new invalid char class, `type: [string,null]` arrays, `nullable` quirks) | sanitize → kin-openapi load → pulschema | Either kin-openapi rejects (loud panic) **or** pulschema panics cryptically (`contract.Failf`) **or** generates a degraded type silently | Mixed (often **Silent degrade**) | Medium |
| 12 | Upstream **adds titles** to component schemas | `ensureSchemaTitles` | No-op (only fills empty titles). Fine — **but** if upstream titles collide, getter names could still collapse; not guarded | **Silent** | Low |
| 13 | pulschema / framework pseudo-version bumped (untagged `v0.0.0-<sha>`) | whole pipeline | Grouping/CRUD/title behavior may drift; no changelog, no pin rationale | **Silent** | High |
| 14 | `pulumi` CLI version drift (gen-sdk) | `make python_sdk` | SDK shape changes run-to-run across machines; determinism is re-run-equality, not version-pinned | **Silent** | Medium |
| 15 | Spec adds `info.license` with a real name | `SanitizeSpecBytes` license drop | Only drops *empty* license; a present one is kept (fine) | n/a | — |
| 16 | A discriminated entity gains a **new variant** | pulschema split + coalesce | New variant auto-promotes **iff** it shares the item path; `TestDiscriminatedResourcesHaveFullCRUD` won't cover it (hardcoded list) | **Silent** (uncovered) | Medium |
| 17 | GitHub down / rate-limited at build | `fetch.sh` curl | `curl -f` fails loud; build aborts (no offline cache) | Loud | Low |

---

## Findings (severity-ordered, silent failures emphasized)

### F1 — [CRITICAL, SILENT] Spec-bump auto-promotion only works for today's entity shapes
**`provider/pkg/gen/schema.go:205-253` (`coalesceDiscriminatedCRUD`), `:260-271` (`findItemPath`)**

The DESIGN §5 promise ("bumping the pinned spec auto-promotes read-only entities to resources … no
per-resource hand-coding") rests entirely on `coalesceDiscriminatedCRUD` filling R/U/D/P for the
18-of-21 stubs pulschema leaves create-only. That repair is shape-specific:

- It only fills a verb **if `findItemPath` finds exactly one `collPath + "/{param}"` sibling**
  (`schema.go:266`, `isSinglePathParamSegment`). An entity whose item path is *not* a single
  `{param}` segment — e.g. composite-key resources (`/coll/{a}/{b}`), or an entity with no item
  GET/PATCH/DELETE at all (write-only), or one where pulschema fails to recognize a *new*
  `oneOf`/`discriminator` dialect and never emits the variant tokens — gets **nothing filled**.
- The result is a resource token with `C` bound but `R == nil`. The framework lets it `create`,
  then on the next `pulumi up` the read fails with "resource read endpoint is unknown" — **at the
  consumer, in production, not at build.**
- `make test` would **still pass**: `TestResourceCRUDBindsItemLevel` (crudmap_test.go:92) iterates
  `pkg.Resources` and *would* catch an `R == nil` on a resource that pulschema emitted… **but only
  if pulschema emitted it as a resource at all.** A new discriminator shape pulschema can't split
  may produce *no* resource token (only data sources), so the writable entity silently never
  appears — and no test asserts "entity X must be writable."

**Trigger:** any new writable entity not shaped like the current 9 (Ubiquiti is actively shipping
writes through 2026 — this *will* happen).
**Why silent:** build green, `make test` green, `make generate` green; failure surfaces only at a
consumer's second apply or as a missing resource nobody notices.
**Hardening:**
1. Add a **negative guard** test: after coalescing, assert *no* resource token has `R == nil`
   (today's set happens to satisfy this; make it a permanent invariant so a new unsplittable shape
   fails the gate **loudly**). `crudmap_test.go` already has the iteration; tighten
   `TestResourceCRUDBindsItemLevel` to fail on `R == nil` for *every* resource unconditionally
   (it already does — but extend `coalesceDiscriminatedCRUD` to log/error when it *cannot* find an
   item path for a create-only resource rather than silently leaving it).
2. Add an **expected-writable-entities allowlist** test: list the entity collection paths expected
   to be writable; on bump, a new POST collection that does *not* materialize as a resource fails
   the gate, forcing human review. This converts "silent under-coverage" into a loud token-drift
   signal (overlaps BUILD-PLAN Phase-5 "token-set drift guard", which is **not yet implemented**).

### F2 — [CRITICAL, SILENT] No verification that the fetched bytes match the pinned version
**`openapi/fetch.sh:8-11`, `provider/cmd/pulumi-gen-unifi/main.go:51`, `provider/pkg/gen/gen_test.go:25`**

The version string `10.4.57` is a **filename convention**, not a verified property. `fetch.sh`
checks the **sha256** of the downloaded bytes (`fetch.sh:19-26`) — good — but nothing asserts the
spec's `info.version` equals `10.4.57`, nor that the three independent hardcodes agree. I confirmed
there is **no `info.version` cross-check anywhere** in `provider/`.

Failure path: a maintainer bumps `SHA` + `WANT_SHA256` together (the documented workflow) but to
the wrong file (e.g. `unifi-protect`, or an adjacent version). The checksum *matches the wrong
bytes*, fetch succeeds, codegen runs on the wrong spec, and the only tell is the version inside
`openapi_generated.yml` — a **gitignored artifact nobody reads**.

**Why silent:** checksum verifies *integrity*, not *identity*. The pin manifest and the bytes can
disagree with no signal.
**Hardening:**
- In `main.go`, after `GetOpenAPISpec`, assert `openAPIDoc.Info.Version` equals a value passed via
  flag/embedded constant; mismatch → `panic`. Cheap, converts silent → loud.
- Better: derive `OUT`/`SRC_PATH` in `fetch.sh` from a single `VERSION` variable and have the
  Makefile pass the same version to `-spec`, so there is **one** source of truth (see F3).

### F3 — [HIGH, SILENT-drift] Version coupling: 6+ hardcodes, no single source of truth
**`Makefile:13` (`SPEC`), `provider/cmd/pulumi-gen-unifi/main.go:51` (default `-spec`),
`provider/pkg/gen/gen_test.go:25` (`rel`), `openapi/fetch.sh:9-10` (`SRC_PATH`/`OUT`),
`openapi/SOURCE:9,18`**

A version bump requires editing **at least 6 locations** across 5 files (plus README/CLAUDE/DESIGN
prose). I grepped them all (`10.4.57` appears in Makefile, main.go default flag, gen_test.go
constant, SOURCE ×4, fetch.sh ×2, and three docs). The Makefile *passes* `-spec=$(SPEC)` to the
generator, so `main.go:51`'s default is only used when run by hand — but `gen_test.go:25` hardcodes
the path **independently of the Makefile**, so a bump that updates the Makefile but not the test
makes `make test` **skip** (the test `t.Skipf`s when the spec is absent — `gen_test.go:33`) rather
than fail. **A skipped determinism/CRUD gate looks green.**

**Why silent:** `t.Skipf` on a missing spec means the entire codegen test suite can no-op after a
half-finished bump and still report success.
**Hardening:**
- Single source of truth: put `VERSION`/`SHA`/`SHA256` in `openapi/SOURCE` (or a tiny
  `openapi/pin.env`), have `fetch.sh`, the Makefile, and tests read from it. No string should
  appear twice.
- Make the spec **mandatory** for the codegen tests in CI: a `-require-spec` mode (or a CI env
  flag) that turns `t.Skipf` into `t.Fatalf`, so a missing/misnamed spec is loud in the gate.

### F4 — [HIGH, SILENT] `excludedPaths` is empirical, hardcoded, and unguarded against drift
**`provider/pkg/gen/schema.go:29-37`**

The list comments itself "grows empirically as codegen output is inspected" — i.e. it's a manual
denylist of non-CRUD RPC paths. I verified all 7 entries currently match real paths in the spec
(no dead entries today). But on bump there are **two silent failure directions**:

- **New junk path** (`*/actions`, `*/ordering`, `*/references`, `*/statistics/*`, or a brand-new
  RPC verb) is *not* excluded → pulschema emits a junk resource/function into `schema.json` and the
  Python SDK. Consumers see a nonsense resource; nobody is alerted.
- **Removed/renamed excluded path** → the entry is a dead no-op (harmless) or, if renamed, the new
  name leaks as junk (same as above).

No test asserts (a) every `excludedPaths` entry still matches a spec path, or (b) the emitted token
set didn't grow unexpectedly. BUILD-PLAN Phase-5 names exactly this guard ("every `excludedPaths`
entry matches a path in the sanitized spec") but it is **unimplemented**.
**Hardening:** implement both Phase-5 guards now as no-Docker unit tests:
1. `for e := range excludedPaths: assert doc.Paths.Find(e) != nil` → dead-entry detection (loud).
2. Snapshot the resource+function **token set** (sorted) into a committed golden file; on bump,
   diff and require explicit human update. This is the single highest-leverage guard for catching
   *both* junk-resource leakage **and** F1's silent under-coverage in one signal.

### F5 — [HIGH, SILENT] Pagination assumes a fixed `{data,totalCount}` offset envelope and a uniform 200 cap
**`provider/pkg/provider/provider.go:163` (`listPageLimit=200`), `:173-183` (envelope sniff),
`:236-263` (`aggregatePages`)**

Verified against the spec: the envelope today is uniformly `{count,data,limit,offset,totalCount}`
across all 23 page DTOs — so the current sniff (`has "data"` && `has "totalCount"`,
provider.go:178-183) is correct *today*. Two future-fragility points:

- **The `limit` cap is NOT uniform.** I found `hotspot/vouchers` caps `limit` at **1000**
  (default 100) while everything else caps at 200. The provider hardcodes `limit=200`
  (`listPageLimit`), so vouchers paginate at 1/5 the allowed page size — **functionally correct but
  silently 5× more requests.** More dangerously: if a future endpoint introduces a cap **below
  200**, the `limit=200` follow-up GET is rejected (`provider.go:215` surfaces a non-200 as an
  error — *loud*, at least). The page size should be read from the spec's `limit` parameter
  `maximum`, not a constant.
- **Envelope-shape change is silent truncation.** If upstream renames `data`→`items`, drops
  `totalCount`, or moves to cursor paging, the sniff at `:178-183` returns `nil, nil` →
  `OnPostInvoke` defers to the framework's single GET → **the data source silently returns only the
  first page.** This is the exact "decodes cleanly, looks like success" correctness bug the Phase-4
  page-aggregation work was meant to kill — and it reappears the moment the envelope changes.

**Why silent:** partial collections decode without error; a consumer iterating "all networks" just
sees fewer than exist.
**Hardening:**
1. Drive page size from the spec: in codegen, capture each list endpoint's `limit` `maximum` into
   metadata; have `OnPostInvoke` use it instead of `listPageLimit`.
2. Add an **assertion** (codegen-time or a test) that every list endpoint's response schema is the
   `{data,totalCount}` envelope; a new shape fails the gate loudly instead of truncating at runtime.
3. Detect cursor paging (`nextCursor`/`next` fields) and error explicitly rather than silently
   returning page 1.

### F6 — [HIGH, SILENT-then-401] `injectAPIKeySecurityScheme` assumes the spec has no securitySchemes
**`provider/pkg/gen/openapi_fixes.go:78-97`**

I verified the current spec ships **no `securitySchemes` and no top-level `security`** — so the
injection is correct today. But the function **unconditionally adds** `ApiKeyAuth` and
**overwrites** `openAPIDoc.Security` (`openapi_fixes.go:94-96`). If upstream starts shipping a real
security scheme (Ubiquiti formalizing auth, OAuth, a bearer token):

- The injected `ApiKeyAuth` is **added alongside** the upstream scheme (different key → no
  collision detected), and the global `Security` requirement is **clobbered** to ApiKeyAuth-only.
- The framework derives the auth header name from *a* scheme; if it picks the upstream one, or if
  the real API moved off `X-API-Key`, every request 401s — **at runtime, not build.**

**Why silent at build:** the inject never fails; nothing asserts the spec lacked a scheme.
**Hardening:** make injection **conditional and loud**: if `Components.SecuritySchemes` is already
non-empty, `panic`/error ("upstream now declares security — review FixOpenAPIDoc") rather than
silently layering a second scheme. Same for `openAPIDoc.Security` already being set.

### F7 — [HIGH, SILENT] Untagged dependency pseudo-versions with no behavioral pin
**`provider/go.mod:6-7`**

```
github.com/cloudy-sky-software/pulschema v0.0.0-20260425162045-4f93ef0f7fdc
github.com/cloudy-sky-software/pulumi-provider-framework v0.0.0-20260425164420-ac1778da4c41
```

Both core codegen/runtime deps are **untagged commit pseudo-versions**. The *entire* resource
grouping, discriminator splitting, getter-naming, and CRUD-binding behavior — the things this
provider's correctness depends on — live in pulschema and can change between commits with **no
changelog and no semver signal.** A `go get -u`, a transitive bump, or a deliberate pulschema bump
to pick up a fix can silently alter the generated token set or CRUD bindings. The
`ensureSchemaTitles` and `coalesceDiscriminatedCRUD` workarounds are **calibrated to specific
pulschema behavior**; an upstream pulschema fix to the discriminator split would make
`coalesceDiscriminatedCRUD` either redundant or actively wrong (double-binding verbs).
**Why silent:** Go modules happily resolve pseudo-versions; nothing flags behavioral drift.
**Hardening:** (a) implement BUILD-PLAN Phase-5 "record dependency-pin rationale" — one line per
SHA stating what behavior it provides; (b) the token-set golden (F4) is the safety net that catches
pulschema behavior drift; (c) add a unit test asserting `coalesceDiscriminatedCRUD` is still
*needed* (i.e. pulschema still emits create-only stubs) so that if upstream fixes it, the now-wrong
workaround is flagged.

### F8 — [MEDIUM, SILENT-degrade] `SanitizeSpecBytes` handles a fixed, known set of spec-quality issues
**`provider/pkg/gen/spec_sanitize.go` (whole file)**

The sanitizer is solid for the **known** beezly-capture defects: invalid component keys
(`:67-77`), empty `info.license` (`:39-45`), typeless schemas (`:89-94`, `typeDeterminingKeys`),
null `items` (`:110-116`), non-string enums (`:183-185`). Enumerated **new** defects and their
fates:

| New defect | Fate | Loud/Silent |
|---|---|---|
| New invalid char class in a key | Handled (`invalidIdentChars` = negation of allowed set) | n/a |
| New collision after sanitize | **Loud** hard error (`:70`) — good | Loud |
| Circular `$ref` | Not handled; kin-openapi or pulschema may stack-overflow / panic | Loud (cryptic) |
| `$ref` to a nonexistent schema | Not handled; pulschema deref → likely panic | Loud (cryptic) |
| `type: ["string","null"]` (3.1 union types) | `isStringEnum`/`isTypelessSchema` treat `type` as present; pulschema may not understand the array → **degraded/empty type** | **Silent degrade** |
| `nullable: true` (3.0 leftover) | Ignored; may produce a non-nullable Pulumi prop | **Silent degrade** |
| Deeply nested `{}`/null beyond walked keywords (`$ref` inside `examples`, `webhooks`, `callbacks`) | `patchEmptySchemas` only walks a fixed keyword list (`:188-213`) — new container keywords missed | **Silent** (may crash pulschema later) |
| `enum` with mixed string+null | `isStringEnum` returns false (a null isn't a string) → enum **dropped silently** | **Silent** |

**Why mostly silent-degrade:** the sanitizer's philosophy is "normalize to free-form object," which
is *type-safe* but *information-lossy* — a new quirk it doesn't recognize tends to either pass
through and produce a vague `object` type or crash pulschema with a stack trace that doesn't name
the offending schema.
**Hardening:** (a) after sanitize+fix, run kin-openapi's `Validate()` and fail loudly on any
validation error (catches dangling/circular refs with a named location before pulschema's opaque
panic); (b) add a post-codegen assertion that no *resource input property* degraded to bare
`additionalProperties:true` unexpectedly (catches silent type loss); (c) handle 3.1 union
`type` arrays explicitly.

### F9 — [MEDIUM, SILENT-then-404] `rewriteServerURL` hardcodes the base path
**`provider/pkg/gen/openapi_fixes.go:20,99-108`**

`integrationBasePath = "/proxy/network/integration"` is hardcoded and **overwrites** whatever
server the spec ships (`:107`). Verified the spec ships relative `/integration` today, so the
rewrite is necessary and correct. But if upstream versions the base path (`/v2/integration`,
`/proxy/network/integration/v2`) or moves it, the rewrite **silently keeps the old path** and every
live request 404s.
**Why silent at build:** the rewrite never inspects what it's replacing.
**Hardening:** assert the *incoming* server URL is the expected relative `/integration` before
overwriting; if it's anything else, error ("upstream server URL changed — review FixOpenAPIDoc").

### F10 — [MEDIUM, SILENT] `coalesceDiscriminatedCRUD` / `findItemPath` ambiguity on nested collections
**`provider/pkg/gen/schema.go:260-277`**

`findItemPath` filters to single-`{param}` siblings and excludes `excludedPaths`. Verified that
today's writable collections with nested subpaths (`acl-rules/ordering`,
`firewall/policies/ordering`, `networks/{id}/references`, `devices/{id}/actions|statistics`) are all
either excluded or fail the single-param check — so coalescing is unambiguous **today**. The
fragility: it depends on `excludedPaths` correctly listing every non-item sibling. If a bump adds a
sub-collection under a writable entity that is *also* a single `{param}` segment (e.g.
`/networks/{networkId}` vs a hypothetical `/networks/{templateId}` sibling), `findItemPath` returns
the **first sorted match** (`:266` returns on first hit) — possibly the wrong one — and binds R/U/D
to it silently.
**Hardening:** when `findItemPath` finds **more than one** single-param sibling, error rather than
silently picking the sorted-first (the doc comment at `:258` acknowledges "the unexpected case of
multiple matches" but the code returns the first instead of failing).

### F11 — [MEDIUM, SILENT] Determinism is re-run equality, not cross-environment reproducibility
**`provider/pkg/gen/gen_test.go:68-83` (`TestPipelineDeterministic`), `Makefile:46-51`**

`TestPipelineDeterministic` proves *same process, same spec → identical output*, and BUILD-PLAN
Phase-5 extends this to all four artifacts. But determinism is **conditioned on tool versions**:
pulschema, the framework, the `pulumi` CLI (for `gen-sdk`), and `gopkg.in/yaml.v3` (OpenAPI doc
marshaling) all influence the bytes. None of these is reproducibly pinned for the SDK step — the
`pulumi` CLI is whatever is on `PATH` (`Makefile:48`). Two machines with different `pulumi` CLIs can
produce different `sdk/python` from the same spec, and since the SDK is **gitignored**, there is
nothing to diff against.
**Why silent:** "deterministic" is asserted within a run; cross-machine drift is invisible.
**Hardening:** pin the `pulumi` CLI version (record required version, check it in the Makefile/CI),
and add the SDK to the re-run-equality gate (Phase-5 item, unimplemented).

### F12 — [MEDIUM] `apiKeyHeaderName` is a compile-time constant
**`provider/pkg/gen/openapi_fixes.go:14`, `provider/pkg/provider/provider.go:85-87`**

If Ubiquiti renames the auth header, the const and `GetAuthorizationHeader` ship the wrong header →
401 on every live call. Loud at runtime, **invisible at build/test** (no live test in the gate).
Lower than F6 because it's a one-line fix and unlikely, but it's a hardcoded contract with no guard.
**Hardening:** covered if F6's "inject only when absent" lands — once upstream ships its own scheme,
the header name comes from the spec. Until then, a one-line note that this const tracks the real API.

### F13 — [LOW, LOUD] `fetch.sh` robustness
**`openapi/fetch.sh`**

Read carefully — it's actually **good**: `set -euo pipefail` (`:5`), `curl -fsSL` (`-f` fails on
HTTP 4xx/5xx, `:17`) writing to a `.tmp` then atomic `mv` only after checksum passes (`:17,28`),
explicit checksum mismatch error with want/got and cleanup (`:20-26`). Gaps, all minor/loud:
- **No retry/offline cache** — GitHub down or rate-limited aborts the build (loud, acceptable; F17
  in the table). A vendored-fallback or `--retry` would harden CI.
- **No `curl` version/`shasum` portability note** — `shasum -a 256` is BSD/mac; CI on a minimal
  Linux image may need `sha256sum`. Currently relies on `shasum` being present.
- Partial download is handled (curl `-f` + checksum), so corruption fails loud. Good.
**Hardening:** add `curl --retry 3 --retry-delay 2`; document the `shasum` dependency or detect
`sha256sum` fallback.

### F14 — [LOW] No CI = no automated bump safety net (the meta-finding)
**(BUILD-PLAN Phase 5, not started)**

Every silent failure above is gated only by `make test` + a human eyeballing artifacts. With no CI:
nothing runs the determinism/CRUD/token guards on a bump, and the artifacts that *would* reveal
drift are gitignored (uncommittable to diff). The DESIGN's "bump the SHA and regenerate" workflow
has **zero** automated verification.
**Hardening:** implement Phase-5 CI now, with the four guards from this review wired as required
checks: (1) token-set golden diff (F4/F1), (2) `excludedPaths`-still-match (F4), (3) re-run
equality across all four artifacts incl. SDK (F11), (4) `info.version` ↔ pin assertion (F2).

### F15 — [LOW] `ensureSchemaTitles` becomes a partial no-op if upstream adds titles
**`provider/pkg/gen/openapi_fixes.go:62-74`**

Only fills **empty** titles (`:71`). If upstream adds titles, the function defers to them (correct).
Residual risk: upstream titles that **collide** could re-introduce the getter-name collapse
`ensureSchemaTitles` exists to prevent, and nothing asserts title uniqueness. Very low likelihood.
**Hardening:** assert post-condition that all component titles are unique; error on collision.

---

## Prioritized hardening roadmap

**Do first (convert silent → loud, cheap):**
1. `info.version` ↔ pin assertion in `main.go` (F2) — ~5 lines.
2. Tighten `coalesceDiscriminatedCRUD` to **error** when a create-only resource has no item path,
   and keep `TestResourceCRUDBindsItemLevel` asserting `R != nil` for *all* resources (F1).
3. `excludedPaths`-still-match unit test + dead-entry detection (F4) — BUILD-PLAN Phase-5 item.
4. Conditional/loud security-scheme injection and server-URL rewrite (F6, F9) — guard the
   "upstream changed its assumptions" case.

**Do next (catch silent drift):**
5. Committed **token-set golden** + diff gate (F4, F1, F7) — the single highest-leverage guard.
6. Drive pagination page size from the spec's `limit.maximum`; assert envelope shape (F5).
7. Single source of truth for the version string; make codegen tests fail (not skip) on missing
   spec in CI (F2, F3).

**Do for release hygiene:**
8. Implement Phase-5 CI with the four required guards (F14).
9. Pin `pulumi` CLI version; extend re-run equality to the SDK (F11).
10. Dependency-pin rationale + a test asserting `coalesceDiscriminatedCRUD` is still needed (F7).
11. kin-openapi `Validate()` pass after fix layer to surface dangling/circular refs loudly (F8).

---

## What is genuinely robust (credit where due)

- `fetch.sh` checksum-verifies with atomic write and loud mismatch (F13) — solid integrity gate.
- `SanitizeSpecBytes` collision detection is a **hard error**, not a silent overwrite
  (`spec_sanitize.go:70`).
- `TestPipelineDeterministic` + the crudmap tests are real, meaningful regression guards for the
  *current* spec; `TestDiscriminatedResourcesHaveFullCRUD` pins variants by name so a regression
  that drops one is caught.
- `apiHost` validation (`provider.go:127`) and the empty-page pagination terminator
  (`provider.go:249`) show the author already thinks about silent-corruption failure modes — this
  review extends that instinct to the spec-bump axis.
- The architecture (spec-as-source-of-truth, deterministic regen, gitignored artifacts) is the
  *right* shape; the gap is the **verification layer** around bumps, which Phase 5 is supposed to
  build and hasn't yet.
