# Go Best-Practices Review — pulumi-unifi hand-written Go

Reviewer: adversarial Go reviewer (idioms, error handling, concurrency, IO/resource
correctness, library/API design).
Scope: only the hand-written glue Go. Generated output and the third-party `pulschema` /
`pulumi-provider-framework` packages are out of scope except where this code's contract
with them is relevant.

Files reviewed:
- `provider/pkg/provider/provider.go`
- `provider/pkg/provider/serve.go`
- `provider/pkg/gen/schema.go`
- `provider/pkg/gen/openapi_fixes.go`
- `provider/pkg/gen/spec_sanitize.go`
- `provider/cmd/pulumi-gen-unifi/main.go`
- `provider/cmd/pulumi-resource-unifi/main.go`
- `provider/pkg/version/version.go`
- `provider/go.mod`

> Note: Go was not installed in the review sandbox and the module cache for the two
> untagged `cloudy-sky-software` deps was not present, so I could not run `go vet` /
> `go build` / inspect the framework source directly. Findings about the framework's API
> shape (e.g. `CreateGetRequest`, `GetHTTPClient`, the rate-limit transport) are inferred
> from this repo's call sites, comments, and tests; a couple are flagged as "verify".

---

## Executive summary

The hand-written surface is small, genuinely thin, and unusually well-commented. The
codegen path (`gen/*`, `cmd/pulumi-gen-unifi`) is solid: deterministic, well-tested, and
its use of `panic`/`contract.Failf` is acceptable for a build-time tool. The recursive
spec sanitizer is correct on the cases it tests and handles the nasty edges (null items,
typeless schemas, collisions-as-error).

The real risks cluster in `provider/provider.go`, in roughly this order:

1. **`OnPostInvoke` pagination loop ignores the request `context`** (High) — the
   follow-up GETs are built with `ctx` but the loop has no timeout/cancellation budget of
   its own and no page-count ceiling, so a server that keeps returning non-empty
   never-shrinking pages can loop unbounded. The empty-page terminator only catches the
   "lies about totalCount" case, not a misbehaving/looping server.
2. **Package-level mutable globals `handler` / `callback`** (Medium, but genuinely
   load-bearing) — written without synchronization in `makeProvider` and read in
   `OnConfigure`/`OnPostInvoke`. Single-instance-per-process makes this *probably* fine in
   production, but it's a real multi-instance / test-isolation hazard and the tests
   already have to save/restore the globals to stay hermetic, which is the tell.
3. **`github.com/pkg/errors` everywhere** (Medium) — archived since 2021. The stdlib
   `errors` + `fmt.Errorf("%w", …)` is the idiom now; the dependency buys nothing here.
4. **Swallowed `strconv.ParseBool` error on `allowInsecure`** (Low–Medium) — a typo'd
   `UNIFI_ALLOW_INSECURE=ture` is silently ignored and you get secure-by-default, which is
   the safe direction but hides operator error with zero feedback.

None of these block the read-path MVP. #1 and #2 are the ones worth fixing before the
write path / multi-resource concurrency lands.

---

## Findings (severity-ordered)

### HIGH

#### H1 — Pagination loop has no context budget and no hard page ceiling
`provider/pkg/provider/provider.go:200-263`

`aggregatePages` loops calling `fetch(len(all), listPageLimit)`. `fetch` builds the
request with the inbound `ctx` (`handler.CreateGetRequest(ctx, …)`), so per-request
cancellation *is* wired. But the loop itself has two gaps:

- **No iteration ceiling.** Termination relies on either `len(all) >= total` or an empty
  page (`len(rows) == 0`). A server that returns a *non-empty but non-advancing* page
  (e.g. always echoes the same N rows, or ignores `offset` and returns a full page every
  time) makes `all` grow without ever satisfying `len(all) >= total` and without ever
  hitting the empty terminator → unbounded loop and unbounded memory. The test
  `TestAggregatePagesEmptyPageTerminates` only covers the "totalCount lies, server returns
  *empty*" case; it does not cover "server returns non-empty forever."
- **No aggregate deadline.** Each GET can carry a transport timeout, but there is no
  ceiling on the *number* of GETs, so total wall-clock for one Invoke is unbounded even
  with healthy per-request timeouts.

Why it matters: this is the data-source read path for *every* list; a single
misbehaving/buggy controller endpoint hangs `pulumi up` / `pulumi preview` indefinitely
and can OOM the plugin.

Concrete fix: add a defensive page cap derived from `total` (e.g.
`maxPages := (total/listPageLimit)+2`, fall back to a constant like 10_000/listPageLimit
when `!hasTotal`) and bail with an error when exceeded; optionally detect non-advancing
pages (offset didn't grow `len(all)`):

```go
const maxPages = 1000 // 200k rows ceiling; far beyond any real UniFi collection
for i := 0; ; i++ {
    if hasTotal && len(all) >= total {
        break
    }
    if i >= maxPages {
        return nil, fmt.Errorf("pagination exceeded %d pages for a collection "+
            "reporting totalCount=%d; aborting to avoid an unbounded loop", maxPages, total)
    }
    before := len(all)
    page, err := fetch(len(all), listPageLimit)
    if err != nil {
        return nil, err
    }
    rows := toSlice(page["data"])
    if len(rows) == 0 {
        break
    }
    all = append(all, rows...)
    if len(all) == before { // server ignored offset / returned dup-only page
        break
    }
}
```

Also worth checking `ctx.Err()` at the top of each iteration to short-circuit a cancelled
preview promptly rather than after the in-flight GET.

---

### MEDIUM

#### M1 — Package-level mutable globals `handler` and `callback`, set without synchronization
`provider/pkg/provider/provider.go:55-58, 73-78, 98-99, 141-142, 188-191`

```go
var (
    handler  *fwRest.Provider
    callback fwCallback.ProviderCallback
)
```

These are written in `makeProvider` and read in `OnConfigure` (`handler.GetSchemaSpec()`,
`injectInsecureTransport(handler.GetHTTPClient())`) and `OnPostInvoke`
(`handler.CreateGetRequest`, `handler.GetHTTPClient`).

Problems:

1. **Multiple provider instances in one process clobber each other.** `makeProvider`
   overwrites both globals every call. Pulumi normally runs one plugin process per
   provider, so in production this is *probably* safe — but it's an implicit invariant the
   code doesn't state or enforce, and it makes the package non-reentrant. `callback = p`
   then `handler = rp.(*fwRest.Provider)` means the last constructed provider wins for all
   subsequently-constructed ones too.
2. **No happens-before guarantee for the read sites.** `makeProvider` runs on the plugin
   bootstrap goroutine; `OnConfigure`/`OnPostInvoke` run on gRPC handler goroutines. There
   is no mutex/atomic, so the writes-then-reads rely on the gRPC server's own start
   ordering for visibility. It works in practice (server isn't serving until Serve
   returns), but it is a data race by the memory model if any instance were ever
   reconstructed concurrently, and `go test -race` across the global-mutating tests is
   only safe because they run serially.
3. **Testability smell.** `provider_test.go` / `wirepath_test.go` must
   `prevHandler, prevCallback := handler, callback` and restore on cleanup. Needing to
   save/restore package state to keep tests hermetic is the canonical sign the state
   should be instance-scoped.

Why it's only Medium: the *real-world* single-instance assumption holds for a Pulumi
plugin, and `OnConfigure` even guards `handler != nil` for unit-testability. But this is
the most architecturally fragile thing in the file.

Concrete fix: store the framework handle on the `unifiProvider` struct instead of
globals. The callback already *is* the `unifiProvider`; give it a field:

```go
type unifiProvider struct {
    fwCallback.UnimplementedProviderCallback
    ...
    handler *fwRest.Provider // set after fwRest.MakeProvider returns
}
```

`makeProvider` sets `p.handler = rp.(*fwRest.Provider)`; the callbacks already have a `p`
receiver, so every read site becomes `p.handler`. `callback` the global disappears
entirely (it's only passed to `MakeProvider`, which can take `p` directly). This removes
both the race and the multi-instance hazard, and lets tests construct independent
providers without touching package state.

(If there's a chicken-and-egg ordering — `MakeProvider` needs the callback before it
returns the handle — that's fine: pass `p`, then assign `p.handler` from the return value.
The handle is only *read* later, in Configure/Invoke, never during construction.)

#### M2 — `github.com/pkg/errors` is archived; use stdlib error wrapping
`provider/pkg/provider/provider.go:20, 70, 107, 128, 197, 202, 212, 217, 221`;
`provider/cmd/pulumi-gen-unifi/main.go:23, 65, 73, 79, 88, 94, 108`;
`provider/pkg/provider/pagination_test.go:6`

`github.com/pkg/errors` has been archived/read-only since 2021 and is effectively
deprecated; `errors.Wrap`/`errors.Errorf`/`errors.New` are all expressible with stdlib
since Go 1.13:

- `errors.Wrap(err, "msg")` → `fmt.Errorf("msg: %w", err)`
- `errors.Wrapf(err, "msg %s", x)` → `fmt.Errorf("msg %s: %w", x, err)`
- `errors.Errorf(...)` → `fmt.Errorf(...)`
- `errors.New(...)` → `errors.New(...)` (stdlib has it)

Note `gen/spec_sanitize.go` already does the right thing (`fmt.Errorf("…: %w", err)`), so
the codebase is inconsistent with itself. Why it matters: archived dep = no security/bug
fixes, and the project carries a transitive dependency it doesn't need. The fix is
mechanical and removes a `require` line from `go.mod`. Low risk, clear win.

#### M3 — `OnPostInvoke` page-fetch error includes the whole error body but reads it unboundedly
`provider/pkg/provider/provider.go:215-218`

```go
if resp.StatusCode != http.StatusOK {
    body, _ := io.ReadAll(resp.Body)
    return nil, errors.Errorf("paginated get %s returned %s: %s", readPath, resp.Status, string(body))
}
```

Two issues:
- `io.ReadAll` on an untrusted/erroring response with no `io.LimitReader` — a controller
  (or a MITM on the insecure path) returning a multi-MB error body gets fully buffered
  into an error string. Wrap with `io.LimitReader(resp.Body, 4<<10)`.
- The discarded `_` error from `ReadAll` is acceptable here (best-effort error context),
  but worth a comment so it doesn't read as an oversight.

#### M4 — `OnConfigure` is long and mixes config resolution with side effects; the env-fallback pattern is repeated 4×
`provider/pkg/provider/provider.go:91-151`

The four config keys each repeat the same `vars[...]` → `firstEnv(inputs[...].DefaultInfo)`
dance inline. Extract a helper:

```go
func (p *unifiProvider) configValue(vars map[string]string, inputs map[string]pschema.PropertySpec, key string) string {
    if v, ok := vars[p.name+":config:"+key]; ok && v != "" {
        return v
    }
    return firstEnv(inputs[key].DefaultInfo)
}
```

That collapses ~30 lines to 4 calls, removes the easy-to-mistype `p.name+":config:"+key`
duplication, and makes the apiKey/siteId/apiHost/allowInsecure resolution uniform (right
now `apiKey` is resolved slightly differently from `siteId` — `siteId` uses an
`else if` chain, the others use the assign-then-check pattern; uniform helper kills the
inconsistency). The side-effect (`injectInsecureTransport`) stays where it is.

---

### LOW

#### L1 — `strconv.ParseBool` error on `allowInsecure` is silently swallowed
`provider/pkg/provider/provider.go:138-140`

```go
if b, err := strconv.ParseBool(allowInsecureStr); err == nil {
    p.allowInsecure = b
}
```

An unparseable value (`UNIFI_ALLOW_INSECURE=yes`, a typo `ture`, `1.0`) is silently
treated as "leave verification on." Fail-secure is the right default, but the operator
gets *zero* signal that their setting did nothing. At minimum log at a visible level when
a non-empty value fails to parse:

```go
b, err := strconv.ParseBool(allowInsecureStr)
if err != nil && allowInsecureStr != "" {
    logging.V(3).Infof("ignoring unparseable allowInsecure=%q (%v); TLS verification stays on", allowInsecureStr, err)
}
p.allowInsecure = err == nil && b
```

Returning a hard error is also defensible (config typos shouldn't pass silently), but
logging keeps it lenient and matches the file's tone.

#### L2 — `injectInsecureTransport` hand-duplicates `http.Transport` defaults; drift risk
`provider/pkg/provider/provider.go:291-305`

The comment is honest that this mirrors the framework's inner transport. But hardcoding
`MaxIdleConns: 100`, the dial/keepalive timeouts, etc. means that if the framework (or Go
stdlib defaults) changes these, the insecure path silently diverges. Lower-risk
alternative: start from `http.DefaultTransport.(*http.Transport).Clone()` and set only
`TLSClientConfig`, so you inherit stdlib defaults and only override the one knob you mean
to:

```go
tr := http.DefaultTransport.(*http.Transport).Clone()
tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402
c.Transport = tr
```

This doesn't recover the lost 429 retry wrapper (that's the documented upstream
limitation), but it removes the manual-defaults drift and is *closer* to the framework's
real client than a hand-typed struct. Caveat: `Clone()` won't reproduce the framework's
exact tuning if it deviates from stdlib — but the current hand-copy doesn't guarantee that
either, and is strictly more brittle.

#### L3 — `PulumiSchema` returns `openapi3.T` by value (a large struct with maps/pointers)
`provider/pkg/gen/schema.go:41`; called at `cmd/pulumi-gen-unifi/main.go:82` with
`*openAPIDoc`

`func PulumiSchema(openAPIDoc openapi3.T) (..., openapi3.T)` takes and returns
`openapi3.T` by value. `openapi3.T` is a large struct containing maps, slices, and pointer
fields (`*Components`, `Paths`, etc.). Passing/returning by value copies the top-level
struct but the maps/pointers are shared — so it's *not* a defensive deep copy (a mutation
of `Doc.Components.Schemas` inside still aliases the caller's), yet it pays a struct-copy
cost and reads as if it were a copy. Either take `*openapi3.T` (idiomatic for this type;
the whole `kin-openapi` API uses pointers) or document that aliasing is intended.
`coalesceDiscriminatedCRUD(meta, updatedOpenAPIDoc, pkg)` (schema.go:145, 205) similarly
takes `doc openapi3.T` and `pkg pschema.PackageSpec` by value — `pkg` in particular is a
big struct copied just to read `.Resources`/`.Functions`; pass `*pschema.PackageSpec` or
just the two maps it actually reads.

#### L4 — `interface{}` instead of `any` throughout `provider.go`
`provider/pkg/provider/provider.go:173, 174, 200, 219, 236, 267, 274` (and test files)

The codebase mixes styles: `gen/spec_sanitize.go` uses `any` (modern, Go 1.18+);
`provider.go` uses `interface{}` (`outputs interface{}`, `map[string]interface{}`, etc.).
`any` is the preferred alias since 1.18 and the project is on Go 1.26. Purely cosmetic but
it's an easy `gofmt`-adjacent consistency pass. (The `OnPostInvoke` signature's
`interface{}` may be dictated by the framework interface — verify before changing that one;
the locals and helpers are free to switch.)

#### L5 — `contract.Assert(err == nil)` in `rawMessage` discards the actual error
`provider/pkg/gen/schema.go:167-174`

```go
func rawMessage(v interface{}) pschema.RawMessage {
    ...
    err := encoder.Encode(v)
    contract.Assert(err == nil)
    return out.Bytes()
}
```

`contract.Assert(err == nil)` panics with a generic assertion message that *drops the
underlying error*. Since `v` here is a fixed `pythongen.PackageInfo`, encode failure is
essentially impossible, so panic-vs-return is fine for a codegen tool — but use the
error-carrying form so a future caller passing un-encodable input gets a useful message:
`contract.AssertNoErrorf(err, "encoding language package info")`. (Same spirit as the
`panic(errors.Wrap(...))` pattern used elsewhere in main.go, which *does* preserve the
error.) Minor: parameter type `interface{}` → `any`.

#### L6 — `Language`/`Schema` ceremony in the gen entrypoint for a single mode
`provider/cmd/pulumi-gen-unifi/main.go:32-38, 61-99`

There is exactly one valid subcommand (`schema`); the `Language` type, the `const Schema`,
and the `switch` with a `default: panic("unrecognized language")` model a multi-language
dispatch that doesn't exist. Not wrong — it's forward-looking scaffolding mirroring the
upstream pulumi codegen entrypoint convention — but for a single mode it's over-built. If
no second mode is imminent, a positional-arg check + direct call is simpler. Leave it if
you expect to add `gen-sdk`-style modes here; flag for "is this earning its keep."

#### L7 — `version.go` has a file-level `// nolint: revive` with no explanation
`provider/pkg/version/version.go:1`

`// nolint: revive` blanket-disables revive for the whole file. The thing it's almost
certainly suppressing is "package comment should be of the form 'Package version ...'".
Prefer a real package doc comment over a blanket nolint:

```go
// Package version exposes the provider's build-time version string.
package version
```

That satisfies the linter for the right reason and documents the package. A blanket
file-level nolint hides future genuine findings in the same file.

#### L8 — `firstEnv` uses `v != ""` to mean "set", conflating empty-but-set with unset
`provider/pkg/provider/provider.go:309-319`

`os.Getenv` can't distinguish unset from empty, and `firstEnv` treats empty as "skip to
next." That's reasonable for these configs (an empty API key is useless), but for
`allowInsecure` an explicit `UNIFI_ALLOW_INSECURE=` (empty) silently falls through to the
next env (there is none) and then to default-off. Edge case, unlikely to bite, noting for
completeness. If exact semantics ever matter, `os.LookupEnv` distinguishes them.

---

## spec_sanitize.go — focused correctness pass

This is the trickiest file (raw `map[string]any` walking + ref rewriting). Verdict: the
recursion and rewrite logic look correct for the documented inputs, and the tests cover
the load-bearing cases (key rename + ref rewrite, collision-as-error, typeless top-level &
inline, null items, non-string enum drop, empty license). Specific notes:

- **GOOD:** collision detection is a hard error (`spec_sanitize.go:69-71`), not a silent
  overwrite — exactly right, and tested.
- **GOOD:** deterministic by construction — sorted `oldKeys` iteration + `encoding/json`'s
  sorted map-key marshaling. The determinism claim in the comment is accurate.
- **GOOD:** the `additionalProperties` bool-vs-schema discrimination
  (`spec_sanitize.go:191`) correctly avoids treating `additionalProperties: true` as a
  sub-schema to patch. Easy bug, handled.
- **L9 (Low) — ref rewriting only covers `$ref` and `discriminator.mapping`.** OpenAPI 3.1
  permits `$ref` siblings and refs can appear via other constructs, but for *component
  schema* renames the two covered positions are the realistic ones in this spec. The bare
  vs full-path mapping handling (`spec_sanitize.go:256-260`) is a nice catch. The risk is
  silent: if a rename target is referenced through a position this walker doesn't rewrite,
  you get a dangling `$ref` that fails *later* in pulschema with a worse error. Consider a
  post-pass that asserts no `$ref` resolves to an old (pre-rename) key — cheap insurance,
  turns a confusing downstream failure into a precise one.
- **L10 (Low) — `isStringEnum` with a composite `type` (3.1 type arrays) returns false**
  (`spec_sanitize.go:156-170`): a 3.1 schema with `"type": ["string","null"]` and a string
  enum has its enum dropped because `type` is a `[]any`, not a `string`. Probably fine for
  this spec (the comment says only numeric/bool enums are the target), but a nullable
  string enum would lose its constraint silently. Low likelihood; note it.
- **L11 (Low) — `patchEmptySchemas` mutates input maps in place** while
  `SanitizeSpecBytes` also builds `newSchemas`; the function is not pure (it edits the
  decoded `doc` graph). That's fine because the input is a freshly-unmarshaled throwaway,
  but the in-place mutation + the `newSchemas[key]` re-read at `spec_sanitize.go:93` is
  slightly confusing (you normalize `m` in place, then call `patchEmptySchemas` on
  `newSchemas[key]` which is the same object). Works, but a one-line comment that `m` and
  `newSchemas[key]` alias would save the next reader a double-take.

---

## openapi_fixes.go

Clean. `FixOpenAPIDoc` always returns `nil` error (`openapi_fixes.go:39-44`) — the three
helpers can't fail, so the `error` return is currently vestigial. Keeping it is defensible
(future fixes may fail, and callers already handle it), but note it so a reader doesn't
hunt for the error path. `ensureSchemaTitles` and the inject/rewrite helpers are correct
and well-documented. One nit: `rewriteServerURL` only rewrites `Servers[0]`
(`openapi_fixes.go:107`); if the spec ever ships multiple servers, the rest keep the
relative URL. Documented intent is single-server, so Low/acceptable.

---

## go.mod / dependency hygiene

- **Go 1.26 (`go.mod:3`)** — aggressive but internally consistent (CLAUDE.md states the
  prereq). Fine for a greenfield provider; just be aware it forces a very recent toolchain
  on contributors/CI.
- **Untagged v0.0.0 pseudo-versions** for the two `cloudy-sky-software` deps
  (`go.mod:6-7`) — `v0.0.0-20260425…`. This is the project's core risk: the provider is
  pinned to *unreleased commits* of `pulschema` and `pulumi-provider-framework`, both of
  which are pre-1.0 and (per the CLAUDE.md/DESIGN notes) have unexported internals this
  code works around. Pseudo-version pins are reproducible (good), but there's no semver
  contract — any bump can break the `coalesceDiscriminatedCRUD` assumptions or the
  `injectInsecureTransport` mirroring. Recommend: a short comment in `go.mod` (or a
  `tools`/`deps` doc) recording *why* these are pinned to exact commits and what to
  re-verify on bump (auth header derivation, `CreateGetRequest` signature, the rate-limit
  transport). Not a code defect; a maintainability landmine worth signposting.
- **`github.com/pkg/errors v0.9.1` (`go.mod:9`)** — removable once M2 is done; it's the
  only direct dep that's archived.
- The large indirect set is inherited from `pulumi/pkg` + `kin-openapi` + the framework;
  nothing actionable there.

---

## What's done well (credit where due)

- **Thinness honored.** The hand-written surface really is small glue; no business logic
  has leaked in that belongs in codegen. Matches the stated design.
- **Comment quality is high and *explains why*, not what** — e.g. the `OnPostInvoke`
  pagination rationale, the `injectInsecureTransport` honesty about losing 429 retry, the
  `coalesceDiscriminatedCRUD` doc block. This is above-average for glue code.
- **Determinism is taken seriously and tested** (`sortedKeys`, sorted sanitizer iteration,
  `ensureSchemaTitles`, `TestPipelineDeterministic`). The `sortedKeys[V any]` generic is a
  clean, correct use of generics.
- **`apiHost` validation** (`provider.go:117-129`) is a thoughtful guard against the
  framework's silent `baseURL.Host = apiHost` corruption — catching operator error at
  configure time with a clear message, and tested across URL/scheme/path cases.
- **`panic`/`contract.Failf` in the codegen path is the right call** — `pulumi-gen-unifi`
  is a build-time tool; a panic with a wrapped error (`main.go`) is fine there and the
  error context is preserved (except L5). Don't "fix" these into error returns.
- **Pagination is structured for testability** — `aggregatePages` is pure (HTTP injected
  via the `fetch` closure), and the tests exercise single/multi-page, empty-terminator,
  fetch-error, and missing-totalCount. Good seam. (H1 is about a missing *case*, not a
  missing seam.)
- **`toInt`/`toSlice` correctly model `encoding/json`'s `float64`-for-numbers behavior**
  and are tested. Small, correct, idiomatic.
- **Resource cleanup is correct** where it matters: `defer resp.Body.Close()` in the
  page-fetch (`provider.go:214`), `t.Cleanup` restoration of globals in tests.

---

## Priority fix list

1. **H1** — add a page ceiling + non-advancing-page guard to `aggregatePages`; check
   `ctx.Err()` per iteration. (Correctness/availability.)
2. **M1** — move `handler` off the package global onto the `unifiProvider` struct; drop the
   `callback` global. (Architecture/race/testability.)
3. **M2** — replace `github.com/pkg/errors` with stdlib `errors` + `fmt.Errorf("%w")`;
   remove the dep. (Dep hygiene, mechanical.)
4. **M3 / L1** — `io.LimitReader` the error body; log unparseable `allowInsecure`.
5. **M4 / L4 / L7** — config-resolution helper, `any` over `interface{}`, real package doc
   in version.go.
6. Signpost the untagged-dep pins (go.mod note) so a bump has a checklist.
