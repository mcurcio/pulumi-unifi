# EXECUTION-LOG

Per-ticket execution log for the WORK.md backlog, branch `execute/foundation`.
One line per ticket: ID, status, commit sha, verification result, notes/deviations.

Environment: Go 1.26.4 (`/usr/local/go/bin`, prefixed on PATH), Pulumi v3.242.0,
Python 3.9.6. Docker daemon UNHEALTHY → `make test-mock` (Tier-1 mock) deferred.

Verification bar: `make build` + `make test` (no Docker) green. Pure refactors
(S0.1, S0.2) additionally gated byte-identical on the three generated artifacts.

## Log

- baseline | snapshot of working tree before refactor execution | committed
- S0.1 + B-M1.2 + pkg/errors sweep | ☑ | 23da5ed | split pkg/provider into provider.go (assembly/lifecycle) + config.go + auth.go + pagination.go + transport.go; moved framework handle onto unifiProvider.handler field and deleted both package globals (handler/callback) + the test save/restore dance; swept github.com/pkg/errors → stdlib errors/fmt.Errorf across provider+gen entrypoint+pagination_test, dropping it from direct go.mod requires (now indirect). Verified: make build + make test green; `go test -race ./...` green (B-M1.2 reentrancy/no-race); all 3 generated artifacts BYTE-IDENTICAL to baseline (behavior-neutral gate). Also gofmt -w'd two pre-existing non-conforming files (spec_sanitize.go, crudmap_test.go) — confirmed still byte-identical output.
- S0.3 + QW-2 + QW-3 + QW-4 | ☑ | <next> | moved static PackageSpec literal + excludedPaths + packageName into packagespec.go; schema.go now orchestration-only (calls packageSpec(), runs pulschema, runs passes). Single-sourced config descriptions via configKeys map → configVariables()/providerInputProperties() (QW-2; the only delta is DefaultInfo.Environment). QW-3: dropped duplicate "unifi" keyword. QW-4: added pass_secret_fields.go (markSecretFieldsPass) marking HotspotVoucherDetails.code secret — loud-on-drift if the type/prop token is absent. OUTPUT-AFFECTING (intended): schema.json diff = {dropped "unifi" keyword, voucher code secret:true, 3 InputProperties descriptions unified to the richer text}; metadata.json + openapi_generated.yml unchanged. Determinism (TestPipelineDeterministic) green; race/vet/gofmt clean; secret-fields guard sanity-checked RED on a bad token then reverted. This was the last output-affecting Wave-0 change → A-M0.2 baselines next.
- S0.2 | ☑ | 32d4b73 | added genstate.go (GenState{*Pkg,*Meta,*Doc} + pass type) and an ordered passes slice + runPasses() invoked in PulumiSchema; migrated coalesceDiscriminatedCRUD (+findItemPath/isSinglePathParamSegment/strPtr) into pass_coalesce_crud.go as pass #1 (coalesceDiscriminatedCRUDPass) taking *GenState (retires 02-L3 by-value copy); findItemPath now takes *openapi3.T. sortedKeys stays in schema.go (shared). Added pass_coalesce_crud_test.go (3 focused unit tests: Phase-1 item-verb fill incl. no-overwrite, Phase-2 orphan prune). Verified: all 3 artifacts BYTE-IDENTICAL; race/vet/gofmt clean; guard sanity-checked RED by skipping R-fill then reverted.
</content>
</invoke>
