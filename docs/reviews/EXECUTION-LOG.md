# EXECUTION-LOG

Per-ticket execution log for the WORK.md backlog, branch `execute/foundation`.
One line per ticket: ID, status, commit sha, verification result, notes/deviations.

Environment: Go 1.26.4 (`/usr/local/go/bin`, prefixed on PATH), Pulumi v3.242.0,
Python 3.9.6. Docker daemon UNHEALTHY → `make test-mock` (Tier-1 mock) deferred.

Verification bar: `make build` + `make test` (no Docker) green. Pure refactors
(S0.1, S0.2) additionally gated byte-identical on the three generated artifacts.

## Log

- baseline | committed | snapshot of working tree before refactor execution (bc508df).
- S0.1 + B-M1.2 + pkg/errors sweep | ☑ | 23da5ed | split pkg/provider into provider.go (assembly/lifecycle) + config.go + auth.go + pagination.go + transport.go; moved framework handle onto unifiProvider.handler field and deleted both package globals (handler/callback) + the test save/restore dance; swept github.com/pkg/errors → stdlib errors/fmt.Errorf across provider+gen entrypoint+pagination_test, dropping it from direct go.mod requires (now indirect). Verified: make build + make test green; `go test -race ./...` green (B-M1.2 reentrancy/no-race); all 3 generated artifacts BYTE-IDENTICAL to baseline (behavior-neutral gate). Also gofmt -w'd two pre-existing non-conforming files (spec_sanitize.go, crudmap_test.go) — confirmed still byte-identical output.
- S0.2 | ☑ | 32d4b73 | added genstate.go (GenState{*Pkg,*Meta,*Doc} + pass type) and an ordered passes slice + runPasses() invoked in PulumiSchema; migrated coalesceDiscriminatedCRUD (+findItemPath/isSinglePathParamSegment/strPtr) into pass_coalesce_crud.go as pass #1 (coalesceDiscriminatedCRUDPass) taking *GenState (retires 02-L3 by-value copy); findItemPath now takes *openapi3.T. sortedKeys stays in schema.go (shared). Added pass_coalesce_crud_test.go (3 focused unit tests: Phase-1 item-verb fill incl. no-overwrite, Phase-2 orphan prune). Verified: all 3 artifacts BYTE-IDENTICAL; race/vet/gofmt clean; guard sanity-checked RED by skipping R-fill then reverted.
- S0.3 + QW-2 + QW-3 + QW-4 | ☑ | 80840d1 | moved static PackageSpec literal + excludedPaths + packageName into packagespec.go; schema.go now orchestration-only (calls packageSpec(), runs pulschema, runs passes). Single-sourced config descriptions via configKeys map → configVariables()/providerInputProperties() (QW-2; the only delta is DefaultInfo.Environment). QW-3: dropped duplicate "unifi" keyword. QW-4: added pass_secret_fields.go (markSecretFieldsPass) marking HotspotVoucherDetails.code secret — loud-on-drift if the type/prop token is absent. OUTPUT-AFFECTING (intended): schema.json diff = {dropped "unifi" keyword, voucher code secret:true, 3 InputProperties descriptions unified to the richer text}; metadata.json + openapi_generated.yml unchanged. Determinism (TestPipelineDeterministic) green; race/vet/gofmt clean; secret-fields guard sanity-checked RED on a bad token then reverted. This was the last output-affecting Wave-0 change → A-M0.2 baselines next.
- A-M0.2 | ☑ | f50654f | committed provider/pkg/gen/testdata/tokens.txt golden (71 tokens = 21 resource + 50 function, kind-prefixed) baselined AFTER S0.3/QW-3/QW-4; added drift_test.go (no Docker) — TestTokenSetMatchesGolden regenerates the typed pipeline token set and diffs vs golden with a readable added/removed report; `UPDATE_GOLDEN=1` rebases it (deliberate reviewed step). Guard sanity-checked RED (junk golden line → diff reported) then restored. make test green.

### Wave 1

- A-M0.3 | ☑ | 2f48500 | added two guards to drift_test.go: TestExcludedPathsResolve (every excludedPaths entry resolves against the sanitized+fixed doc via doc.Paths.Find — dead-exclusion guard) and TestNoDuplicateShortTokenNames (no duplicate short names within the resource or function token set — variant-collision guard). excludedPaths guard sanity-checked RED with a bogus entry then reverted; dup-detection logic verified RED on a synthetic collision. make test green.
- A-M0.4 | ☑ | a1241cc | extended runPipeline + TestPipelineDeterministic to the third in-process artifact (openapi_generated.yml via yaml.Marshal of the pulschema-updated doc), so nondeterminism in the OpenAPI doc now fails the default gate too. The fourth artifact (Python SDK, out-of-process gen-sdk) is covered by the CI double-generate + git-diff step landing in A-M0.1. Determinism green.
- A-M0.5 + A-M0.6 | ☑ | 84eed0d | created openapi/pin.env as the single source of truth (SPEC_REPO/SHA/VERSION/SHA256), sourced by fetch.sh and `include`d by the Makefile (SPEC derives from $(SPEC_VERSION)); force-tracked past the *.env gitignore rule. Go side: gen.PinnedSpecVersion + gen.SpecFileName() — used by main.go's -spec default and gen_test.go findSpec (no more hardcoded "10.4.57" in tests/main.go; only pin.env + the Go mirror remain, asserted equal). A-M0.5: main.go panics if fetched info.version != PinnedSpecVersion; mirrored as TestSpecInfoVersionMatchesPin. A-M0.6: findSpec now t.Fatalf (not t.Skipf) on a missing spec when CI is set; TestPinnedSpecVersionMatchesPinEnv fails a half-finished bump. Verified: build byte-identical; fetch.sh re-runs + checksum OK; both guards sanity-checked RED on a wrong version then reverted; make test green.

## CHECKPOINT — Wave 0 → Wave 1 boundary

The delicate seam work is landed and the safety net is in place. Summary of what
landed in Wave 0 + the A-M0.2 baseline:

- **Provider runtime split (S0.1 + B-M1.2):** `pkg/provider` is now one cohesive
  package across 5 responsibility-aligned files; the framework handle lives on
  the struct (no package globals), so the provider is reentrant and `-race` clean.
- **Gen pass pipeline (S0.2):** `GenState{*Pkg,*Meta,*Doc}` + ordered `passes`
  slice run by `runPasses`; `coalesceDiscriminatedCRUD` migrated to
  `pass_coalesce_crud.go`. Track D fans out by adding `pass_*.go` files + one
  registration line.
- **Identity/orchestration split + config single-sourcing (S0.3, QW-2/3/4):**
  static identity in `packagespec.go`; `schema.go` is orchestration-only; config
  descriptions derive from one map; voucher `code` is secret.
- **Token golden (A-M0.2):** `testdata/tokens.txt` committed; `drift_test.go`
  guards it. This is the spec-bump safety net the rest of the plan depends on.

**Determinism is GREEN.** `TestPipelineDeterministic` passes; the pure refactors
(S0.1, S0.2) were each verified byte-identical on schema.json/metadata.json/
openapi_generated.yml; the one output-affecting change (S0.3+QW-3/4) produced
exactly the three intended schema.json deltas and nothing else, and was then made
the golden baseline. `make build` + `make test` + `go test -race` + `go vet` +
`gofmt -l` all clean.
- A-M0.7 | ☑ | 8f82cc4 | coalesceDiscriminatedCRUDPass now returns a loud error when a managed resource is still create-only after coalescing (m.R == nil → no resolvable item path), instead of silently shipping a stub that dies on the next `pulumi up`. runPasses surfaces it via contract.Failf. TestResourceCRUDBindsItemLevel keeps asserting R != nil for every resource unconditionally. Added TestCoalescePassErrorsOnUnresolvableRead (guard fires) + fixed TestCoalescePassPrunesOrphans to give its resource a resolvable item path. Output byte-identical (guard-only); all 21 real resources resolve R (gen tests green).
- A-M0.8 | ☑ | 7f6368d | added a one-line-block rationale comment per untagged v0.0.0-<sha> direct dep in go.mod (pulschema + pulumi-provider-framework: what each provides, what to re-verify on bump, the relevant upstream PR). Added TestCoalesceStillNeeded (runPulschemaRaw runs GatherResourcesFromAPI WITHOUT our passes) asserting raw pulschema still emits create-only discriminated stubs (observed 18) — so an upstream full-CRUD-binding fix (G-U1) flags the coalesce pass as now-redundant. build/vet green.
- B-M1.1 | ☑ | 0859a55 | aggregatePages now takes ctx and is bounded three ways (pagination.go): per-iteration ctx.Err() (cancel/deadline aborts the aggregate), a page ceiling (total/listPageLimit+2 when totalCount is known, else maxPagesFallback=10000), so a controller that ignores offset / never empties cannot hang or OOM `pulumi up`. Added TestAggregatePagesKnownTotalCeiling, TestAggregatePagesNoTotalCeiling (asserts exactly maxPagesFallback fetches), TestAggregatePagesContextCancelled; all 5 existing aggregate tests updated to pass context.Background(). Each new guard sanity-checked RED-when-removed (ctx-cancel → nil err; no-total ceiling removed → 6s timeout panic) then reverted. provider tests/vet/gofmt green.
- B-M1.3 (remainder) | ☑ | 0a9bd6d | pkg/errors→stdlib already rode S0.1. Remaining hygiene: (1) io.LimitReader(resp.Body, 4<<10) on the paginated-error body (pagination.go) — bounds an oversized/MITM error body; (2) log (not swallow) an unparseable non-empty allowInsecure (config.go) — fail-secure but visible; (3) extracted p.configValue() helper collapsing the 4× var-then-env dance, all four config keys now resolve uniformly (config.go); (4) interface{}→any across pagination.go + provider test files. Output byte-identical; race/vet/gofmt clean.
- C-M1.4 + C-M1.5 | ☑ | afe912a | C-M1.4 loud-on-changed-assumptions guards (openapi_fixes.go + pass_coalesce_crud.go): injectAPIKeySecurityScheme now errors if the spec already declares securitySchemes or top-level security (would collide); rewriteServerURL errors if the incoming server != expected relative "/integration" (stale base path → 404); findItemPath now returns (string,error) and errors on >1 single-{param} sibling (ambiguous CRUD binding) instead of sorted-first. FixOpenAPIDoc/injectAPIKeySecurityScheme/rewriteServerURL now return errors. C-M1.5: FixOpenAPIDoc runs openAPIDoc.Validate(ctx, DisableExamplesValidation()) after the fix layer so dangling/circular $refs surface with a named location before pulschema's opaque panic (verified Validate catches a dangling ref). Added 3 guard tests (existing-scheme/security, unexpected server, item-path ambiguity). Output byte-identical; race/vet/gofmt green.
- E-M4.4 | ☑ | d8c1f18 | added pagination_integration_test.go (no Docker, httptest TLS, default gate): TestOnPostInvokeAggregatesPages drives a real getWifiBroadcastPage Invoke through provider+framework against a server serving a 3-page collection (407 rows) — asserts all rows assembled, GET offsets [0,200,400] (framework read + 2 OnPostInvoke follow-ups), follow-up limit=listPageLimit. TestOnPostInvokePropagatesPageError asserts a page-2 500 surfaces as an Invoke error/failure (not silent truncation). Aggregation guard sanity-checked RED (ceiling=0 → 200 rows) then reverted. Found+documented: the framework's own first GET carries offset=0 & its default limit=25; OnPostInvoke's follow-ups carry limit=200. provider tests green.
- E-M4.3 (no-Docker part) | ◐ | 3a2f942 | added securetls_test.go: TestSecureTLSPathTrustsCAviaSSLCertFile points SSL_CERT_FILE at the httptest server's own cert and configures with allowInsecure UNSET (verification ON), asserting the CA-trusted handshake succeeds AND the framework transport was NOT replaced by the insecure *http.Transport. Platform-gated: SKIPS on non-linux (Go honors SSL_CERT_FILE only on linux; darwin uses the keychain — documented). DARWIN CAVEAT: the active assertion path could not be executed here (skipped on this darwin host); it compiles + vets clean and will run in Linux CI. The live keychain-trust path remains the Tier-2/E-M4.1 concern → ticket left ◐ (partial), not fully ☑.
- E-M4.5 | ☑ | 26c7ebf | added test/sdk/test_smoke.py (committed OUTSIDE the gitignored sdk/ tree that python_sdk wipes): pytest.importorskip(pulumi_unifi) then asserts a stable class set on pulumi_unifi.sites.v1 — discriminated variants Standard/IotOptimized, flat Gateway/FirewallZone/FirewallPolicy/ARecord, data-source fns get_firewall_zone/get_wifi_broadcast_page, Standard issubclass pulumi.CustomResource, and top-level countries.v1.get_countrie. Added `make test-sdk` (python_sdk → throwaway .sdkvenv → pip install ./sdk/python + pulumi + pytest → run); gitignored .sdkvenv. Verified: 11/11 pass against the real installed SDK; guard sanity-checked RED on a bogus expected class then reverted; artifacts byte-identical after the regen.
- F-M5.4 | ☑ | 8e036b6 | rewrote the test-mock recipe as a single shell block: `set -e; trap 'docker compose ... down' EXIT; up --wait; <go test>`. Teardown is now unconditional even when `up --wait` itself fails (previously the down was welded to the test command's exit, so a bring-up failure left the stack up). DOCKER CAVEAT: daemon UNHEALTHY here so the recipe was not executed; verified via `make -n test-mock` expansion + `bash -n` syntax check of the trap block. Full run deferred until Docker is healthy.
- QW-1 + QW-5 + QW-6 | ☑ | e038a65 | QW-1: replaced version.go's blanket `// nolint: revive` with a real package doc (build/vet/gofmt clean). QW-5: DESIGN §7 now accurately describes the github:// PluginDownloadURL scheme (Pulumi computes the asset name; no ${OS}/${ARCH} template); DESIGN §6 + openapi/SOURCE corrected — the apiKey config property is hand-authored in packagespec.go, not emitted by pulschema from the scheme (the scheme only names the auth header). QW-6: test/e2e/README.md TLS section rewritten — allowInsecure is implemented (Phase 4); the e2e's unique value is the CA-pinned allowInsecure=false path, with securetls_test.go as the no-Docker partial. Docs/comment-only; no code behavior change.
- A-M0.1 | ☑ | 722ec37 | added .github/workflows/ci.yml with 4 jobs: (test) gofmt -l + go vet + `make test` with CI=true so codegen tests fail-not-skip on a missing spec; (determinism) `make generate` twice + `diff -r` over sdk/ — the out-of-process SDK half of A-M0.4; (sdk-smoke) `make test-sdk` (E-M4.5); (test-mock) Dockerized `make test-mock`. Pins Go 1.26, uses pulumi/actions + setup-python where gen-sdk is needed. YAML validated; CI=true `make test` green locally; SDK verified deterministic across two generate runs (the determinism job's exact check); `sdk/` layout confirmed for the `mv sdk` step. NOT executed on a live runner (no GitHub Actions here) — workflow logic validated locally. Mark test/determinism/sdk-smoke as required checks once the repo is on GitHub.

## FINAL — Wave 0 + Wave 1 complete; STOP at the Track-D decision gate

All in-scope tickets landed on branch `execute/foundation` (21 commits ahead of
main). Final verification: `make build` OK; `make test` + `go test -race ./...`
green (121 test funcs, all passing); `go vet` + `gofmt -l` clean; the three
embedded artifacts byte-identical to the post-S0.3 baseline; SDK deterministic
across two `make generate` runs (the earlier one-off NON-DETERMINISTIC reading
was a copy-during-regeneration race in the manual check, not a real defect —
re-verified clean with sequential copies, and TestPipelineDeterministic covers
the three Go-side artifacts in-process).

Done: S0.1, S0.2, S0.3, QW-2/3/4, A-M0.2, A-M0.3, A-M0.4, A-M0.5, A-M0.6,
A-M0.7, A-M0.8, A-M0.1, B-M1.2, B-M1.1, B-M1.3, C-M1.4, C-M1.5, E-M4.4, E-M4.5,
F-M5.4, QW-1, QW-5, QW-6. Partial: E-M4.3 (no-Docker httptest part; skips on this
darwin host, runs in Linux CI; live keychain path is Tier-2).

STOPPED (left for human decision / out of scope, per the brief):
- Track D (D-M2.*/D-M3.*) — gated on the per-variant-resources vs tagged-union
  DECISION in 00-SYNTHESIS "Open question". This is the human checkpoint.
- B-M1.6 (Wave 2), S0.4 (deferred), E-M4.1/E-M4.2 (live UniFi controller),
  F-M5.1/5.2/5.3 (release), Track G (external repos), A-M0.2′ (re-baseline post-D).
- `make test-mock` — Docker daemon UNHEALTHY in this environment; the trap fix
  (F-M5.4) and the mock-tier smoke run are deferred until Docker is healthy.
