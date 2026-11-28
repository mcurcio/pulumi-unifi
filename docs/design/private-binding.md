# Private binding: slice 1 (extractor, Kind model, codegen, engine, e2e) and slice 2 (official ACL writes, authenticated edge)

**Status:** Part 1 (§0-§14) is the decided and built design for the first private-API slice: built,
measured and green (§14). Part 2 (§S2.0-§S2.14) is the **decided design for slice 2 and the official
ACL rule writes**, from the owner's decisions of 2026-11-28 and the slice 2 probe. Its build step 1
(official ACL rules, their ordering, the rename with aliases) is built and measured live; the rest is
not built yet (§S2.14).
Both implement the agreed frame (`docs/design/private-api-provider-frame.md` on
`docs/two-api-provider-frame`: one provider over two APIs, rules 1-3, G-Q1, natural keys, stored ids
that name their API, whole-kind serving, with the one variant exception of §S2.7). The evidence is
`docs/research/private-api-model-source.md`. **Where am I:** slice 1 Implementation done; slice 2
Implementation in progress on this branch: step 1 done (with the ordering of step 3 pulled in, §S2.14
row 1), steps 2 and 4-6 next.

## 0. How this was chosen

Three independent designs were scored 1 (poor) to 5 (strong).

| Criterion | D1 minimal (Python extractor, build tag) | D2 frame-faithful (Go extractor, 12 kinds) | D3 risk-first (Go extractor, 6 kinds, gates) |
|---|---|---|---|
| Fit to the frame | 3: put `static-dns` in the slice, which breaks whole-kind serving (official DNS records exist) | 5 | 4 |
| Time to a real e2e-tested package | 5 | 2: 12 kinds, 6 of them classic with unverified writes | 4 |
| Licence safety | 4 | 4 | 5: adds a CI grep gate |
| Determinism and CI risk | 2: `javap` text depends on the JDK; a build tag splits the binary | 4 | 5: model digest, token golden, and a skip that can be made to fail |
| Maintainability across version bumps | 3 | 4: ties the two pins together | 4: differential oracle |
| **Total** | 17 | 19 | **22** |

**Winner: D3.** Grafted in: from D1, the 4 v2 kinds that round-tripped (the base of the slice), the
static in-container TCP forwarder (no extra image to pull), and opt-in private generation with the
default build unchanged (a make variable rather than a build tag); from D2, `UOS_INSTALLER` reuse of a
local copy, the pin tie (`NETWORK_VERSION == SPEC_VERSION`; installer `integration.json` SHA-256 ==
`SPEC_SHA256`, both checked), the `source:` provenance note on every override, and the per-kind rule
checks.

## 1. Goals and non-goals

| Goals (slice 1) | Non-goals (deferred, with the slice that owns them) |
|---|---|
| A Go extractor that turns the pinned UniFi OS Server installer into one canonical Kind model, with no JDK and no Python | Rebuilding the official resources on the Kind model; pulschema stays (frame step 4) |
| A private engine that enforces rules 1-3 and G-Q1 for private kinds | Per-field serving (Q4 B); moving kinds between APIs; re-resolving stale ids across APIs |
| 4 private v2 resources in the merged schema and Python SDK | Classic `/api/s/{site}/rest` resources; settings singletons (slice 2) |
| `make test-e2e-private`: the Pulumi CLI drives the Python SDK against a real UniFi OS Server 5.1.15, booted from the same pinned installer | Authenticated edge (`/proxy/network`) writes; post-wizard behaviour (the key-minted tier, slice 2) |
| All existing gates unchanged and green; the committed `schema.json` golden is byte-identical | Data-source functions for private kinds (natural-key import covers lookup for now) |
| | Publishing a private-capable SDK or plugin (Phase 5, an owner decision; see §9) |

The slice-2 non-goals above (classic kinds, settings singletons, the authenticated edge, post-wizard
behaviour) are now goals of Part 2; its own goals and non-goals are in §S2.1.

## 2. Components and file layout

| Path | Committed | Role |
|---|---|---|
| `uos/pin.env` | yes | `UOS_VERSION=5.1.15`, `UOS_PLATFORM=linux-x64`, `UOS_URL`, `UOS_SIZE=874927011`, `UOS_SHA256=04c8e401…cad2bb`, `UOS_NETWORK_VERSION=10.4.57`. Shell-sourceable and parsed by Go, like `openapi/pin.env`. Force-tracked past `*.env`. |
| `uos/SOURCE` | yes | Human-readable pin manifest, mirroring `openapi/SOURCE`. |
| `uos/fetch.sh` | yes | Gets the installer into the cache, or reuses `UOS_INSTALLER=<path>` after a SHA-256 check. It checks the **pinned** SHA-256, never fw-update's own checksum. |
| `provider/pkg/uos/` | yes | Extractor library, standard library only: `archive.go` (find the ZIP appended to the ELF via EOCD + `io.SectionReader` → `image.tar` → the Network layer, found **by content** (the layer holding `usr/lib/unifi/lib/ace.jar`) → `ace.jar` → nested `internal-dependencies.jar`, streamed in memory); `classfile.go` (constant pool, `RuntimeVisible{,Parameter}Annotations`, `Signature`, `Code`); `routes.go` (port of `routes.py`); `dto.go` (port of `dto.py --crud`: types, enums following `@JsonValue`, required, `@Value.Default` constant returns, `@Length`/`@Size`, custom validator messages, `@JsonTypeInfo`/`@JsonSubTypes`); `fields.go` (`api/fields/*.json`); `secrets.go` (`sensitive_metadata.json`); `model.go` (assembles `kind.Model`). |
| `provider/pkg/kind/` | yes | The one Kind/Field model (frame §2) plus a canonical serializer (sorted keys, sorted kinds, no timestamps or paths). Shared by the extractor, the generator and the engine. |
| `provider/cmd/pulumi-gen-unifi` | yes | New subcommands `uos-extract -installer F -out build/uos/model.json` and `private -model M -overrides O -golden S -out DIR`. |
| `provider/pkg/private/kinds.yaml` | yes | Overrides and exposure allowlist (§6). Embedded. |
| `provider/pkg/private/gen/` | yes | Model + overrides + golden → `schema.merged.json` + `private.json`; collision checks; reserved names. |
| `provider/pkg/private/engine/` | yes | Rules 1-3, G-Q1, Check, Diff, import by natural key. Knows nothing about HTTP. |
| `provider/pkg/private/binding/` | yes | HTTP for the private API: v2 paths and, already, the classic `{meta,data}` envelope; list+filter when item GET is 405; reuses `provider/pkg/provider/transport.go` (TLS, `allowInsecure`). |
| `provider/pkg/private/dispatch.go` | yes | Wraps the framework's `ResourceProviderServer`: `unifi:network:*` → engine; all else → framework (§4). |
| `provider/pkg/private/testdata/tokens.txt` | yes | Golden list of our private token names (4 lines). |
| `provider/pkg/private/testdata/digests.txt` | yes | SHA-256 of canonical `model.json` and of the generated `schema.merged.json` + `private.json` for the pin. Hashes only, no Ubiquiti text. |
| `provider/pkg/uos/testdata/*.java` + `*.class` | yes | Self-written synthetic classes that carry the same annotations, for Tier-1 parser tests. No Ubiquiti bytes. |
| `test/e2e-private/` | yes | `run.sh` (boot, forward, ready, test, teardown), `fwd/main.go` (static TCP forwarder), `program/__main__.py` + `Pulumi.yaml`, Go driver `e2e_test.go` (`//go:build e2e_private`). |
| `tools/uos-extract/` | yes (as is) | The research prototype stays as the **differential oracle** (`make uos-parity`). Not in the build. |

## 3. Model format (`build/uos/model.json`, gitignored)

```
Model {source: {uosVersion, networkVersion, installerSha256, extractorVersion}, kinds: [Kind]}
Kind  {name, api: "v2"|"classic", collection, itemPath, verbs, itemGet: bool /*false when item GET is 405*/,
       idField: "_id"|"id", envelope: "bare"|"meta-data", polymorph?: {prop, variants{}}, fields: {path: Field}}
Field {wireName, type: string|int|number|bool|uuid|enum|array|object|map, enum[], items?, fields?,
       pattern?, constraints{len,size,custom}, required, default?, readOnly, secret, source: "bytecode"|"fields"}
```

- `readOnly`: fields in the response model but not in the request model.
- `secret` fails closed: an `x_` prefix, a `sensitive_metadata.json` entry, `WRITE_ONLY` access, or a name
  matching `(?i)(password|passphrase|secret|psk|token|private_?key)` makes a field secret. Only overrides can clear it.
- `itemGet` is not in the bytecode. It comes from overrides (`itemGet: false`), measured live (405 on `apgroups`,
  `firewall-app-blocks`, `acl-rules`, `static-dns`).
- The public facts of each exposed kind come from applying `kinds.yaml` to the model; the
  engine reads only the merged result (`private.json`).

## 4. Build, pin and cache

| Item | Decision |
|---|---|
| Switch | `PRIVATE ?= 0`. With 0 (the default, and CI), `generate_schema` still runs `pulumi-gen-unifi private` without a model: it writes `schema.merged.json` **byte-identical to `schema.json`** and an empty `private.json`. With `PRIVATE=1`, `generate_schema` depends on `build/uos/model.json` and fails if it cannot be made. No build tag: one binary shape, and the engine is compiled and tested in every build. |
| Cache | `${UNIFI_UOS_CACHE:-$HOME/.cache/pulumi-unifi/uos}/<sha256>/installer`, outside the repo. `UOS_INSTALLER=<path>` reuses a local copy after the SHA-256 check (this run: the scratchpad `out-5.1.15/installer`). |
| Extract | Streamed from the installer, in memory. Nothing raw is written to disk, except `image.tar` for e2e, which goes to `<cache>/<sha256>/image.tar`. |
| Model | `build/uos/model.json`; rebuilt only when the pin or the extractor source changes (make prerequisite on `provider/pkg/uos/*.go`). |
| Pin ties | Tier 1 (committed files only): `UOS_NETWORK_VERSION == SPEC_VERSION`. With the installer: SHA-256 of its `webapps/ROOT/api-docs/integration.json` == `SPEC_SHA256` (already observed byte-identical for 5.1.15). |
| Determinism | Same pin + same extractor + same `kinds.yaml` → identical `model.json`, `schema.merged.json`, `private.json` and SDK. `make private-check` extracts twice, diffs, and compares against `digests.txt`. |
| CI | Unchanged in slice 1 (`PRIVATE=0`, no 875 MB download). A CI job that caches **only the model** (keyed by installer SHA + extractor hash) with `UNIFI_REQUIRE_UOS_MODEL=1` is the first follow-up. |

## 5. Codegen and token naming

| Item | Decision |
|---|---|
| Token | `unifi:network:<Kind>`, Python `pulumi_unifi.network.<Kind>`. It names no API and no API version (frame rule 3). `network` is the app name, so it stays valid if a kind later moves to the official API. Official tokens keep `sites/v1` until the contract rebase (frame step 5). |
| Kind name | From `kinds.yaml` (`token:`), never auto-derived. |
| Collisions | Gen error if a private token equals an official token, or if an exposed private kind is listed in `kinds.yaml` `officialTwins:` (whole kinds, G-Q4). The twin list: `static-dns`, `acl-rules`, `firewall-policies`, `firewall/zone`, networks, Wi-Fi, traffic-match lists. |
| Properties | lowerCamel of the wire name (`download_limit_kbps` → `downloadLimitKbps`); the wire name is kept in `private.json`. The id field (`_id` or `id`) is not a property: it is the resource id. Any other property named `id` or `urn` (Pulumi-reserved) must be renamed in `kinds.yaml` `rename:`, or gen fails. |
| Types | string/uuid → string; int → integer; number → number; bool → boolean; enum → string, with its values in the description and checked in Check (Pulumi enum types deferred); array → `items`; map → `additionalProperties`; object → a named type `unifi:network:<Kind><PathPascal>`. |
| Required | Extracted required + `implicitRequired` + every natural-key field. |
| Secret / read-only | `secret: true` in the schema; read-only fields are outputs only. |
| Polymorphic kinds | Gen error if an exposed kind has `polymorph`, unless overrides opt in (none in slice 1). |
| Config | `networkBaseUrl` is added **in the private fragment only** (§7), so the golden is unchanged. |
| Merge | Deterministic: golden resources/functions/types/config plus the private ones, sorted, serialized with the existing `serialize.go`. The plugin embeds `schema.merged.json` and returns it from GetSchema. `python_sdk` runs `gen-sdk` on `schema.merged.json`. With `PRIVATE=0` that file equals the golden, so the SDK, determinism and sdk-smoke jobs see no change. |
| Embeds | `main.go` embeds `schema.merged.json` and `private.json` (both gitignored; written by `generate_schema`, as `openapi_generated.yml` is today). The framework still gets the official `schema.json` + metadata, so its checks are untouched. |

## 6. Overrides file (`provider/pkg/private/kinds.yaml`, committed)

The only committed place for private route and field names: a small, hand-written set of facts
(go-unifi carries about 580 lines of comparable patches). Every entry carries `source:`, the evidence
that justifies it.

| Section | Meaning | Slice-1 content |
|---|---|---|
| `expose:` | Allowlist: collection → `token`, `naturalKey`, `itemGet` | the 4 kinds in §10 |
| `officialTwins:` | Kinds an official resource already serves; never exposed | as in §5 |
| `implicitRequired:` | Required in practice, not annotated (seen as 500 NPEs) | none needed for the 4 kinds; e.g. `nat: [source_filter]` recorded for later |
| `rules:` | Cross-field checks run in Check | none in slice 1 |
| `defaults:` | Values a dropped field resets to, where the model has none (G-Q1) | added as the e2e measures them (e.g. the values a fresh create returns), each with `source: "live create 10.4.57"` |
| `createOnly:`, `notReturned:`, `notSecret:`, `rename:` | Field facts | filled from e2e findings |

An override that names a kind or field the model lacks is a **loud gen error** (as `drift_test.go`
does for `mappings.yaml`), so a version bump that renames something cannot pass silently.

## 7. Config

| Key (env) | Meaning |
|---|---|
| `networkBaseUrl` (`UNIFI_NETWORK_BASE_URL`), new, private fragment only | Base for private calls. Default `https://<apiHost>/proxy/network` (the UniFi OS edge). Direct Network app: `http://127.0.0.1:8081`. Paths appended: `/v2/api/site/{site}/<collection>` and, for classic later, `/api/s/{site}/rest/<kind>`. |
| `apiKey` (existing) | Sent as `X-API-Key` on private calls when set; no auth header when unset. Never logged. |
| `siteId` (existing) | The short name (`default`) for private calls. Resolving between UUID and short name is deferred. |
| `allowInsecure` (existing) | Applies to the private client too. |

The dispatcher removes `networkBaseUrl` before it forwards CheckConfig/Configure to the framework.
Version guard (frame: one controller version per release): deferred until `stat/sysinfo` is verified
on an authenticated controller; slice 1 logs the pin it was built for.

## 8. Runtime semantics: now vs deferred

**Managed paths** = leaf paths of the inputs, with lists as leaves; objects merge field by field.

| Operation | Now | Deferred |
|---|---|---|
| Stored id | `private:<id>`. An id with another prefix is an error. | `mac:` ids and re-resolving across APIs (classic `user` in slice 2) |
| Check | Validates **declared** values only: type, enum, pattern, length, required (with implicit and natural-key). A dropped managed path with no default is a Check error asking for a `defaults:` entry (G-Q1). Values read back are never rejected (rule 1). | Custom validator messages |
| Create | POST a body built **only from inputs**; store `private:<id>`. | — |
| Read | Item GET, or list + filter by id when `itemGet: false`. 404 or absent → gone. Inputs = the live object projected onto managed paths (old inputs' paths); outputs = every returned non-secret field. A secret is stored only when it is declared. | — |
| Diff | Old inputs vs new inputs over managed paths; `createOnly` → replace. After refresh, old inputs are the live projection, so drift on managed paths shows; unmanaged fields never diff. | Showing `notReturned` fields as never drifting |
| Update (rule 2, G-Q1) | Fresh read → deep-merge managed paths onto it → set every dropped path to its default (model or overrides) → full PUT. Unmanaged fields keep their live values. | PATCH where a kind supports it |
| Delete | DELETE; 404 = success. | A built-in refusal turned into a release with a warning |
| Import (rule 3) | Id is `private:<id>` or the natural-key value (`field=value;…` for a composite key). Resolved by listing; zero or several matches is an error naming the candidates. Managed paths after import = natural key + the import's `properties`. | — |

## 9. Committed vs gitignored, and the licence decision

| Committed | Gitignored or outside the repo |
|---|---|
| `uos/{pin.env,SOURCE,fetch.sh}`, `provider/pkg/{uos,kind,private}` Go, `kinds.yaml`, `tokens.txt`, `digests.txt`, synthetic `.java`/`.class` fixtures, `test/e2e-private/*`, this doc | installer, `image.tar`, jars, classes, `api/fields`, `integration.json` (cache); `build/uos/model.json`; `provider/cmd/pulumi-resource-unifi/{schema.merged.json,private.json}`; `sdk/`; e2e Pulumi home and state |

**Decision (recorded here as the frame requires):** the repo commits only our own editorial facts: the
pin, the token names, and the few route and field names in `kinds.yaml`, each with its evidence. These
are interoperability facts, the same position as go-unifi's MPL-licensed patches. Bulk extracted text
(field files, class data, the model, the generated private schema and SDK) is never committed. A
`make test` check (`TestNoUbiquitiArtifacts`) fails if a tracked or unignored file is a `.class`/`.jar`, sits under
`fields/`, is a JSON file shaped like `api/fields` (a top-level object of validation-string leaves), is
shaped like the extracted model (a top-level `kinds` list that is non-empty or carries
`source.networkVersion`/`integrationSha256`; only the synthetic `provider/pkg/uos/testdata/fixture-model.json`
is exempt), is JSON naming a `unifi:network:` token (a generated private schema or surface), or is an
archive (`.tar`, `.tgz`, ...), an ELF binary or any file over 4 MiB (the installer, `image.tar`, a dump).
The Tier-2 seed `test/e2e/unifi-seed.tgz` (committed on main, Git LFS) is the one archive exempt; this
gate does not re-decide it.
Builds a user makes locally contain field names; **whether to publish a private-capable SDK or plugin is
left to the owner at Phase 5**.

## 10. First slice: kinds

| Collection (v2) | Token | Natural key | Item GET | Why in |
|---|---|---|---|---|
| `qos-rules` | `QosRule` | `name` | yes | Minimal create round-tripped; nested objects (`source`, `destination`, `schedule`) exercise the deep merge |
| `apgroups` | `ApGroup` | `name` | 405 → list + filter | Round-tripped; no device needed |
| `firewall-app-blocks` | `FirewallAppBlock` | `name` | 405 → list + filter | Round-tripped; arrays and a nested `schedule` |
| `network-members-group` | `NetworkMembersGroup` | `name` | yes | Round-tripped; id field is `id`, not `_id` |

Out: `static-dns` and `acl-rules` (official twins); classic kinds (writes unverified; `usergroup` is
the first slice-2 kind, after a write probe); kinds needing business rules, a set-up controller or a
device. Later kinds join one per PR, each with its own e2e row.

## 11. Test plan

| Gate | What | Needs |
|---|---|---|
| `make test` | existing tests, plus: pin tie (`UOS_NETWORK_VERSION == SPEC_VERSION`); synthetic class-file parser tests; engine tests with `httptest` and synthetic kinds (merge, drop-to-default, list + filter, 405, 404 delete, natural-key import with 0/1/2 matches, id prefix); `kinds.yaml` schema; `TestNoUbiquitiArtifacts`; with `PRIVATE=0`, `schema.merged.json == schema.json` | none |
| `make test-mock`, `schema-check`, determinism, sdk-smoke | unchanged; must stay green | Docker for test-mock |
| `make uos-model` | `uos/fetch.sh` → `pulumi-gen-unifi uos-extract` → `build/uos/model.json` | installer |
| `make private-check` | `UNIFI_REQUIRE_UOS_MODEL=1`: extract twice + diff; `digests.txt`; `tokens.txt`; the private surface lint (grammar `unifi:network:<PascalCase>`, no collision with the golden, secrets flagged, `TestContractLint` rules that apply); installer `integration.json` SHA == `SPEC_SHA256`. Without the model, these tests log `NOT RUN (no model)` and skip; with `UNIFI_REQUIRE_UOS_MODEL=1` a skip is a failure. | installer |
| `make uos-parity` | `-tags uosoracle`: Go model vs the prototype's `crud.json` (`UOS_ORACLE_CRUD=<path>`): exact field match on the 4 slice kinds; the `spec_check` port must reach ≥ 73/73 routes and ≥ 157/161 fields on 5.1.15 and 5.1.42 | installer + oracle files (JDK only to regenerate the oracle) |
| `make test-e2e-private` | below | Docker (privileged), installer |

**`make test-e2e-private`** (`PRIVATE=1`; `test/e2e-private/run.sh`; offline/local tier, not stock CI):

| Step | Detail |
|---|---|
| 1. Build | `make PRIVATE=1 build python_sdk`; venv with `sdk/python` + `pulumi`; plugin dir on `PATH`. |
| 2. Image | `docker load` `<cache>/<sha256>/image.tar` (from the same pinned installer, so model == target); tag `uosserver:<pin>`. |
| 3. Boot | `docker run -d --name uos-e2e-private` with the research recipe (`--privileged --cgroupns=host`, cgroup mount, tmpfs, `UOS_UUID`/`APP_*`/`PRODUCT_NAME`/`FIRMWARE_PLATFORM`) + `-p 127.0.0.1:18081:18081`. No wizard, no key, no nginx edge. |
| 4. Reach | Build `fwd` (`CGO_ENABLED=0`), `docker cp` it in, `docker exec -d … fwd 0.0.0.0:18081 127.0.0.1:8081`. |
| 5. Ready | Poll `GET http://127.0.0.1:18081/v2/api/site/default/apgroups` for JSON 200; 8 min limit. |
| 6. Drive | `go test -tags e2e_private ./test/e2e-private/...`: pinned Pulumi CLI 3.230.0, `PULUMI_SKIP_UPDATE_CHECK=1`, temp `PULUMI_HOME`, `file://` backend, fixed throwaway passphrase, config `unifi:networkBaseUrl=http://127.0.0.1:18081`, no `apiKey`. The program picks its inputs from config `step`. |
| 7. Assert, per kind | (1) `up` creates all 4; ids start `private:`; a raw GET shows the declared values. (2) `refresh` then `preview --expect-no-changes` (rule 1). (3) A raw PUT changes an **unmanaged** field; the next step changes managed field A and drops managed field B; after `up`, a raw GET shows A changed, B at its default, and the unmanaged field kept (rule 2, G-Q1). (4) A raw PUT changes a managed field; `preview` shows exactly that diff. (5) `pulumi import` one object by natural key into a fresh stack; `preview --expect-no-changes` (rule 3). (6) `destroy`; a raw list holds none of the e2e objects. |
| 8. Teardown | `trap` → `docker rm -f`, remove temp dirs. |
| Budget | about 2 min build (cached), 3-5 min boot, 3 min test; hard timeout 20 min. |

Any gate the implementation cannot run is reported **NOT RUN**, never assumed.

## 12. Risks

| Risk | Mitigation |
|---|---|
| The Go port diverges from the measured prototype | `make uos-parity` on 5.1.15 and 5.1.42; the prototype numbers are the bar |
| `archive/zip` cannot open the ELF-prefixed ZIP | Find the EOCD manually and read through an `io.SectionReader` |
| `X-API-Key` on private routes through the edge is unproven (may need a session login) | Slice 1 proves the direct, unauthenticated path only; the docs say so; edge auth is the slice-2 key-minted tier |
| Pre-wizard behaviour differs from a set-up controller | Stated; slice 2 runs against a seeded, wizard-completed controller |
| Implicit requirements and business rules surface only as 500s | Each e2e failure becomes a `kinds.yaml` entry with `source:` |
| A dropped field with no known default blocks an update | Intended (frame): Check errors and names the field; the fix is a `defaults:` entry |
| The private surface is not byte-gated by the golden | `tokens.txt` + `digests.txt` + private surface lint; folded into the contract at the step-5 rebase |
| The private gate is skipped silently | `UNIFI_REQUIRE_UOS_MODEL=1` turns the skip into a failure; set by `private-check` and `test-e2e-private` |
| The dispatcher bypasses framework features (429 retry, pagination) | The private client reuses `transport.go`; v2 lists in the slice are unpaginated |
| A version bump renames obfuscated classes | The extractor anchors on annotations and real field names; override names that no longer resolve fail gen; the digest diff is reviewed on bump |
| A privileged host and an 875 MB first download | Local/offline tier with a SHA-keyed cache, like the existing Tier 2 |
| Licence | Only editorial facts are committed; `TestNoUbiquitiArtifacts`; publication left to the owner |

## 13. Make targets added

`uos-fetch`, `uos-model`, `private-check`, `uos-parity`, `test-e2e-private`; `PRIVATE=1` switches
`generate_schema`/`build`/`python_sdk` to the merged surface. Existing targets keep their behaviour at
`PRIVATE=0`.

## 14. Deviations recorded during implementation

### Step 1: extractor, Kind model, overrides (built and measured)

**Measured result.** The Go extractor (`provider/pkg/uos`, standard library only, no JDK, no
Python) reads the pinned installer in about 16 s and gives the same model twice. On both 5.1.15 and
5.1.42, read straight from the installers, `make uos-parity` finds every route the prototype finds
(614 and 622) and reaches the prototype's spec bar: 73/73 routes, 157/161 request fields, 154/157
required, 8/12 enums. 813 of the prototype's 814 request-field facts agree on the 31 (30) CRUD
collections, and the 4 slice kinds match exactly. The one difference is a prototype bug (see 3 below).

| # | Design said | Implementation | Why |
|---|---|---|---|
| 1 | §6: `expose:` entries carry `itemGet` | `itemGet:` is its own section; `expose:` has no `itemGet` | The 405 measured on `acl-rules` and `static-dns` (which are not exposed) is recorded too. Every entry has `source:` |
| 2 | §3: "`itemGet` is not in the bytecode" | The model sets `itemGet` from the route table: is there a GET on the item path? The overrides record the 405s that were measured live | The route table has no item GET for exactly the 4 kinds measured at 405. `TestPrivateSliceKinds` checks that the two sources agree |
| 3 | §2: a port of `routes.py` and `dto.py --crud` | The port works on parsed class files and improves on the prototype in three measured ways: (a) handlers that declare `throws` are found (9 more routes on 5.1.15, 10 on 5.1.42); (b) every custom-validator message is kept, as a sorted list, where `dto.py` kept one, picked by Python set order; (c) enum constants come only from `ACC_ENUM` fields, which drops a bogus value the prototype read from a `wan-slas` constant. A `@Value.Default` counts as a constant only for `<const>; [boxing valueOf;] return`. Anything else is `defaultComputed` | Correctness. The parity test accepts (b); (c) is the one logged difference |
| 4 | §3: Kind `{name, api, collection, itemPath, verbs, itemGet, idField, envelope, polymorph?, fields}` | Also `createPath` (`content-filtering/create`-style creates), `listGet` (`network-members-group` has no list GET) and `createList` (`firewall-app-blocks` POSTs a list). Paths are relative to the API's site base | The binding needs all three. `network-members-group` import-by-listing must find another way to list (slice-1 engine) |
| 5 | §3: Field `{wireName, type, enum[], items?, fields?, pattern?, constraints{len,size,custom}, required, default?, readOnly, secret, source}` | Also `values` (map values), `polymorph` on object fields (for example `qos-rules` `source`/`destination`), `optional` (`Optional<>`), `defaultComputed`, `writeOnly`. Constraints also hold `min`, `max` and `nonEmpty`, and `custom` is a list. Types also include `date`, `datetime` and `any` (a library type maps to `any`) | Facts present in the bytecode. The generator decides how to surface them. No obfuscated class name enters the model |
| 6 | §3: `source: {uosVersion, networkVersion, installerSha256, extractorVersion}` | Also `integrationSha256` | The pin tie can then be checked from the model alone |
| 7 | — | Classic kinds: `verbs` is empty, and `listGet`/`itemGet` are false. `collection` is `rest/<lowercase name>`, or `rest/setting/<section>` for settings sections | `api/fields` does not state them. Unknown until the slice-2 write probe |
| 8 | §5 twin list | `officialTwins:` entries also list the official golden tokens that serve the kind (`official:`). A test checks them against `provider/pkg/gen/testdata/tokens.txt` | Turns "an official resource serves it" into a fact a test can check. **Corrected:** this row first said the official side serves ACL rules only as functions. It does not: the official resources `TrafficMatchIpv4` and `TrafficMatchMac` have full CRUD on `/v1/sites/{siteId}/acl-rules` (`mappings.yaml` gives `acl-rules` the `TrafficMatch` prefix), so `acl-rules` is a true twin and ACL writes are covered |
| 9 | §11: `private-check` covers the digests, `tokens.txt`, the surface lint and the pin tie | In step 1 it covers the model only: canonical form, the `model.json` digest, the pin ties, `kinds.yaml` resolving against the model, the slice kinds' facts, and extract-twice determinism. `tokens.txt`, the other digests and the surface lint come with the generator (step 2) | They need the generator's output |
| 10 | §2: `TestNoUbiquitiArtifacts` (no file named) | `provider/pkg/uos/licence_test.go`, in `make test`. It checks tracked files and untracked, unignored ones. A `.class` file is allowed only under `provider/pkg/uos/testdata/classes/` and only next to its own source. The fixture build compiles against self-written annotation stubs (`testdata/java/stubs`, same names as Spring, Jackson, Jakarta, Hibernate, Immutables and Swagger) whose classes are not committed | Fixtures must be our own bytes |
| 11 | §11: model-backed tests "log `NOT RUN (no model)`" | They run only when `UNIFI_UOS_MODEL` (and, for determinism, `UNIFI_UOS_INSTALLER`) is set. `make private-check` sets both, plus `UNIFI_REQUIRE_UOS_MODEL=1`. A plain `make test` skips them with `NOT RUN`, whatever local files exist | A stale local model must not break `make test` |
| 12 | §11: `uos-parity` takes `UOS_ORACLE_CRUD=<path>` | It also takes `UOS_ORACLE_ROUTES=<routes.tsv>`. The classes come from the installer (`UOS_INSTALLER`), or from the prototype's `classes/` directory (`UOS_ORACLE_CLASSES`) | Route parity is measured too |

### Step 2: codegen and runtime binding (built and measured)

**Measured result.** `make PRIVATE=1 build python_sdk` turns the pinned model into 4 resources
(`tokens.txt`), 4 object types and the `networkBaseUrl` config key in `schema.merged.json`, the
engine's kind table in `private.json`, and `pulumi_unifi.network` in the SDK. Both files match
`digests.txt`, and two generations are byte-identical. At `PRIVATE=0`, `schema.merged.json` is
`schema.json` byte for byte and `private.json` serves no kinds. A manual smoke run (not a committed
gate; `make test-e2e-private` is step 3) drove the Pulumi CLI 3.230.0 and the `PRIVATE=1` SDK against
UniFi OS Server 5.1.15 before the wizard, through `test/e2e-private/fwd`. `up` created all 4 kinds
with `private:` ids, and `refresh` then `preview --expect-no-changes` was clean. A raw PUT to
unmanaged `enabled` and `download_burst`, followed by an `up` that changed `download_limit_kbps` and
dropped `destination.port_matching_type`, kept both unmanaged values and reset the dropped one to its
model default (`ANY`). A raw PUT to a managed field showed as exactly that diff. `pulumi import` by
natural key worked, and `destroy` left no objects.

| # | Design said | Implementation | Why |
|---|---|---|---|
| 13 | §6 sections | A `listPath:` section names a collection GET that lives elsewhere: `network-members-group` lists at `network-members-groups` (route table, then confirmed live). With an entry, `listGet` is true | Import by natural key and list + filter need a list (step-1 open issue) |
| 14 | §5: "Gen error if an exposed kind has `polymorph`" | Still true for a body-level union. A nested union on an object field (`qos-rules` `source`/`destination`) becomes one object type holding the base fields plus every variant's fields, all optional. Two variants that define a field differently are a gen error | The slice needs `qos-rules`. Pulumi tagged unions stay deferred |
| 15 | §5: Required = extracted + `implicitRequired` + natural key | Extracted required counts only when the field has no decoded default, because the controller fills a defaulted field | `matching_target` is `@NotNull` with default `ANY`. A required input would make users restate the default |
| 16 | §8: drop to "its default (model or overrides)" | A field the model marks `Optional<>`, with no other default, resets to `null`. Any other field without a default is still a Check error (G-Q1) | `Optional.empty` is the Java "off". Recorded in the schema description ("Removing it clears it.") |
| 17 | §5: read-only fields are outputs only | Top-level read-only fields are outputs. Nested read-only fields are left out of the shared object types | The object types serve both inputs and outputs. None of the slice kinds has a nested read-only field |
| 18 | §8 Check: "values read back are never rejected" | Check validates a leaf only when it differs from the old input at the same path. After an import or a refresh, the old inputs are what the controller returned | This is how rule 1 is enforced |
| 19 | §8 Diff | The detailed diff is keyed by dotted property path (`destination.portMatchingType`), so a nested drop shows at its own path, not as a delete of the whole object | Accurate previews |
| 20 | §8 Update: "deep-merge onto a fresh read, full PUT" | The PUT body is the fresh read without read-only fields, with the id kept and unmodelled keys left as the controller holds them. Dropped paths are set to their defaults, then the new inputs' managed paths go on top. A new leaf value replaces any subtree under it | The controller accepted this on all 4 kinds |
| 21 | §8 Create | After the POST the object is read back (item GET, or list + filter), so the outputs hold the controller's defaults. When the response carries no id, the object is found by natural key | `qos-rules` returns server-filled `index`, `download_burst` and `upload_burst` |
| 22 | §7: `apiKey` "no auth header when unset" | When private kinds are served and no `apiKey` is set, the dispatcher configures only the private client. Official calls then fail with the framework's "API key is required" message. At `PRIVATE=0`, Configure is forwarded unchanged | The framework callback rejects an empty key. The e2e runs without one |
| 23 | §2 dispatch | `provider.Serve` takes a `Wrap`, and the plugin always wraps the framework with `private.NewDispatcher`: one binary shape. GetSchema returns `schema.merged.json`. The framework still runs on the golden. `transport.go` exports `InsecureTransport()` for the private client | §4 "one binary shape" |
| 24 | §11 `make test` | Tier-1 adds engine, binding and dispatcher tests against `internal/fakectl`, an in-memory v2 controller with synthetic kinds (`Thing`: item GET, `_id`, nested defaults, a secret; `Group`: 405, `id`, another list path, list create). Generator tests run on a synthetic model, and `TestNoModelIsGolden` checks the `PRIVATE=0` byte identity. `make test-sdk` also runs `test/sdk/test_private_smoke.py`: it skips at `PRIVATE=0` and is required at `PRIVATE=1` | Request shapes and rules are tested without Docker |
| 25 | §11 `private-check` | It also runs `provider/pkg/private/gen` `TestPrivateSurfaceGolden`: the `schema.merged.json` and `private.json` digests, `tokens.txt`, the private surface lint (token grammar, golden entries unchanged, property names, secret-looking names flagged, schema and kind table agree, Pulumi `ImportSpec` binds) and two-run determinism | Completes step-1 deviation 9 |
| 26 | §11 step 3 (Boot) | **Live finding:** the image's Network app requires a **version-5** `UOS_UUID`. A v4 UUID from `/proc/sys/kernel/random/uuid` (the research recipe) fails its `systemService` bean with "Invalid uuid version - 4", and `:8081` never opens. The smoke run used `uuid5(NAMESPACE_DNS, "pulumi-unifi-e2e-private")` | Step 3's `run.sh` must generate a v5 UUID |
| 27 | §2 `test/e2e-private/fwd` | Committed in step 2 (used by the smoke run) | — |

### Step 3: the live e2e tier (built and run green)

**Measured result.** `make test-e2e-private` (`test/e2e-private/run.sh`) builds the `PRIVATE=1`
plugin and Python SDK from the pinned installer, `docker load`s the same installer's image, boots it
fresh with no wizard and no key, and drives the Pulumi CLI 3.230.0 through `e2e_test.go`. All 6 steps
of §11 step 7 pass on all 4 kinds: create, refresh + no-op preview + a second no-op `up`, an update
that keeps unmanaged fields and resets dropped ones, managed drift shown as exactly one property diff,
import by natural key into a fresh stack with a no-op preview, and destroy with nothing left.

| # | Design said | Implementation | Why |
|---|---|---|---|
| 28 | §8 Import: "Managed paths after import = natural key + the import's `properties`" | **Live finding:** Pulumi 3.230.0 does not pass an import's `properties` to the provider, nor narrow the stored inputs to them. With every writable field returned as inputs, an imported ApGroup carried `externalId` and `forWlanconf`, and a program that left them out failed Check (G-Q1: dropped, no default). Now an import adopts the **natural key plus the required fields** (nested: required within required). `Engine.DiffLive` (the dispatcher passes the old outputs) treats a path the new inputs start to manage as no change when its value equals what the controller returned, so a program that declares more than the import adopted, at the live values, previews clean | Rule 3 must work with the CLI as it is. Adopting a field at its live value changes nothing (rule 1). Tier-1: `TestImportByNaturalKey` |
| 29 | §10 ApGroup | The e2e updates ApGroup by renaming it (its natural key; an in-place update). `device_macs` stays `[]` | **Live finding:** the controller rejects MACs of devices it has not adopted (`api.err.InvalidDeviceInApGroup`), and `for_wlanconf: true` needs devices (`api.err.ApGroupForWlanMustHaveDevices`). A fresh controller has no devices |
| 30 | §11 step 6: `go test ./test/e2e-private/...` | `test/e2e-private` is its own Go module (standard library only; it also holds `fwd/`). `run.sh` runs `go test -tags e2e_private .` there | The provider module, `make test` and `go vet ./...` never see this tier |
| 31 | §11 step 2: load and tag `uosserver:<pin>` | The tag is `uosserver:uos-<version>-<sha256[:12]>`. The load is skipped when that tag exists (`E2E_RELOAD=1` forces it). `image.tar` is written into the cache by `uos-extract -image-out` when it is missing | The tag is only ever made by `run.sh` from the pinned installer's own `image.tar`, so it names the pin. Saves a reload of 865 MB on every run |
| 32 | §11 step 7 (4): "`preview` shows exactly that diff" | `preview --refresh --json`: exactly one change, an `update` of that resource whose `detailedDiff` holds only that property. Then `up --refresh` restores it | A plain `preview` does not read the controller, so it cannot see the drift |
| 33 | §11 step 7 (3) | The unmanaged fields changed by raw PUT are QosRule `enabled` and `download_burst` and FirewallAppBlock `client_macs`. The dropped fields are QosRule `destination.port_matching_type` (back to `ANY`, the model default) and `destination.port` (an `Optional`, cleared). Every kind changes a managed field. `kinds.yaml` `defaults:` stays empty: none was needed | These are the fields a fresh controller accepts without devices or set-up |
| 34 | §11 step 7 (5): import one object | Three: ApGroup by bare key, NetworkMembersGroup by `name=…`, FirewallAppBlock with no `properties`. A missing key must fail with the zero-match error | Covers both key forms and the DiffLive path |

# Part 2: Slice 2 and official ACL writes

Scope is the owner's four decisions of 2026-11-28 plus slice 2 as the frame defines it:

1. ACL rules are written through the **official** API (`/v1/sites/{siteId}/acl-rules`), proven live.
2. An official **ACL rule ordering** resource on `GET/PUT /v1/sites/{siteId}/acl-rules/ordering`.
   The per-rule `index` is deprecated and has no effect; first match wins, so order is part of the IaC
   contract.
3. **IPv6 ACL rules** as a private kind, the one documented exception to whole-kind serving (frame
   G-Q4). The plan builds through slice 2, because IPv6 ACL rules are only useful once private writes
   work through the authenticated edge.
4. Rename `TrafficMatchIpv4`/`TrafficMatchMac` to `AclRuleIpv4`/`AclRuleMac`, with aliases so state
   migrates without replace, acknowledged through the breaking-delta mechanism.

Slice 2 proper: (a) private writes through the authenticated edge with the same `apiKey`/`apiHost`/
`allowInsecure`; (b) post-wizard behaviour on the seeded, key-minted controller; (c) classic
`/api/s/{site}/rest` kinds (first `usergroup`) and at least one settings singleton; (d) the IPv6 kind.

Evidence: the "Slice 2 probe (authenticated edge, seeded controller)" section of
`docs/research/private-api-model-source.md` (cited below as probe a1-f2).

## S2.0 How this was chosen

Two designs were scored 1 (poor) to 5 (strong).

| Criterion | M: minimal risk (hand-written ordering handler, engine stays private) | D: domain layers (one engine, per-API bindings, singleton and variant as Kind facts) |
|---|---|---|
| Fit to the frame and owner decisions | 3: serves an IPv6 token whose every create fails on the pin; ordering bypasses "one engine, two bindings" | 4: frame step 4 one kind at a time; but its `relativeOrder` lets an unmanaged rule move ahead of managed ones with no drift, which misses decision 2 (first match wins) |
| Layering and domain boundaries | 3: a second, ad-hoc lifecycle in `official/ordering`; the engine stays named "private" | 5: engine never sees HTTP or which API; singleton, variant and merge strategy are data; a shared controller-access layer |
| Live testability | 4: one boot for both tiers; keeps the pre-wizard tier unchanged | 4: one shared harness; but it moves `test-e2e-private` onto the seed, losing the only seedless tier |
| Determinism and CI risk | 4: small, local changes | 4: an engine move and a new surface file, both mechanical and gate-covered |
| Licence safety | 5 | 5 |
| **Total** | 19 | **22** |

**Winner: D.** Grafted from M:
- the ordering contract: the program's rules form the **leading block**, in declared order, and drift
  is the live prefix differing from the input (§S2.5), replacing D's `relativeOrder`;
- `test-e2e-private` stays exactly as it is (pre-wizard, `:8081`, no key) and the edge tier is
  additive (§S2.10);
- the discovery stub as the probe proved it: our own few lines of JavaScript run with the image's node
  (§S2.10);
- lazy site resolution, with no network call when the form given is the form needed (§S2.6);
- `createDefaults:` for create-only fields the server requires (`acl_index`, probe c1) (§S2.7);
- `mgmt` not exposed, plus a gen lint that an exposed classic `x_` field must be secret (§S2.8);
- the "served outside the framework" set in `TestCRUDMapKeysAreLiveTokens`, read from `singletons:`;
- the loud checks on aliases (§S2.4) and the variant read check over the unfiltered list (§S2.7);
- a classic 5xx is surfaced as a short error with the server's message, not an HTML page.

## S2.1 Goals and non-goals

| Goals (slice 2) | Non-goals (deferred, with the step or slice that owns them) |
|---|---|
| `AclRuleIpv4`/`AclRuleMac` (renamed, aliased) proven live: create, no-op, update, delete, and state migration from the old tokens | Rebuilding the other 21 framework resources on the engine (frame step 4, one kind at a time after this) |
| `unifi:sites/v1:AclRuleOrdering`, served by the engine through an official binding, at `PRIVATE=0` | `firewall/policies/ordering` (fits `singletons:` unchanged; its own PR) |
| One engine and per-API bindings (`official`, `v2`, `classic`) over one controller-access layer | Per-field serving (Q4 B). Probe a3 shows the official ACL PUT replaces the whole rule, so B is unsafe there |
| Private v2 and classic writes through the authenticated edge with the one `apiKey`; no login | A session login or CSRF handling (not needed, probe b4); an `Authenticator` is built only if a later pin refuses the key |
| The 4 slice-1 kinds proven post-wizard on the seeded controller | Re-resolving stale ids across APIs; `mac:` ids (classic `user`) |
| Classic `unifi:network:UserGroup` and the settings singleton `unifi:network:SettingNtp` | Settings sections with secrets (`mgmt`), other classic kinds (one per PR after this) |
| `unifi:network:AclRuleIpv6` built and Tier-1 tested; served from the first Network version that accepts IPv6 ACL rules | Serving it on Network 10.4.57: the server refuses IPv6 ACL rules (probe c1) |
| A version guard for private kinds (`stat/sysinfo` vs the pin) | Resetting settings on destroy (their defaults are not measured) |
| All existing gates green, plus `make test-e2e` (live ACL and ordering) and a new `make test-e2e-edge` | Publishing a private-capable SDK or plugin (Phase 5, owner) |

## S2.2 Layers and their boundaries

```
L5 Routing          provider/pkg/dispatch     token -> surface entry -> engine; everything else -> framework
L4 Surface (data)   surface.json (generated)  the kinds the engine serves, each with api: official | v2 | classic
L3 Engine (domain)  provider/pkg/engine       rules 1-3, G-Q1, natural key, stored id, singleton, variant;
                    provider/pkg/kind         the Kind/Field model it executes (exists)
L2 Bindings (wire)  provider/pkg/binding/{official,v2,classic}   paths, envelopes, status -> outcome
L1 Controller       provider/pkg/controller   one HTTP client (TLS policy, X-API-Key, 429 retry),
   access                                     endpoint roots from apiHost, SiteResolver, version probe
```

- **The engine never sees HTTP, paths, envelopes or which API it talks to.** It calls a `Binding`
  (`List`, `Get`, `Create`, `Replace`, `Delete`, `API()`), and the stored-id prefix comes from
  `Binding.API()` (`official:`, `private:`), not from a constant. A binding never decides what to send;
  it maps wire outcomes to domain outcomes (`gone`, `conflict`, `invalid{field}`).
- **New domain facts, each implemented once in the engine as data:** `Kind.Shape = collection |
  singleton`, `Kind.Variant = {field, value}`, and `Field.Merge = replace | leadingBlock`. No kind is
  special-cased by name.
- **Moves (mechanical, step 2):** `private/engine` → `engine`; `private/binding` → `binding/v2` +
  `binding/classic`; the shared transport parts of `provider/transport.go` → `controller`;
  `private/dispatch.go` → `dispatch`. `provider/pkg/private` keeps what is private: the overrides,
  the private gen and the model checks. `internal/fakectl` moves beside the bindings it fakes.
- **The surface file.** `private.json` becomes `surface.json`: every engine-served kind, tagged with
  its `api`. At `PRIVATE=0` it holds only official engine kinds, generated from the public spec and
  `mappings.yaml` (today: `AclRuleOrdering`); at `PRIVATE=1` the private kinds are added. The plugin
  still embeds five files. `schema.merged.json` at `PRIVATE=0` stays byte-equal to the golden.
- **The framework keeps its own client**, configured from the same config. Engine-served kinds get
  `controller`'s 429 retry on both TLS paths; the documented caveat (the insecure path loses 429 retry)
  remains for framework resources only.

## S2.3 Official ACL rules: rename, aliases, required inputs, `index`

| Item | Decision | Reason |
|---|---|---|
| Rename | `mappings.yaml` `entityPrefixes`: `/v1/sites/{siteId}/acl-rules: AclRule`. `traffic-matching-lists` keeps `TrafficMatch`. The existing `pass_token_rename` rewrites resources, the CRUD map and the auto-name map together. `descriptions:` keys move to `AclRuleIpv4`/`AclRuleMac`. | One editorial fact in the layer that is already data. The functions are already `getIpAclRule`/`getMacAclRule`/`listAclRules`. |
| Aliases | New `mappings.yaml` section `aliases:` (`unifi:sites/v1:AclRuleIpv4: [unifi:sites/v1:TrafficMatchIpv4]`, and Mac). New deterministic pass `pass_aliases.go`, after the rename, sets `ResourceSpec.Aliases = [{type: <old>}]`. Loud errors: the new token is not a live resource; an alias equals a live token; two tokens share an alias. | Aliases are contract, so they sit in the golden beside the rename. `gen-sdk` emits `pulumi.Alias(type_=…)` into the generated `opts`; the engine resolves them before calling the plugin, which only ever sees the new token. |
| Old class names | Removed. No deprecated duplicate token. | Two tokens on one CRUD path break the 1:1 rule of `TestCRUDMapKeysAreLiveTokens` and rule 3. Nothing is released (Phase 5). |
| `networkIdFilter` | Generic gen fix: lift each `allOf` member's `required` list into the variant's `requiredInputs`. A unit test with a synthetic spec covers the mechanism; the golden shows every effect. Any other input it makes required stays only if the spec states it required, and gets its own Breaking bullet. | Probe a2: the server returns 400 "networkIdFilter must not be null"; today a program passes preview and fails at `up`. |
| `index` | Output only on both resources, through a new `mappings.yaml` `outputOnly:` list (`token.property`, dead entries fail). Its description points to `AclRuleOrdering`. | It has no effect, and the ordering PUT renumbers it (probe a5). The rename already forces program edits, so removing it in the same change costs users nothing extra. |
| Delta gate | `detectBreakingDelta` follows aliases: a base token missing from the current schema is compared with the current token that aliases it. A removal with no alias stays with the byte golden and `tokens.txt`. Unit test: a synthetic rename that also adds a required input is flagged. | Today it skips removed tokens, so the rename would hide `added-required-input AclRuleMac.networkIdFilter`. |
| Acknowledgement | `CHANGELOG.md` `## [Unreleased]` `### Breaking`, three bullets: (1) the rename, state carried by aliases, Python class names changed; (2) `AclRuleMac.networkIdFilter` required; (3) `index` is no longer an input. Then `make schema` regenerates the golden and `tokens.txt`; `kinds.yaml` `officialTwins` for `acl-rules` names the new tokens. | The repo's existing mechanism (`TestNoUnacknowledgedBreakingDelta`); the private twin lint catches a stale twin token. |

Probe facts kept as documented behaviour, not code: the official PUT replaces the whole rule and needs
`type` (the framework already sends the full input set and the variant's const); a PUT may change MAC
to IPV4 in place with the same id (out-of-band; a refresh shows it as drift of the variant's fields).

## S2.4 Aliases: what migrates and how it is proven

State migrates because the aliased old type, plus the unchanged name and parent, yields the old URN;
the engine renames it in place, with no create or delete. Proven live in `make test-e2e` (§S2.11):
`up` an `AclRuleIpv4`, `stack export`, rewrite its type and URN to `TrafficMatchIpv4`, `stack import`,
then `preview --expect-no-changes` with the new program and an `up` that reports no create or delete.
If it fails, the rename is held and the doc records `pulumi state` steps instead.

## S2.5 `unifi:sites/v1:AclRuleOrdering`

| Item | Decision | Reason |
|---|---|---|
| Schema source | `/acl-rules/ordering` **stays** in `excludedPaths` (pulschema would emit at most a data source). A new `mappings.yaml` section `singletons:` holds `{path, token, property: orderedAclRuleIds}`; a gen pass builds the `ResourceSpec` from the spec's own ordering body schema into the golden and writes the kind into `surface.json` (`api: official`, `shape: singleton`). Cross-checks: the path must be in `excludedPaths`; the token must not be in the framework CRUD map; `TestCRUDMapKeysAreLiveTokens` reads its "served outside the framework" set from `singletons:`. | Generated from the public spec, in the `PRIVATE=0` surface, with no hand-written schema. The same entry shape fits `firewall/policies/ordering` later. |
| Inputs and outputs | Inputs: `orderedAclRuleIds: string[]` (official rule ids, `merge: leadingBlock`), `unlisted: LAST \| REJECT` (default `LAST`), `siteId` (replace on change, like every resource). Output: `effectiveOrder: string[]` (the full live order). | Users list the rules they manage; Pulumi's dependency graph then orders operations: rules are created before the ordering; a rule removed from the program is first dropped from the ordering (it lands in the unmanaged tail), then deleted. |
| Contract | The listed rules form the **leading block** of the site's order, in the declared order. Unlisted rules follow in their current relative order (`LAST`). `REJECT` fails the update and names every unlisted rule. | Decision 2: first match wins, so the program's rules must take precedence; this is also the only order a program can own without owning other people's rules. |
| Identity | One per site. Stored id `official:<siteUUID>`. Import id: the site's name or UUID. | Rule 3: a stored id names its API; a singleton's natural key is its site. |
| Create | Adopt: GET the live order, compose, PUT. Never fails because rules exist. | Nothing can be created. |
| Compose (wire) | PUT body = the managed ids, then every other live id in live relative order. Duplicate or `private:` ids fail Check (the latter names the private kind's `aclRuleId` output). An id that is not live passes Check; the PUT's 400 `invalid-reference` is shown verbatim. On 400 `not-all-acl-rules-specified` the engine re-reads once and retries, then fails. | Probe a5/d2: the PUT must list every user-defined rule, including unmanaged and private-created ones. One retry covers a rule created elsewhere between GET and PUT. |
| Read and Diff | Projected input = the first *n* live ids, *n* = the number of managed ids. Drift shows when an unmanaged rule moves ahead of a managed one, when managed rules are reordered, or when a managed id is gone. Diff is element-wise; any change is an in-place update. | Rule 1 for a list whose position is managed. A private create can leave a duplicate `acl_index` (probe d2); that never shows here, because the official GET returns an order, not indices. |
| Delete | Release: no call, the live order stays, a warning is logged. | There is no "unordered" state, and a reset would reorder other people's rules. |

## S2.6 Controller access and the authenticated edge (slice 2a)

| Item | Decision | Reason |
|---|---|---|
| Auth | One `apiKey`, sent as `X-API-Key`, for official, v2 and classic calls. No login, no cookie, no CSRF token. | Probe b2-b4: key-only writes succeed through the edge; no `Set-Cookie`, no `X-Csrf-Token`; no or a wrong key → 401 everywhere. Frame: "a login only if refused". |
| Roots | From `apiHost`: `https://<apiHost>/proxy/network/integration/v1`, `…/proxy/network/v2/api/site/{name}`, `…/proxy/network/api/s/{name}`. | The edge serves all three. |
| `networkBaseUrl` | Stays an override of the Network root only (direct `:8081`, pre-wizard), private fragment only, so the golden is unchanged. Its default is the edge. The "private kinds without a key" path in `Configure` stays, documented as pre-wizard only. | Probe b5: after the wizard `:8081` answers 401; the edge is the product path. The override keeps the seedless tier. |
| Site | `siteId` takes the site name or UUID. `controller.SiteResolver` resolves lazily: no call when the form given is the form needed; otherwise one `GET /integration/v1/sites` (`internalReference` = name, `id` = UUID), cached. When `siteId` is a name, the dispatcher forwards the UUID to the framework. With no key, it falls back to slice-1 behaviour (siteId used as given). | Frame §2 Site row; probe a1/e. A name on official paths fails today, so forwarding the UUID is additive; a UUID-configured stack (and the Prism mock) makes no extra call. |
| Version guard | On the first private-kind call, `GET …/api/s/{name}/stat/sysinfo` and compare the Network version with the pin's `networkVersion`. A mismatch fails the private kinds only, with both versions in the message. Official kinds are not guarded. | Frame: one controller version per release; verifiable now (probe b3). |
| Secrets | The key is never logged. Model fields marked sensitive, and every classic `x_` field, are secret, failing closed. | Probe f2: `mgmt` returns `x_` credentials in plain text. |

## S2.7 `unifi:network:AclRuleIpv6` and the G-Q4 variant exception (slice 2d)

**Exception text (to be added to the frame and kept here):**
> **G-Q4 variant exception.** Kinds are served whole, with one exception. When the official API serves
> some variants of a discriminated kind and not another, that variant alone may be served by the
> private API as its own token, provided that: (1) the discriminator is pinned (`variant:`) and is
> enforced on write, list and read; (2) objects of both APIs share one id namespace for relations
> between kinds, and the private token outputs that id; (3) generation fails once any official token
> serves the variant value, and the kind then moves to the official API with an alias. First and only
> use: ACL rules of `type: IPV6` (the official API serves IPV4 and MAC).

| Item | Decision | Reason |
|---|---|---|
| Token and ids | `unifi:network:AclRuleIpv6`. Natural key `[name]`. Stored id `private:<_id>`. Output `aclRuleId` = `external_id` = the official rule id. | Probe e: official id = private `external_id`. `aclRuleId` is what `AclRuleOrdering` takes. |
| `kinds.yaml` | `expose: {kind: acl-rules, token: AclRuleIpv6, naturalKey: [name], variant: {field: type, value: IPV6}, servedFrom: <networkVersion>, source: …}`; `officialTwins` `acl-rules` gains `officialVariants: {field: type, values: [IPV4, MAC]}`; new section `createDefaults: {kind: acl-rules, fields: {acl_index: 0}}` (sent on create only, then never managed); `implicitRequired` for the traffic source and destination endpoints, each with `source: "slice 2 probe c1"`. | The model's `type` is a plain enum, not a polymorph, so the variant is a field pin. `acl_index` is required on create (probe c1) but its position is owned by `AclRuleOrdering`. |
| Gen checks | Exposing an official twin is a gen error unless the entry has `variant:`, its value is in the field's enum, and it is not in `officialVariants`; and the golden's twin tokens' discriminator consts (IPV4, MAC) must equal `officialVariants`. A new official IPV6 variant therefore fails gen. The kind is generated into the schema only when the pin's `networkVersion` ≥ `servedFrom`; with `servedFrom` absent it is not served and gen prints `NOT SERVED (servedFrom unset)`. | The exception enforces itself, and ends itself when the official API catches up. |
| Schema | `type` is not an input; `acl_index` is output only; the discriminator is create-only. | The binding owns them. |
| Engine | Create and PUT bodies carry `type: IPV6`; create adds `createDefaults`; update keeps the live `acl_index` (rule 2 merge onto a fresh read). List and natural-key lookup keep only `type == IPV6`. Read by id searches the **unfiltered** list, then checks the variant: a rule whose `type` changed is a loud error naming the official token that now serves it, never "gone". | Probe a3: the official PUT can change a rule's type in place; "gone" would make Pulumi create a duplicate in a first-match list. |
| On 10.4.57 | **Not served.** `servedFrom` stays unset until a probe measures a Network version that accepts IPv6 ACL rules. | Probe c1: create, or a PUT to IPV6, returns 500 "IPv6 ACL is not supported yet"; an IPv6 subnet fails validation. Shipping a token whose every create fails is worse than not shipping it. |
| Unblocking | Step 6a (§S2.12) probes UniFi OS Server 5.1.42 / Network 10.5.67: IPv6 create, read, update, delete, and whether official list and ordering include an IPv6 rule (if ordering omits it, or refuses PUTs while one exists, that is recorded and `AclRuleOrdering` documents it). If it works, serving needs a pin bump (official spec, golden, private model, seed): an **owner decision**, done as its own step. | The two pins are tied (§4); a bump moves the whole contract. |

## S2.8 Classic kinds and the settings singleton (slice 2c)

| Item | Decision | Reason |
|---|---|---|
| Binding semantics (`binding/classic`) | `{meta,data}` envelope; create answers 200 with `data:[obj]`; GET answering 200 with `data:[]` is gone; an `rc:"error"` body becomes a typed error with `msg` and `validationError{field,pattern}`; a DELETE answering 400 `api.err.IdInvalid` is gone only after a list confirms the id is absent; a 5xx becomes a short error with the server's message. The engine still sends its merged full body, though the server merges a PUT. | Probe f1/f2. Wire semantics stay out of the engine. Confirming IdInvalid by list keeps a malformed id from passing as success. |
| Natural-key uniqueness | Generic engine rule for collection kinds: Create lists by natural key first and fails with "already exists; import it (id …)" on a match. | The classic API accepts duplicate names (probe f1), which would silently break rule 3. It is also correct for v2 kinds. |
| First kind | `unifi:network:UserGroup` (`rest/usergroup`), natural key `[name]`. `qos_rate_max_*` uses `-1` for "off", recorded as a `defaults:` entry with `source: live`. | Full CRUD measured (probe f1); no device needed. |
| Settings singleton | `unifi:network:SettingNtp` (`rest/setting/ntp/{_id}`), `shape: singleton`, keyed by the setting's `key` in the site. Create adopts (GET the one document, merge the managed fields, PUT); Delete releases with a warning (the §S2.5 lifecycle, unchanged). A dropped managed field resets to its default on update (G-Q1). Stored id `private:<_id>`; import id `ntp` or `private:<_id>`. | Probe f2: merge PUT, bad-enum 400, restore by re-read; no secrets in the section. Reusing the singleton shape proves it is not tied to ordering. |
| Secrets lint | Gen fails if an exposed classic kind has an `x_` field that is not secret. `mgmt` is not exposed in slice 2. | Probe f2. |

## S2.9 Stored ids and tokens, summarized

| Token | Served by | Stored id | Import id |
|---|---|---|---|
| `unifi:sites/v1:AclRuleIpv4`, `AclRuleMac` | framework (official) | rule UUID (unchanged) | rule UUID |
| `unifi:sites/v1:AclRuleOrdering` | engine, `binding/official` | `official:<siteUUID>` | site name or UUID |
| `unifi:network:AclRuleIpv6` (when served) | engine, `binding/v2` | `private:<_id>` | `private:<_id>` or `name` |
| `unifi:network:UserGroup` | engine, `binding/classic` | `private:<_id>` | `private:<_id>` or `name` |
| `unifi:network:SettingNtp` | engine, `binding/classic` | `private:<_id>` | `ntp` or `private:<_id>` |

Accepted until the frame's step-5 contract rebase: the module split (`sites/v1` vs `network`) and the
framework resources' unprefixed stored ids.

## S2.10 Live harness (slice 2b)

| Item | Decision | Reason |
|---|---|---|
| Image | Tier 2 keeps the pinned `hieutq/unifi-os-server@sha256:3e88…` with the committed seed. Pull from Docker Hub, else `mirror.gcr.io/hieutq/unifi-os-server@<same digest>`. If both fail, a local rebuild from `uosserver:uos-5.1.15-04c8e401eb34` plus our own entrypoint layer is allowed for the local tier, and the run log says so. | Its layers are Ubiquiti's 5.1.15 layers (identical diff_ids) plus one entrypoint layer, so the model and the target are one version. |
| Shared harness | `test/live/` (shell + our own helpers): restore the seed, boot, edge setup, readiness gate, teardown. `test/e2e/run.sh` uses it. `restore.sh` untars with the UniFi OS image's own `tar` instead of `alpine:3`. | One boot recipe; no extra image pull. |
| Sandbox workarounds | `E2E_SANDBOX=auto` (default; `0`/`1` override) turns them on when `/proc/net/if_inet6` is absent: a compose override with `privileged: true` and `extra_hosts: host.docker.internal:127.0.0.1`; a committed ~10-line discovery stub (our JavaScript, `os.networkInterfaces()` on `127.0.0.1:11002`) run with the image's node, then a unifi-core restart; a loop that deletes `listen [::]` lines and runs `nginx -s reload` (never a systemd restart of nginx, whose ExecStartPre wipes unifi-core's site configs), removing a stale `uos-internal.sock` if reload cannot bind. | The probe's measured recipe, moved from hand steps into the harness; off on a normal host. |
| Targets | `make test-e2e`: the existing gRPC test (DnsARecord) plus a new CLI driver (Pulumi CLI 3.230.0, `PRIVATE=0` SDK) for the ACL rules, ordering and alias migration. New `make test-e2e-edge`: `PRIVATE=1` plugin and SDK, the `test/e2e-private` driver in an edge mode on the seeded controller. New `make test-live`: one boot, runs both. `make test-e2e-private` is **unchanged**: pre-wizard, `:8081`, no key. | A boot takes minutes, so one boot serves both drivers. The pre-wizard tier is the slice-1 regression and the only tier that needs no seed (useful at a pin bump). |
| Edge mode config | `apiHost=127.0.0.1:11443`, `allowInsecure=true`, `networkBaseUrl` unset, the site name `default`. The key is read from `test/e2e/seed.env` into the process environment as `UNIFI_APIKEY` only: never on a command line, in Pulumi config, in logs or in the run log. | Hard constraint on the key. |

## S2.11 Test plan

| Gate | New coverage | Kind |
|---|---|---|
| `make test` | alias pass (set; dead; collision; alias = live token); `allOf` required lift (synthetic spec); `outputOnly:` (dead entry fails); `singletons:` cross-checks; CRUD-map test's outside set; alias-aware `detectBreakingDelta` (synthetic rename + added required input flagged); engine tables for singleton adopt/release, `leadingBlock` compose and projection (`LAST`, `REJECT`, missing id, duplicate, `private:` id rejected, one retry on not-all-specified), variant pin (create/PUT injection, filtered list, changed-variant read error), natural-key uniqueness; `fakectl` gains an official ACL rules + ordering surface with its 400 rules and a classic envelope (`data:[]`, `rc:error`, IdInvalid); controller client (key header, 429 retry on both TLS paths), `SiteResolver` (UUID↔name, lazy, no-key fallback), version guard; `kinds.yaml` `variant`/`officialVariants`/`servedFrom`/`createDefaults`/`singleton` parsing and gen rules (twin without `variant` fails; officially served value fails); `x_` secret lint; `TestNoUbiquitiArtifacts`. | mocked, synthetic |
| `make schema-check` / `schema-delta` | Golden regenerated once (rename, aliases, `networkIdFilter` required, `index` output only, `AclRuleOrdering`); `tokens.txt`; ContractLint. With `CONTRACT_BASE_SCHEMA` = main, the delta passes only with the Breaking bullets. | golden |
| Determinism | `make generate` twice with CLI 3.230.0, `diff -r sdk`; new passes iterate sorted keys; `surface.json` at `PRIVATE=0` is a pure function of the spec and `mappings.yaml`. | generated |
| `make test-sdk` | `AclRuleIpv4`, `AclRuleMac`, `AclRuleOrdering` import; `TrafficMatchIpv4` does not; the generated source holds `Alias(type_="unifi:sites/v1:TrafficMatchIpv4")`. At `PRIVATE=1`: `network.UserGroup`, `network.SettingNtp` (and `AclRuleIpv6` only when served). | generated |
| `make test-mock` | Prism: create/update/delete for `AclRuleIpv4`/`AclRuleMac` under the new tokens; ordering GET/PUT through dispatcher → engine → `binding/official` (wire and shape only; Prism keeps no state). | Prism |
| `make private-check` | New `digests.txt` and private `tokens.txt` entries; the variant entry resolves against the model; extract-twice determinism. | installer |
| `make test-e2e-private` | Unchanged, must stay green. | live, pre-wizard |
| `make test-e2e` | **AclRuleIpv4** and **AclRuleMac** (with `networkIdFilter` = the seed network's UUID, listed at run time): create, refresh + `preview --expect-no-changes`, update (including clearing a filter, which proves the full PUT), delete with nothing left. **AclRuleOrdering**: adopt on [r2, r1] (a raw GET shows them leading), no-op, reorder to [r1, r2], an unmanaged rule created raw is kept in the tail, an out-of-band move of an unmanaged rule ahead shows as drift, a v2-created IPV4 rule stays included, destroy releases (order left as is). No drift on the rules after an ordering PUT (`index` is output only). **Alias migration** as in §S2.4. | live, seed |
| `make test-e2e-edge` | The 4 slice-1 kinds through the edge with the key, all 6 driver steps. **UserGroup**: create, refresh no-op, unmanaged drift kept, managed drift shown, duplicate name refused, import by name, destroy. **SettingNtp**: adopt, change one field, no-op, drift, drop-reset, destroy = release, then the harness restores the original values and checks by re-read. **AclRuleIpv6**: only on a pin where it is served; on 10.4.57 not_run. | live, seed |

Every gate that cannot run is reported not_run with the reason.

## S2.12 Build order (every gate green after each step)

1. **Official ACL rules.** Rename, `aliases:` pass, `allOf` lift, `outputOnly:` for `index`,
   alias-aware delta detector, CHANGELOG bullets, golden and `tokens.txt`, `officialTwins` tokens.
   `test/live/` with the sandbox workarounds; `test-e2e` gains the CLI driver with the ACL rule
   round trips and the alias migration. Gates: all, plus `test-e2e`.
2. **Layer move, no behaviour change.** `engine`, `binding/{v2,classic}`, `controller`, `dispatch`;
   `private.json` → `surface.json`; stored-id prefix from `Binding.API()`; `controller`'s 429 retry.
   Gates: all; `test-e2e-private` is the regression proof.
3. **Ordering.** `Kind.Shape`, `Field.Merge = leadingBlock`, `binding/official`, `singletons:` gen
   pass, `SiteResolver`, `AclRuleOrdering`; live ordering in `test-e2e`. Gates: all, plus `test-e2e`.
4. **Edge and the seeded tier.** v2 binding through `controller` at the edge; version guard;
   `test-e2e-edge` and `test-live`; the 4 slice-1 kinds post-wizard. Gates: all, plus `test-e2e`,
   `test-e2e-edge`.
5. **Classic.** `binding/classic` semantics, natural-key uniqueness, `UserGroup`, `SettingNtp`,
   `x_` lint. Gates: as step 4.
6. **IPv6.** (a) Probe 5.1.42 (docs only). (b) `variant`/`officialVariants`/`servedFrom`/
   `createDefaults`, the gen rules, the engine variant pin, `AclRuleIpv6` Tier-1 tested; the G-Q4
   exception text in the frame. With this, slice 2 is built. (c) Serving it live: `servedFrom` set to
   the first accepting version measured in (a), which needs the owner-approved pin bump; then the
   live IPv6 round trip and its ordering interplay in `test-e2e-edge`.

## S2.13 Risks

| Risk | Mitigation |
|---|---|
| IPv6 is refused on the pin (probe c1) | Built and Tier-1 tested, not served; probe 5.1.42; serving needs an owner-approved pin bump; live IPv6 reported not_run until then. |
| Official ordering may omit IPv6 rules, or refuse PUTs while one exists | Measured in step 6a before serving; documented on `AclRuleOrdering`. |
| The alias does not migrate | Live test in step 1; on failure the rename is held and `pulumi state` steps are documented. |
| The `allOf` lift makes other inputs required | Golden diff reviewed; kept only where the spec requires it; one Breaking bullet each. |
| The program's leading block fights other tools that reorder rules | By design; drift on refresh; `REJECT` for strict users. |
| A rule is created elsewhere between ordering GET and PUT | One re-read and retry, then a clear error. |
| The variant changes out of band | Loud error, never "gone", so no duplicate rule. |
| The layer move touches slice-1 code | Its own step with no behaviour change; engine tests and `test-e2e-private` stay green. |
| Natural-key uniqueness costs a list per create | One GET per create; accepted for rule 3. |
| Sandbox workarounds hide real host behaviour | Detection-gated, off on a host with IPv6; a run on a normal host is not_run until someone has one. |
| Docker Hub 429 | Mirror with the same digest; local rebuild allowed for the local tier and stated in the run log. |
| Licence | Unchanged position (§9): only our code, counts, names, summaries and hashes; `TestNoUbiquitiArtifacts`; the key is read at run time and never written anywhere. |

## S2.14 Deviations recorded during implementation

Filled in as each step of §S2.12 is built and measured, in the format of §14.

| # | Design said | Implementation | Why |
|---|---|---|---|
| 1 | §S2.12: `AclRuleOrdering` in step 3, after the layer move of step 2, as a Kind fact (`Kind.Shape = singleton`, `Field.Merge = leadingBlock`) in the one engine | Built in step 1. New packages in their §S2.2 places: `provider/pkg/controller` (client, TLS policy, key header, 429 retry, `SiteResolver`), `provider/pkg/binding/official` (ordering GET/PUT, wire outcome → `ErrIncomplete`/`ErrGone`), `provider/pkg/engine` (the `Ordering` lifecycle: Check, `Compose`, `Project`, adopt, release, Diff). The private engine stays in `provider/pkg/private/engine`; the dispatcher (`private/dispatch_singleton.go`) routes singleton tokens to `engine.Ordering` | The build step of this run was decisions 1, 2 and 4 together. The ordering is a separate lifecycle until step 2 moves the private engine into `provider/pkg/engine` and expresses the singleton as Kind data; the packages already sit where §S2.2 puts them, so step 2 merges, it does not move them again |
| 2 | §S2.2: `private.json` becomes `surface.json` holding every engine-served kind with its `api` | `private.json` keeps its name and gains a `singletons` list (`{token, api: official, path, property, merge}`), written at `PRIVATE=0` too, from `mappings.yaml` `singletons:` via `gen.Singletons()`; `TestNoModelIsGolden` pins the entry; the lint checks each singleton is a golden resource | Same as row 1: the rename belongs to the step-2 layer move |
| 3 | §S2.3: the `allOf` lift takes "each `allOf` member's `required` list" | `pass_variant_required.go` takes the variant schema's **own** `required` list plus its **inline** `allOf` members' (never the shared `$ref` base's, never the discriminator, only input properties) | The spec puts `networkIdFilter` in the MAC variant's own `required`, beside its `allOf`, not inside a member |
| 4 | §S2.3: any other input the lift makes required gets its own Breaking bullet | 14 resources gained spec-required inputs (the DNS records, two managed networks, the three traffic-matching lists, `WifiBroadcastStandard`, `AclRuleMac`); `CHANGELOG.md` has one bullet per entity group (4 bullets for the lift) listing every input | Every lifted input is required by the spec (DNS `ttlSeconds` and MAC `networkIdFilter` were measured as server-required); one bullet per input would be 35 bullets |
| 5 | §S2.3: `index` description points to `AclRuleOrdering` | `index` stays an output with the spec's description; the `AclRuleIpv4`/`AclRuleMac` resource descriptions point to `AclRuleOrdering` | `outputOnly:` stays a plain list of `token.property`; the pointer lives in the descriptions layer that already exists |
| 6 | §S2.5: inputs `orderedAclRuleIds`, `unlisted`, `siteId`; output `effectiveOrder` | As designed; `unlisted` is the generated enum `unifi:sites/v1:AclRuleOrderingUnlisted` (`LAST`, `REJECT`, default `LAST`), generated from the merge strategy by `pass_singletons.go`, which also requires the PUT body's property to be a required string array | The schema stays generated, with no hand-written per-kind schema |
| 7 | §S2.5: Delete releases "and a warning is logged" | The release warning goes to the plugin log (`logging.Warningf`), not to the user's console | The dispatcher has no engine host handle to log to the user; giving it one belongs to the step-2 dispatcher move |
| 8 | §S2.6: `SiteResolver` in step 3, and the dispatcher forwards the UUID to the framework | `SiteResolver` is built (lazy, one cached `GET /v1/sites` for a name, none for a UUID) and used by the ordering only; the framework still gets `siteId` as configured | Forwarding the UUID to the framework is the rest of §S2.6 (step 3/4); the live tier configures the site UUID |
| 9 | Not in the design | `provider/pkg/provider/diff.go`: an `OnDiff` hook resolves the discriminated variant the new inputs name and classifies changes against the base's plus the variant's properties | Measured live in this step: dropping `AclRuleIpv4.protocolFilter` **replaced** the rule. The framework classifies against the shared base's properties only, so every variant-only field (`protocolFilter`, `networkIdFilter`, `DnsARecord.ipv4Address`, …) replaced the object although the PUT accepts it. A pre-existing defect for all 17 discriminated resources; `TestDiffVariantFieldsUpdateInPlace` was red before the hook |
| 10 | §S2.10: `test/live/` shared harness, `restore.sh` untars with the image's `tar` | `test/live/lib.sh` (sourced by `test/e2e/run.sh`), `test/live/sandbox.compose.yml`, `test/live/discovery-stub.js`; `restore.sh` (kept for manual use) also untars with the image. Measured recipe changes: the image's node is `/usr/bin/node24`; the stub is copied to `/opt` (`/tmp` is a tmpfs, `docker cp` writes beneath it); a stale `uos-internal.sock` shows only in nginx's `error.log` (the reload returns 0), so the loop checks that nginx listens on :443 and, if not, removes the socket and reloads | Measured on the first live boot of this step |
| 11 | §S2.10: `make test-e2e` gains the CLI driver | `test/e2e/cli` is its own Go module (standard library, tag `e2e_cli`, like `test/e2e-private`) with its own Python program; `run.sh` reads only `UNIFI_APIKEY` from `seed.env` (no longer sources the whole file) and needs `PULUMI=<the 3.230.0 CLI>` | Keeps the provider module free of the driver; the key never reaches a command line or config |
| 12 | §S2.11 `test-e2e`: a v2-created IPV4 rule stays included | The v2 rule is created with `acl_index` = the current rule count | A taken `acl_index` ties with a managed rule and can move ahead of it (probe d2), which is drift the test would then (correctly) report |
| 13 | §S2.4: alias migration proven live, else documented as `pulumi state` steps | Proven live: the old-token state previews `2 unchanged` with `--expect-no-changes`, `up` reports `2 unchanged` and rewrites the state to `AclRuleIpv4`, the rule keeps its id | — |
| 14 | Not in the design | `Makefile` `test-mock` readiness probe: `$(curl … \|\| echo 000)` became `000000` on a refused connection and passed the gate before Prism was up; now `\|\| true` | Pre-existing bug, hit in this step (every mock test answered 502) |
