# Review 05 — Testing coverage & regression-guard quality

**Scope:** Adversarial assessment of whether the `pulumi-unifi` test suite is comprehensive and
guards against regressions, with emphasis on the two regression vectors this project is built
around: **upstream spec bumps** (the spec is the source of truth, fetched at build, regenerated
deterministically) and **dependency bumps** (`pulschema` / `pulumi-provider-framework` are untagged
pseudo-version pins).

**Reviewer stance:** skeptical. Treat "a test exists" as not equal to "the behavior is guarded."

---

## Executive summary

The unit layer is genuinely good *for the bugs it was written against*. The codegen fixes
(`FixOpenAPIDoc`, `ensureSchemaTitles`, `SanitizeSpecBytes`) and the CRUD-coalescing repair (B1) each
have targeted, behavior-asserting tests that re-derive the structural invariant rather than hardcode
strings — `crudmap_test.go` in particular is exemplary (it pins discriminated variants by name *and*
asserts the sibling-path relationship structurally). The pagination helper `aggregatePages` is
well-fuzzed at the edges. The wire test (`TestWirePath`) proves the two facts Prism cannot.

**But the suite has a structural blind spot that exactly matches the project's primary risk.** The
whole value proposition is "bump the spec, regenerate, trust the output." There is **no committed
golden baseline and no token-set / exclusion drift guard**. Determinism is only *re-run equality* —
a spec bump that silently adds, drops, or renames 40 resource tokens, drops the integration endpoint,
or makes an `excludedPaths` entry dangle would pass **every test in the suite**. The determinism gate
itself (`TestPipelineDeterministic`) covers only 2 of the 4 artifacts the docs say it must cover
(schema + metadata; **not** `openapi_generated.yml`, **not** the SDK). BUILD-PLAN Phase 5 lists both
the four-artifact gate and the drift guard as TODO; both are confirmed **absent**.

Compounding this: **there is no CI** (no `.github/`), so even the tests that exist are not enforced on
any change — they run only when a developer remembers `make test`. The Python SDK has **zero tests**,
not even the `import pulumi_unifi` smoke the docs claim is verified. Tier-2 e2e is unimplemented, so
the **live round-trip, the CA-pinned secure TLS path, and the discriminated-variant CRUD over the wire
are all unverified** — and the one resource exercised end-to-end (`FirewallZone`) was explicitly chosen
*because it avoids* the discriminated complexity that is the project's hardest, most-regression-prone
surface. Finally `OnPostInvoke` (the actual paginating provider method, as opposed to the pure
`aggregatePages` helper) is never exercised across a real multi-page boundary.

Net: the suite is a strong **unit** suite with a weak **regression-against-upstream-change** posture —
which is the posture that matters most here. Highest-leverage fixes: (1) a no-Docker token-set/exclusion
drift guard, (2) extend the determinism gate to all four artifacts, (3) stand up CI so the gate runs.

---

## Coverage matrix

Legend: **U** = unit (`make test`, no Docker) · **M** = mock/integration (`make test-mock`, Docker) ·
**E** = e2e (live controller) · **none** = no automated coverage.

| Behavior / invariant | U | M | E | Verdict |
|---|:--:|:--:|:--:|---|
| Spec sanitization: bad keys, refs, typeless, null items, non-string enum, empty license, collision-as-error | ✅ | — | — | Well covered (`spec_sanitize_test.go`) |
| `FixOpenAPIDoc` injects `X-API-Key` apiKey scheme | ✅ | — | — | Covered |
| `FixOpenAPIDoc` rewrites server to absolute integration base path | ✅ | — | — | Covered |
| `ensureSchemaTitles` (determinism of discriminated getter names) | ✅ | — | — | Covered |
| Pipeline determinism — `schema.json` | ✅ | — | — | Covered |
| Pipeline determinism — `metadata.json` | ✅ | — | — | Covered |
| Pipeline determinism — `openapi_generated.yml` | ❌ | — | — | **GAP** (docs say required) |
| Pipeline determinism — `sdk/` | ❌ | — | — | **GAP** (docs say required) |
| **Token-set / exclusion drift on spec bump** | ❌ | — | — | **ABSENT** (Phase-5 TODO) |
| **Golden/reviewed-baseline of generated output** | ❌ | — | — | **ABSENT by design** (re-run equality only) |
| Resource/function counts (21/50) match documented surface | ❌ | — | — | **ABSENT** |
| `excludedPaths` entries actually match a path in the spec | ❌ | — | — | **ABSENT** (silent dangle on bump) |
| Integration endpoint still present after bump | ❌ | — | — | **ABSENT** |
| crudMap covers every live token (reverse) | ✅ | — | — | Covered |
| crudMap keys are all live tokens / orphans pruned (B1 Phase-2) | ✅ | — | — | Covered |
| Resource CRUD binds item-level {id} for R/U/D/P (B1 Phase-1) | ✅ | — | — | Covered (all resources) |
| Discriminated variants gain full CRUD (named pin) | ✅ | — | — | Covered (`crudmap_test.go`) |
| Bare `X-API-Key` value (no scheme prefix) | ✅ | — | — | Covered (unit + wire) |
| `X-API-Key` on the wire | ✅(wire) | ❌(Prism no auth) | ❌ | Covered by `TestWirePath` only |
| `{siteId}` substitution on the wire | ✅(wire) | ✅ | ❌ | Covered |
| `apiHost` validation (reject scheme/path) | ✅ | — | — | Covered (table) |
| `allowInsecure` transport injection | ✅ | ✅(dogfood) | — | Covered insecure path |
| `allowInsecure=false` leaves framework transport intact | ✅ | — | — | Covered |
| **CA-pinned secure TLS path (`allowInsecure=false` trusts real CA)** | ❌ | ❌ | ❌ | **UNVERIFIED** (e2e-only, unimplemented) |
| `aggregatePages` edge cases (single/multi/empty/error/no-total) | ✅ | — | — | Well covered |
| **`OnPostInvoke` real multi-page loop (CreateGetRequest+HTTP)** | ❌ | ❌(weak oracle) | ❌ | **GAP** — only the pure helper is tested |
| Non-page passthrough (naked array / single object body) | ⚠️ | — | — | `toSlice` unit only; method-level path untested |
| Read path end-to-end (URL+TLS+decode), no-siteId + sites/v1 | — | ✅ | ❌ | Mock only |
| Write dispatch C→U→D (flat body) | — | ✅(FirewallZone) | ❌ | Mock only, stateless |
| **Write round-trip of a discriminated variant (Standard/IotOptimized)** | ❌ | ❌ | ❌ | **UNVERIFIED** end-to-end |
| **Live read against real controller** | — | — | ❌ | **UNVERIFIED** (Tier-2 open) |
| **Live create→update→delete + no-op second `up`** | — | — | ❌ | **UNVERIFIED** (Tier-2 open) |
| Python SDK importable / classes typed | ❌ | — | — | **ABSENT** (docs claim it; no test) |
| TS / Go / .NET SDK generation | ❌ | — | — | Not generated at all |
| `firstEnv` env fallback precedence | ✅ | — | — | Covered |
| `injectInsecureTransport` helper | ✅ | — | — | Covered |
| `PluginDownloadURL` resolves / plugin auto-installs | ❌ | — | ❌ | **ABSENT** (Phase 5) |
| **CI runs any of the above on each change** | ❌ | ❌ | ❌ | **NO CI EXISTS** |

---

## Severity-ordered gaps

### CRITICAL

**C1 — No CI: none of these tests run on any change.**
*Location:* `.github/` absent (confirmed: "NO .github DIR"); Phase 5 not started.
*Not covered:* nothing is enforced automatically. The default gate (`make test`), the mock tier, and
determinism all depend on a human running them locally.
*Regression it lets through:* literally any regression — a contributor (or a `pulschema`/framework
dependency bump via `go mod tidy`) can break codegen, the wire contract, or determinism and nothing
flags it. Given the whole project is "regenerate from upstream," the absence of an automated gate is
the single largest risk.
*Concrete fix:* add `.github/workflows/ci.yml`: a `make test` job (fetches spec, runs the unit gate +
determinism), a `make test-mock` job (Docker), and `go vet` / `gofmt -l`. Make these required checks.
Add the four-artifact determinism comparison (C2) as a CI step even before extending the Go test.

**C2 — Determinism gate covers 2 of 4 artifacts.**
*Location:* `provider/pkg/gen/gen_test.go:68` `TestPipelineDeterministic` — compares only `schema1/2`
and `meta1/2`. `runPipeline` (line 41) never marshals `openapi_generated.yml`; the SDK is never
generated in-test.
*Not covered:* `openapi_generated.yml` (the doc the plugin `//go:embed`s and the mock serves) and
`sdk/python/`. BUILD-PLAN Phase 5 explicitly says the gate "must cover all four artifacts."
*Regression it lets through:* a `pulschema`/framework bump (or spec bump) that introduces map-iteration
nondeterminism in the OpenAPI doc or SDK emission would yield a dirty `git diff` on every regenerate —
the exact failure the gate exists to prevent — while the test stays green.
*Concrete fix:* extend `runPipeline` to return the marshaled `updatedOpenAPIDoc` and compare it across
runs. For the SDK, add a CI step that runs `make generate` twice and `git diff --exit-code` over
`sdk/` (cheaper than in-process gen-sdk in a Go test).

**C3 — No token-set / exclusion drift guard (spec-bump silent surface change).**
*Location:* absent. No test references `excludedPaths` from `provider/pkg/gen/schema.go:29`; no test
asserts the integration endpoint exists or that counts match (confirmed: "NO count assertions").
BUILD-PLAN Phase 5 lists this as a TODO.
*Not covered:* (a) every `excludedPaths` entry still matches a live path in the sanitized spec — a
renamed/removed upstream path leaves a **dangling exclusion that silently stops excluding**, promoting
a junk RPC endpoint to a resource; (b) the resource/function token set is the reviewed one — a bump
that adds, drops, or renames tokens (e.g. auto-promotes a data source to a resource, or a discriminator
variant disappears) passes silently; (c) the integration endpoint still exists.
*Regression it lets through:* the headline scenario in the brief — "a spec bump that silently changes
40 tokens would pass all tests." Because nothing generated is committed, there is no diff to review.
This is the most dangerous gap for a regenerable provider.
*Concrete fix:* add `provider/pkg/gen/drift_test.go` (no Docker): assert (1) for every `p` in
`excludedPaths`, `doc.Paths.Find(p) != nil` after sanitize+fix (fail loud on dangle); (2) a sorted
snapshot of resource + function tokens equals a committed `testdata/tokens.txt` (the *one* golden file
that is cheap, reviewable, and exactly the human-sign-off gate the docs want); (3) a chosen integration
token (e.g. `unifi:sites/v1:getWifiBroadcastPage`) is present. Update `tokens.txt` deliberately on a
bump → converts silent surface drift into a reviewed PR diff.

### HIGH

**H1 — Discriminated-variant CRUD is never exercised end-to-end.**
*Location:* `provider/pkg/provider/writepath_integration_test.go:18-30, 48` — deliberately uses
`FirewallZone` (flat body) "instead of" a discriminated resource because the discriminated bodies
"cannot be made Prism-valid without empirical iteration." Live Tier-2 (the only place this could run)
is unimplemented.
*Not covered:* the actual `Standard`/`IotOptimized`/`Gateway`/`ARecord`/… write path — i.e. the very
resources B1 was built to repair. The *metadata* binding is unit-tested (`crudmap_test.go`), but no
test sends a discriminated body over the wire and confirms the framework + coalesced crudMap dispatch
C→R→U→D correctly (e.g. that the discriminator field is placed in the body, that PUT-as-update works
on the variant, that Read maps back to the variant token).
*Regression it lets through:* a framework bump that changes how discriminated bodies serialize, or a
coalescing regression that binds the wrong item path, would not surface until a user runs `pulumi up`
against a real SSID. This is the project's hardest surface and its weakest end-to-end coverage.
*Concrete fix:* short term, add a Prism write-dispatch case for `Standard` (wifi/broadcasts) with a
hand-iterated spec-valid body — the test file itself notes this "would be a natural addition." Long
term this is the Tier-2 SSID round-trip (already on the checklist). At minimum add a unit test that
marshals a `Standard` input through the schema and asserts the discriminator field lands in the request
body shape.

**H2 — `OnPostInvoke` (the real paginating method) is never run across a page boundary.**
*Location:* the pure helper `aggregatePages` is well-tested (`pagination_test.go`), but
`OnPostInvoke` (`provider/pkg/provider/provider.go:173`) — which resolves the read path from
`metadata.ResourceCRUDMap`, builds the follow-up GET via `handler.CreateGetRequest`, sets
`offset`/`limit`, executes over the real client, and decodes — is exercised only with **already-complete
single-page** envelopes. `TestWirePath:64` returns `totalCount:0` so the loop never iterates; the mock
read tests hit Prism, whose examples "don't chain" (README §Prism limits), so no second page is ever
fetched. The `fetch` closure (lines 200-224: query-param wiring, non-200 handling, decode) is entirely
unexercised.
*Not covered:* multi-page assembly *through the framework's request builder and HTTP client*; the
non-200 error branch; the readPath-resolution-from-token logic; the "no readPath / handler nil →
passthrough" branch; the non-page-shape early returns at the method level (only `toSlice`/`toInt` are
unit-tested in isolation).
*Regression it lets through:* a framework bump that changes `CreateGetRequest`'s signature/behavior,
or a crudMap-shape change, breaks live pagination silently — large collections quietly return only
page 1 (the exact "decodes cleanly but is wrong" bug aggregation was added to fix), and no test fails.
*Concrete fix:* an `httptest` server (like `TestWirePath`) that serves a 3-page collection and a
`getNetworksOverviewPage`-style token; assert all rows return and that offset/limit query params were
sent on each follow-up. Add a case where page 2 returns 500 and assert the error propagates.

**H3 — Python SDK has no test, not even `import`.**
*Location:* `sdk/python/` is generated but has zero `test_*.py` (confirmed). BUILD-PLAN Verification
checklist line 165 claims "`import pulumi_unifi` works; data-source classes present and typed" — there
is no automated assertion of this.
*Not covered:* that the generated SDK is importable, that the documented resource/data-source classes
exist and carry the expected typed inputs, that a discriminated variant class (`Standard`) is present.
TS/Go/.NET SDKs are not generated at all (acceptable per "Python first," but worth noting for the
multi-language claim in DESIGN §2).
*Regression it lets through:* a `gen-sdk` or schema-shape regression that emits a broken/uninstallable
SDK, or drops a class, ships unnoticed — the consumer (`iac`) is the first to find out.
*Concrete fix:* add `sdk/python/tests/test_smoke.py` run in CI after `make python_sdk`: install the
SDK into the venv, `import pulumi_unifi`, and assert a stable set of classes exist (e.g.
`pulumi_unifi.sites.v1.Standard`, a known data source). Pair with the token golden (C3) so the same
bump that changes the surface updates both.

### MEDIUM

**M1 — No golden baseline → "deterministic" is not "correct."**
*Location:* by design — nothing generated is committed (DESIGN §3). `TestPipelineDeterministic` proves
run-to-run equality, never *equality to a reviewed output*.
*Not covered:* whether the generated schema/metadata is what a human approved. Two identical-but-wrong
runs pass. This is the conceptual root of C2/C3.
*Regression it lets through:* any systematic-but-stable change (a fix layer that now mangles every
description; a type translation that flips). The token golden in C3 is the minimal mitigation; a fuller
mitigation is a committed `schema.json` snapshot diffed in CI (heavier, but the strongest guard).
*Concrete fix:* start with the C3 token golden; consider a committed `testdata/schema.golden.json`
compared in a CI step (regenerate, `git diff`), updated deliberately on bumps.

**M2 — Write-path mock is dispatch-and-parse only (Prism is stateless).**
*Location:* `writepath_integration_test.go:14-17` — every Create returns the same example `id`;
Update/Delete parse a 200 without mutating state. The mock cannot prove a real round-trip, idempotent
second `up`, or that Update actually changed the resource.
*Not covered:* state persistence, drift detection, the no-op-second-`up` invariant (checklist line 171,
unchecked).
*Regression it lets through:* a diff/Read regression that produces perpetual diffs or a no-op update.
*Concrete fix:* this is fundamentally the Tier-2 live test; document it as the gating item and ensure
the e2e harness, once provisioned, asserts a second `up` is a no-op.

**M3 — CA-pinned secure TLS path is wholly unverified.**
*Location:* every integration/wire test sets `allowInsecure=true` (read/write integration tests,
`TestWirePath`). The `allowInsecure=false` + OS-trust path is explicitly deferred to Tier-2
(`readpath_integration_test.go:66-71`), which is unimplemented.
*Not covered:* that a real CA-trusted handshake succeeds with verification *on* — the production
default. A regression that breaks the secure path (e.g. the insecure injection leaking into the default
path) would only be caught by the missing e2e.
*Concrete fix:* a no-Docker `httptest` test using a locally-generated CA installed via `SSL_CERT_FILE`
on Linux (skip on darwin, which ignores it — document why) with `allowInsecure=false`, asserting the
handshake succeeds and the transport is *not* the insecure one. Partial, but better than zero.

**M4 — e2e README is stale and contradicts shipped behavior.**
*Location:* `test/e2e/README.md:32-33` states "The provider framework has **no insecure hook**
(`allowInsecure` is unimplemented for the MVP)" and prescribes `SSL_CERT_FILE`. `allowInsecure` is now
implemented (provider.go:141, design §6). Stale test docs cause the e2e to be stood up against the
wrong assumption.
*Not covered:* N/A (doc bug), but it will mislead whoever implements Tier-2.
*Concrete fix:* update the README to reflect that `allowInsecure=true` exists; clarify the e2e's
purpose is specifically the `allowInsecure=false` CA-pinned path (M3), which is its unique value.

### LOW

**L1 — `make test-mock` teardown is fragile (already a known Phase-5 item).**
*Location:* `Makefile` `test-mock` target — `docker compose up --wait` runs *before* the
trap-less block; a bring-up failure leaves the stack up. The down is welded to the test command's exit,
not bring-up. BUILD-PLAN Phase 5 flags this.
*Regression it lets through:* not a product regression, but flaky/dirty local + CI runs that mask real
failures. *Fix:* wrap bring-up+run+down in one block with a `trap … EXIT`.

**L2 — Spec-absence skips, not fails (acceptable but worth a CI guard).**
*Location:* `gen_test.go:33` and `readpath_integration_test.go`/`wirepath_test.go` `readArtifact`
skip when the spec/artifacts are missing. Correct for local dev, but in CI a misconfigured fetch would
turn the entire determinism + wire suite into silent skips that report green.
*Fix:* in CI, run `make test` *after* a confirmed `make build`, and add a CI assertion that the spec
file exists (or treat skips as failures via `-run` + an explicit presence check).

**L3 — `aggregatePages` "server lies about totalCount" is covered; one residual edge is not.**
*Location:* `pagination_test.go` covers empty-page terminator, missing totalCount, fetch error, and a
lying totalCount that under-fills. Not covered: a server returning **more** rows than `totalCount`
(over-fill) — the loop breaks on `len(all) >= total` so extra rows on the *first* page are fine, but a
follow-up page that overshoots is untested. Minor; current logic handles it, but a regression wouldn't
be caught. *Fix:* add a case where a follow-up page returns more rows than needed and assert no
truncation/duplication.

---

## Notes on test quality (the good, and the tautology check)

- **`crudmap_test.go` is the model to emulate.** `TestDiscriminatedResourcesHaveFullCRUD` re-derives
  the sibling-path relationship (`isChildItemPath`) independently of the code under test and pins the
  variant tokens by name — it catches both a silent variant drop *and* a wrong item-path binding. Not
  tautological.
- **`provider_test.go` table tests** (`TestOnConfigureRejectsURLApiHost`) assert behavior (accept/reject)
  with good negative cases. Solid.
- **`TestOnConfigureAllowInsecureDefaultsOff`** is a real no-regression guard (asserts the transport
  pointer is *unchanged* and not a plain `*http.Transport`) — exactly the kind of guard the suite
  needs more of.
- **Mild tautology risk:** `TestReadPathAgainstMock` asserts only `len(out) != 0`. A response that
  decodes to a non-empty-but-wrong shape passes. It proves "something came back over TLS," not "the
  right thing." Acceptable as a wiring smoke given the unit layer covers shape, but tightening it to
  assert an expected key would raise its value.
- **`TestWirePath`'s page stub** (`totalCount:0`) is a deliberate shortcut that, as noted in H2, means
  the wire test never proves pagination over the wire — it proves auth + siteId only. That's fine for
  its stated purpose but should not be mistaken for pagination coverage.

---

## Top 5 recommended additions (highest leverage first)

1. **CI workflow** (C1) — run unit + determinism + mock on every PR; required checks. Without this,
   every other test is advisory.
2. **Token-set + exclusion drift guard** (C3) with a committed `testdata/tokens.txt` golden — the one
   cheap golden that converts silent spec-bump surface drift into a reviewed diff.
3. **Extend `TestPipelineDeterministic` to all four artifacts** (C2) — close the OpenAPI-doc and SDK
   determinism holes the docs already mandate.
4. **`OnPostInvoke` multi-page test via `httptest`** (H2) — exercise the real paginating path,
   including the follow-up GET wiring and the non-200 branch.
5. **Discriminated-variant write coverage** (H1) — at minimum a `Standard` Prism dispatch case + a
   body-shape unit test; ultimately the Tier-2 SSID round-trip.
