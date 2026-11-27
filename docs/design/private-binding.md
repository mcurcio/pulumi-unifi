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
`make test` check (`TestNoUbiquitiArtifacts`) fails if a tracked file is a `.class`/`.jar`, sits under
`fields/`, or is a JSON file shaped like `api/fields` (a top-level object of validation-string leaves).
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
