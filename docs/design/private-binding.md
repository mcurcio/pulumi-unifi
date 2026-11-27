# Private binding, slice 1: extractor, Kind model, codegen, engine, e2e

**Status:** decided design for the first private-API slice. It implements the agreed frame
(`docs/design/private-api-provider-frame.md` on `docs/two-api-provider-frame`: one provider over two
APIs, rules 1-3, G-Q1, natural keys, stored ids that name their API, whole-kind serving). The evidence
is `docs/research/private-api-model-source.md`. **Where am I:** Module → Interfaces (this doc) →
Implementation (next, on this branch).

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
| 8 | §5 twin list | `officialTwins:` entries also list the official golden tokens that serve the kind (`official:`). A test checks them against `provider/pkg/gen/testdata/tokens.txt` | Turns "an official resource serves it" into a fact a test can check. **Open:** at 10.4.57 the provider surfaces ACL rules only as functions (`getIpAclRule`, `getMacAclRule`, `listAclRules`), not as resources, so treating `acl-rules` as a twin leaves ACL writes uncovered. The owner should decide whether that stays |
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
