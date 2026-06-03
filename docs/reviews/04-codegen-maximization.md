# Review 04 — Codegen Maximization / Minimizing Hand-Written Code

**Reviewer stance:** adversarial. The stated goal is a *generated* provider that tracks an abstract
upstream OpenAPI doc, with hand-written Go confined to thin, mechanical, deterministic glue. This
review hunts for every place hand-written or opinionated logic has crept in, judges whether it is
justified, and proposes how to shrink the surface or push it upstream.

**Scope reviewed:** `provider/pkg/gen/{schema.go,openapi_fixes.go,spec_sanitize.go}`,
`provider/cmd/pulumi-gen-unifi/main.go`, `provider/pkg/provider/provider.go`, `Makefile`,
`openapi/{SOURCE,fetch.sh}`, the gen-layer tests, and the actual generated artifacts
(`schema.json`/`metadata.json`) against the pinned 10.4.57 spec.

---

## Executive summary

The project largely lives up to its thesis. The genuinely heavy lifting — resource grouping, type
translation, CRUD endpoint mapping, SDK emission — is delegated to pulschema +
pulumi-provider-framework + `pulumi package gen-sdk`. There is **no hand-authored resource list, no
`grouping.{go,yaml}`, no per-resource Go**. Self-scoping to write coverage works exactly as
advertised: I verified the pinned spec yields **21 resources + 50 functions + 71 crudMap keys, 0
orphans**, derived purely from verbs present. That is the right architecture and it is faithfully
executed.

The hand-written surface that remains is real but mostly *defensible*. It falls into four buckets:

1. **Spec-quality repair** (`SanitizeSpecBytes`) — generic OpenAPI hygiene compensating for a
   low-quality community capture. Mostly upstreamable; one part (enum dropping) is lossy.
2. **UniFi-specific facts not in the spec** (`FixOpenAPIDoc`: auth header, base path) — irreducible
   opinion, correctly minimal, but partly hardcoded where it could be derived/config-driven.
3. **Compensation for a pulschema bug** (`coalesceDiscriminatedCRUD`) — the single largest and most
   opinionated piece. Justified as a stopgap, but it bakes in a structural heuristic that *will*
   break silently on certain spec shapes, and it is the #1 upstream-PR candidate.
4. **Hand-maintained metadata** (the `PackageSpec` literal) — duplicated config, hardcoded version
   filename. Low-risk but already drifting; reducible.

The biggest *risks* are not the existence of the glue but the **absence of guardrails** around it:
there is no test that `excludedPaths` entries still match the spec, no token-set drift gate, and the
discriminated-variant resource names (`Standard`, `Gateway`, `Mac`, `Ipv4`, `Ports`) are generic
enough to collide across entities on a future spec bump. "Track the abstract upstream" is true today
but several silent-failure modes lurk one spec bump away.

**Overall grade on the stated goal: B+.** The architecture is right and the hand-written code is
honest and well-commented. Points off for: a 90-line opinionated CRUD-repair heuristic that should be
upstream, missing drift/exclusion guards that turn spec bumps into silent-corruption risks, and
hardcoding the spec *version* into four source files.

---

## Inventory of every hand-written component

| Component | File:line | Lines | What it is | Justified? | Reducible? | Upstream candidate? |
|---|---|---|---|---|---|---|
| `excludedPaths` list | `gen/schema.go:29-37` | 9 | 7 hardcoded paths dropped (RPC `actions`, `ordering`, read-only sub-resources) | **Partly** — necessary, but "grows empirically" = opinionated | Yes — make principled (drop POST-only RPC paths by shape) + add an existence guard | Maybe — a "non-CRUD path" classifier could live in pulschema |
| `PackageSpec` literal | `gen/schema.go:42-124` | ~80 | name/desc/keywords/homepage/publisher/license/PluginDownloadURL + Config + InputProperties + csharp/python lang info | **Mostly** — Pulumi requires package metadata somewhere | Yes — de-duplicate Config↔InputProperties; derive `apiKey` from the security scheme | No — package identity is inherently this repo's |
| `coalesceDiscriminatedCRUD` | `gen/schema.go:176-253` | ~78 | Post-process repairing pulschema's create-only discriminated stubs; prunes orphan keys | **As a stopgap, yes** — without it 18/21 resources are broken | Partly — pruning is clean; the *fill* heuristic is fragile | **YES — primary upstream PR** (per-verb discriminator schema names in pulschema) |
| `findItemPath` + helpers | `gen/schema.go:255-292` | ~38 | Heuristic: item path = collPath + "/{param}" | Yes, for the stopgap | The heuristic is the fragile part (see F2) | Same PR as above |
| `FixOpenAPIDoc` / `injectAPIKeySecurityScheme` | `gen/openapi_fixes.go:39-97` | ~60 | Inject `X-API-Key` apiKey scheme; set global security | **Yes** — irreducible UniFi fact (spec has none) | Header *name* could be config-driven; the injection itself cannot | No (UniFi-specific) |
| `rewriteServerURL` | `gen/openapi_fixes.go:99-108` | 10 | Rewrite relative `/integration` → `https://localhost/proxy/network/integration` | **Yes** — framework needs an absolute base URL | `/proxy/network/integration` and `localhost` are hardcoded; path is a UniFi fact, host is a throwaway | Framework PR: accept a relative/path-only server + host override (would remove the rewrite) |
| `ensureSchemaTitles` | `gen/openapi_fixes.go:62-74` | 13 | Give every title-less component schema a Title = its key | **Yes** — required for determinism | No (minimal already) | **YES** — pulschema should not collapse title-less discriminated getters |
| `SanitizeSpecBytes` (key sanitize + ref rewrite) | `gen/spec_sanitize.go:30-99,233-272` | ~110 | Legalize component keys, rewrite `$ref`/discriminator mappings, drop empty license | **Yes** — kin-openapi rejects the keys before any fix can run | Slightly | **YES** — generic OpenAPI-3 key sanitizer is reusable / belongs upstream |
| typeless/empty-schema normalization | `gen/spec_sanitize.go:101-152,172-230` | ~80 | `{}`/`null`/description-only schemas → free-form object | **Yes** — pulschema panics otherwise | No | **YES** — pulschema should treat constraint-less schema as `any` rather than panic |
| enum dropping | `gen/spec_sanitize.go:154-185` | ~30 | Drop non-string enums (8 in this spec) | **Lossy but justified** — pulschema only supports string enums | No (until pulschema supports them) | **YES** — pulschema numeric/int enum support |
| `apiHost` validation | `provider/provider.go:117-129` | ~10 | Reject `apiHost` containing `/` | **Yes** — framework does raw `baseURL.Host = apiHost` | No | Framework PR: validate/parse host itself |
| `injectInsecureTransport` | `provider/provider.go:284-305` | ~22 | Replace transport to skip TLS verify (re-implements framework transport) | **Reluctantly** — framework exposes no insecure hook | No — duplicates framework internals, loses 429 retry | **YES** — framework exported insecure-transport / 429-retry setter |
| `OnPostInvoke` page aggregation | `provider/provider.go:165-263` | ~100 | Follow offset/limit, assemble full collection | **Yes** — framework issues one GET, silently truncates | Marginally | Framework PR: native pagination from the page-envelope schema |
| Hardcoded spec filename `unifi-network-10.4.57.json` | `main.go:51`, `Makefile:13`, `gen_test.go:25`, `fetch.sh:9-10` | — | Spec *version* baked into 4 source files | **No** — couples codegen to a version | Yes — derive filename from one place | n/a |

---

## Severity-ordered findings

### F1 — `coalesceDiscriminatedCRUD` is the right *layer* but the wrong *home* — HIGH (design risk, not a bug today)
**`gen/schema.go:176-253`**

This is the single most opinionated piece of code in the repo: ~78 lines of bespoke logic that
**reconstructs CRUD bindings pulschema failed to produce**. I confirmed it is *load-bearing* — without
it, 18 of 21 resources are create-only stubs that die on the second `pulumi up`. So it is justified as
a stopgap and the implementation is careful (deterministic, sorted iteration, never overwrites a verb
pulschema already bound, prunes orphans). The Phase-2 orphan prune is clean and self-evidently
correct (token-set membership only).

The problem is *where it lives*. This is not "thin mechanical glue analogous to FixOpenAPIDoc" as the
comment claims — `FixOpenAPIDoc` injects two facts the spec is *missing*; this **rewrites pulschema's
output** to undo a pulschema bug. It is a fork of pulschema's grouping logic living downstream of
pulschema. Every byte of it is debt that disappears the moment pulschema is fixed.

- **Justified?** Yes, today. But it is the opposite of "track the abstract upstream" — it tracks
  *pulschema's current behavior*. If a pulschema bump changes how it binds discriminated verbs, this
  code silently double-binds or fights it.
- **Path to eliminate:** Upstream PR to pulschema (see U1). This is the single highest-leverage move
  to shrink the hand-written surface.

### F2 — The `findItemPath` heuristic bakes in assumptions that break silently — HIGH
**`gen/schema.go:255-277`**

The fill step assumes: *a collection POST path `P` has exactly one canonical item path `P + "/{param}"`
that carries the entity's R/U/D, and the discriminator rides in the body so that item path is correct
for every variant.* I verified this holds for all 9 writable entities in 10.4.57. But the heuristic is
brittle in ways that will not announce themselves:

1. **Collection-level mutating verbs are ignored.** `hotspot/vouchers` has `DELETE` on the
   *collection* (bulk delete) — confirmed in the spec. The heuristic only looks at the item path, so a
   real bulk-delete endpoint is silently invisible. For `Voucher` that happens to be fine (item DELETE
   exists too), but the pattern "mutation lives on the collection" is a blind spot.
2. **First-match wins on multiple `{param}` children.** `findItemPath` returns the first sorted child
   matching `collPath + "/{param}"`. If a future entity has two single-param children (e.g.
   `/networks/{networkId}` and a hypothetical `/networks/{alias}`), it silently picks one by sort
   order. The comment acknowledges this ("Sorted iteration keeps the result deterministic even in the
   (unexpected) case of multiple matches") — deterministic, but possibly *deterministically wrong*.
3. **Param-name coupling is avoided (good)** — it keys on shape, not literal param names, so it
   survives `{aclRuleId}`→`{id}` churn. Credit where due.

- **Justified?** As a stopgap, yes. But (1) and (2) are silent-correctness hazards on spec bump.
- **Path to reduce:** fold into the U1 upstream PR. Short of that, add a unit assertion that each
  filled resource's chosen item path is *unique* (fail loudly on >1 candidate), and a test that flags
  collection-level mutating verbs that get dropped.

### F3 — Discriminated-variant resource names are generic and collision-prone — HIGH (latent)
**emergent, surfaced by `gen/schema.go` + pulschema naming**

The 21 resource tokens include `Standard`, `Gateway`, `Switch`, `Unmanaged`, `Mac`, `Ipv4`, `Ports`,
`Ipv4Addresses`. These come from variant schema titles, not entity-qualified. Inspecting the spec's
discriminator mappings shows heavy reuse of variant names across entities: `IPV4`/`MAC` appears in
acl-rules; `PORTS` appears in traffic-matching-lists *and* in firewall match-targets *and* in
protocol-matching; `GATEWAY`/`SWITCH`/`UNMANAGED` appears in multiple network sub-schemas. Today there
is no collision only because the *writable* entities happen to have disjoint variant titles.

When Ubiquiti ships writes for another entity that reuses one of these variant names (very likely,
given the mapping reuse above), two entities will both want the token `unifi:sites/v1:Ports`. The
result is a **schema token collision** — pulschema/the framework will overwrite or fail. `coalesceDiscriminatedCRUD`
would then fill CRUD onto whichever survived, masking the collision. `ensureSchemaTitles` mitigates the
*getter* version of this for read-only data sources, but resource tokens derive from request-schema
titles and have no equivalent guard.

- **Justified?** This is pulschema's naming, not hand-written — but the project *relies* on it without
  a guard.
- **Path to reduce:** add a no-Docker test asserting resource + function token sets have no
  duplicates after generation (a token-set drift gate, BUILD-PLAN Phase 5 already lists this as TODO —
  it is more urgent than "human sign-off on additions"). Long term, pulschema should entity-qualify
  variant tokens.

### F4 — No guard that `excludedPaths` entries still exist in the spec — MEDIUM
**`gen/schema.go:29-37`**

`excludedPaths` is 7 hardcoded strings that "grow empirically." I verified all 7 currently match real
spec paths. But there is **no test** asserting that (BUILD-PLAN Phase 5 lists it as a future item; it
is not implemented — I checked). Failure modes on spec bump:

- An excluded path is **renamed/removed upstream** → the stale entry silently no-ops, and the
  now-unexcluded RPC path (if re-added under a new name) leaks a junk resource.
- A **new RPC/`actions`/`ordering` path appears** → it is *not* excluded, silently producing a junk
  resource or a malformed CRUD binding. Nothing fails; `pulumi up` just gains a nonsense resource.

The list is also *opinionated* in a non-obvious way: it mixes three categories (RPC `actions`,
`ordering` mutations, read-only sub-resource lookups like `networks/{id}/references`) with no
machine-checkable predicate. `actions`/`ordering` could be dropped *by shape* (POST-only or PUT-only
leaf paths that are not collection/item CRUD), which would make the list self-maintaining for those
two categories and shrink the hand-maintained set to just the genuinely editorial read-only lookups.

- **Path to reduce:** (a) add the existence guard from Phase 5 (turn silent into loud); (b) replace
  the `actions`/`ordering` entries with a shape-based rule so only true editorial exclusions remain
  hardcoded.

### F5 — Config vars duplicated between `Config` and `InputProperties`; `apiKey` not derived from the scheme — MEDIUM
**`gen/schema.go:59-117`**

All four config vars (`apiKey`, `apiHost`, `siteId`, `allowInsecure`) are written **twice** — once in
`Config.Variables`, once in `Provider.InputProperties` — with hand-copied descriptions. They already
**drift**: `apiHost`'s description in `Config` ("...Overrides the host in the generated server URL.")
is longer than in `InputProperties` ("The UniFi controller host (and optional :port)."). This is a
textbook maintenance hazard in supposedly-generated code.

Also: DESIGN §6 and the schema.go comment claim "pulschema also emits the `apiKey` config property
from that scheme." I verified the generated `schema.json` — **it does not**; both `apiKey` entries are
hand-written. So the security-scheme injection is doing *less* derivation than documented, and the
`apiKey` config is pure hand-maintenance.

- **Justified?** Pulumi needs both blocks, but the duplication is accidental, not essential.
- **Path to reduce:** build `InputProperties` programmatically from `Config.Variables` plus a small
  env-var map (the only real delta is `DefaultInfo.Environment`). ~15 lines collapses ~50 and kills
  the drift. Optionally verify whether a newer pulschema can emit `apiKey` from the scheme and delete
  the hand-written one if so (correct the DESIGN claim either way).

### F6 — Spec *version* hardcoded in four source files — MEDIUM
**`main.go:51`, `Makefile:13`, `gen_test.go:25`, `fetch.sh:9-10`**

`unifi-network-10.4.57.json` is hardcoded in four tracked source locations. The pin itself (SHA +
checksum) lives correctly in one place (`fetch.sh`), but the *filename* leaks everywhere, so a spec
bump requires editing four files in lockstep — and a mismatch (e.g. forgetting `gen_test.go`) makes
the determinism test silently *skip* (it `t.Skipf`s when the file is absent) rather than fail. This
directly undermines "track the abstract upstream": the codegen is coupled to a literal version string
in code that has nothing to do with versioning.

- **Path to reduce:** have `fetch.sh` write a stable symlink/name (e.g. `openapi/spec.json`) or emit
  the filename to a single `openapi/SPEC_FILE` that `Makefile`, `main.go` (via env/flag), and the test
  all read. Then only `fetch.sh` + `SOURCE` carry the version. Bonus: make the determinism test
  *fail* (not skip) when the spec is missing in CI.

### F7 — `injectInsecureTransport` re-implements framework internals — MEDIUM
**`provider/provider.go:284-305`**

To support `allowInsecure`, the provider hand-copies the framework's entire `http.Transport`
construction (timeouts, idle conns, etc.) just to flip `InsecureSkipVerify`, because the framework
exposes no transport setter. The copy will silently rot if the framework changes its transport, and it
**loses the framework's 429 retry wrapper** (documented honestly). This is hand-written code that
exists only because of a missing upstream seam.

- **Path to reduce:** upstream PR (U4) for an exported insecure-transport option or a 429-retry
  wrapper constructor; then this collapses to one line.

### F8 — `OnPostInvoke` pagination is provider-side logic that arguably belongs in the framework — LOW/MEDIUM
**`provider/provider.go:165-263`**

~100 lines re-issue offset/limit GETs to assemble full collections, because the framework issues one
GET and silently truncates. The logic is well-factored (the pure `aggregatePages` is unit-testable)
and the bug it fixes is real (partial data that decodes cleanly). But it hardcodes UniFi page-envelope
keys (`data`/`totalCount`) and the limit `200`, and it reaches into framework internals
(`CreateGetRequest`, `GetHTTPClient`). It is justified glue, but it is provider-specific logic
compensating for a generic framework gap.

- **Path to reduce:** upstream PR (U5) for schema-driven pagination (the page-envelope is describable
  in the OpenAPI doc). Until then, keep it but consider deriving the page-size cap from the spec's
  `limit` parameter `maximum` instead of the hardcoded `200`.

### F9 — `rewriteServerURL` hardcodes the integration base path and a throwaway host — LOW
**`gen/openapi_fixes.go:20-24,99-108`**

`/proxy/network/integration` is a genuine UniFi fact (irreducible). `localhost` is a throwaway
placeholder (fine — the host is swapped at runtime). The minor opinion is that the base path is a
compile-time constant; if Ubiquiti ever changes the proxy mount it is a code edit. Low risk, but it
could be a `const` documented as "the one UniFi deployment fact we hardcode" (it already is — this is
close to minimal). The cleaner long-term fix is U2 (framework accepts a path-only server + host
override, removing the need to synthesize an absolute URL at all).

### F10 — `enum` dropping is lossy validation removal — LOW (justified, documented)
**`gen/spec_sanitize.go:154-185`**

I counted: 51 string enums preserved, **8 numeric/integer enums dropped** (e.g. WiFi
`broadcastingFrequenciesGHz` ∈ {2.4, 5, 6}, channel-width integer enums). Dropping them means the
generated SDK accepts any number where the API accepts only a fixed set — validation moves from
plan-time to API-error-time. This is honest, commented, and forced by pulschema (string-enum-only).
Acceptable, but it is *lossy* and *opinionated* (it chooses "drop constraint" over "fail"). Flag it as
a known fidelity gap and the cleanest upstream candidate (U3).

---

## Concrete upstream PR candidates

Ranked by how much hand-written surface each removes from this repo.

| # | Target | Change | Removes from this repo | Effort |
|---|---|---|---|---|
| **U1** | **pulschema** | Bind **all** verbs (R/U/D/P) to each discriminated-variant resource token, not just create — i.e. per-verb discriminator schema names + full CRUD per variant. | **All ~116 lines** of `coalesceDiscriminatedCRUD` + `findItemPath` + helpers (F1, F2). Biggest single win. | High (touches pulschema's core grouping) |
| **U2** | pulumi-provider-framework | Accept a relative/path-only `servers[0].url` and apply `apiHost` as host override, OR accept a full base-URL override. | `rewriteServerURL` (F9) and removes the "absolute URL with placeholder host" dance. | Medium |
| **U3** | pulschema | Support numeric/integer/boolean enums (emit as typed enums or at least preserve as validation). | enum-dropping branch in `patchEmptySchemas` (F10); restores fidelity for 8 enums. | Medium |
| **U4** | pulumi-provider-framework | Export an insecure-transport option (or a 429-retry wrapper constructor) so callers can skip TLS verify without losing retry. | `injectInsecureTransport` (F7) → 1 line; restores 429 retry. | Low |
| **U5** | pulumi-provider-framework | Schema-driven list pagination (follow page envelope described in the OpenAPI doc). | `OnPostInvoke`/`aggregatePages` (F8), ~100 lines. | Medium/High |
| **U6** | pulschema | Treat constraint-less / `null` / description-only schemas as free-form `any` instead of panicking; tolerate non-identifier component keys (sanitize internally). | Most of `SanitizeSpecBytes` typeless-normalization + key-sanitization (F-table rows). | Medium |
| **U7** | pulschema | Assign stable unique titles to title-less discriminated component schemas internally (the determinism fix). | `ensureSchemaTitles` (gen/openapi_fixes.go:62-74). | Low |

U1, U6, U7 together would eliminate the bulk of `gen/` and bring the project very close to its ideal:
`FixOpenAPIDoc` reduced to the two irreducible UniFi facts (inject `X-API-Key`, name the base path),
plus a hand-maintained `PackageSpec` identity block.

---

## What is genuinely unavoidable vs accidental

**Unavoidable (irreducible opinion — leave it):**
- Injecting the `X-API-Key` security scheme — the spec has none; this is a UniFi fact. (`openapi_fixes.go`)
- Naming the integration base path `/proxy/network/integration` — a UniFi deployment fact.
- The `PackageSpec` *identity* (name, publisher, repo, license, PluginDownloadURL) — inherently this repo's.
- `GetAuthorizationHeader` returning the bare key, `GetGlobalPathParams` for `{siteId}` — minimal,
  spec-shaped provider callbacks.

**Accidental / reducible now (no upstream needed):**
- Config↔InputProperties duplication and drift (F5) — derive one from the other.
- Spec-version filename hardcoded in 4 files (F6) — centralize.
- `excludedPaths` `actions`/`ordering` entries (F4) — replace with a shape-based rule.
- Missing exclusion-existence + token-collision guards (F3, F4) — add ~30 lines of test.

**Reducible only via upstream (carry for now, but file the PRs):**
- `coalesceDiscriminatedCRUD` (U1) — the big one.
- `injectInsecureTransport` (U4), `OnPostInvoke` pagination (U5), enum dropping (U3),
  typeless/key sanitization (U6), `ensureSchemaTitles` (U7), `rewriteServerURL` (U2).

---

## Ranked: hand-written components by opinionated-ness / risk

1. **`coalesceDiscriminatedCRUD` + `findItemPath`** — most opinionated, highest design risk (forks
   pulschema, fragile heuristic, masks collisions). **F1, F2, F3.**
2. **`excludedPaths`** — editorial, no guard, silent drift on bump. **F4.**
3. **enum dropping** — lossy/opinionated but bounded and documented. **F10.**
4. **`injectInsecureTransport`** — duplicates framework internals, loses retry. **F7.**
5. **`OnPostInvoke` pagination** — provider-specific, framework-internal coupling. **F8.**
6. **`PackageSpec` literal duplication + version hardcoding** — maintenance hazard, low risk. **F5, F6.**
7. **typeless/empty-schema normalization, key sanitization** — generic OpenAPI hygiene, well-tested. Low risk.
8. **`FixOpenAPIDoc` auth/server injection, `ensureSchemaTitles`** — minimal, irreducible (modulo upstream). Lowest risk.

## Top recommendations (in priority order)

1. **File the pulschema CRUD-binding PR (U1).** It deletes the most opinionated code in the repo.
2. **Add two no-Docker guard tests:** (a) every `excludedPaths` entry matches a path in the sanitized
   spec; (b) resource + function token sets contain no duplicate short names. These convert the two
   most dangerous silent spec-bump failures (F3, F4) into loud ones.
3. **De-duplicate the config block (F5)** and correct the DESIGN claim that pulschema emits `apiKey`.
4. **Centralize the spec filename (F6)** so a bump touches only `fetch.sh` + `SOURCE`; make the
   determinism test fail-not-skip in CI.
5. **File U4/U5** so `allowInsecure` keeps 429 retry and pagination leaves the provider.
