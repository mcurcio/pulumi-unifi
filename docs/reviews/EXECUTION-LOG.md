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
- A-M0.7 | ☑ | <am07sha> | coalesceDiscriminatedCRUDPass now returns a loud error when a managed resource is still create-only after coalescing (m.R == nil → no resolvable item path), instead of silently shipping a stub that dies on the next `pulumi up`. runPasses surfaces it via contract.Failf. TestResourceCRUDBindsItemLevel keeps asserting R != nil for every resource unconditionally. Added TestCoalescePassErrorsOnUnresolvableRead (guard fires) + fixed TestCoalescePassPrunesOrphans to give its resource a resolvable item path. Output byte-identical (guard-only); all 21 real resources resolve R (gen tests green).
