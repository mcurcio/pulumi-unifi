# Private API model source: can we extract it from the official UniFi OS Server release?

**Status:** research finding for step 0 ("Discovery first") of
[the provider frame](../design/private-api-provider-frame.md). Probed 2026-11-26 on UniFi OS Server
5.1.15 (Network 10.4.57) and 5.1.42 (Network 10.5.67), linux-x64.

## Verdict: yes, with gaps in the field facts

One official, versioned, checksummed artifact gives us both private surfaces **and** the official
spec, at one Network version, and the same image runs as a test controller:

| Question | Answer | Evidence |
|---|---|---|
| Versioned, checksummed download? | **Yes** | `fw-update.ubnt.com` lists every release with `sha256_checksum`; both downloads matched |
| Unpackable without running the installer? | **Yes**, `unzip` + `tar` only | the installer is a Rust ELF with a ZIP appended; the ZIP holds an OCI `image.tar` |
| Network version stated inside? | **Yes** | image build arg `UNIFI_VERSION=10.4.57-34628-1` (5.1.15), `10.5.67-35187-1` (5.1.42) |
| Classic `/api/s/{site}/rest` kinds? | **Yes**, the same `api/fields/*.json` go-unifi uses | 28 files, 72 kinds, 1,437 leaf fields (10.4.57) |
| `/v2/api/site/{site}/...` kinds (no field files)? | **Yes, from bytecode** (prototype) | Spring and Jackson annotations survive obfuscation: 404 v2 site routes, 32 CRUD-shaped collections, request bodies with JSON names, types, enums, required and length limits |
| Official Integration spec? | **Yes, first-party** | `webapps/ROOT/api-docs/integration.json` is **byte-identical** to the beezly 10.4.57 spec we pin (SHA-256 `ee1492cf…`) |
| Machine-readable defaults, read-only, secret? | **Partly** | not in `api/fields`; partly in bytecode and side files (see the field-facts table) |
| Ubiquiti's image boots without hieutq's changes? | **Yes, under Docker** (one sandbox-only workaround) | systemd `running`; `/proxy/network/integration/v1/sites` returns a JSON 401 from the Network app. An **authenticated** call was NOT RUN (it needs the wizard and a key) |

## Sources and what each covers

| Source (inside the 5.1.15 image) | Covers | Field facts stated | Format | Stable across versions? |
|---|---|---|---|---|
| `api/fields/*.json` in `BOOT-INF/lib/internal-dependencies.jar`, nested in `/usr/lib/unifi/lib/ace.jar` | classic `rest/*` kinds: 27 files + `Setting.json` (45 sections) = 72 kinds | one validation string per field. Of 1,437 leaves: 412 bool, 233 enum, 238 numeric regex, 262 other regex, **292 empty (no type or rule)**; arrays and nesting from JSON shape | JSON | yes: same 28 files in 10.5.67 |
| Spring MVC annotations (`@Get/Post/Put/DeleteMapping`, `@RequestMapping`) in the jar's classes | the route table: 620 routes, 404 under `/api/site/{siteName or siteId}` (served as `/v2/api/site/{site}`, confirmed by the web UI bundle) | path, verb, which parameter is `@RequestBody` | class files (`javap`) | yes: class names change, annotation values do not |
| Request-body DTOs (Immutables interfaces with Jackson and Jakarta Validation annotations) | the v2 kinds' fields | JSON name (`@JsonProperty`), Java type (bool, int, string, UUID, list, map, nested object), `@NotNull`, `Optional<>`, `@Value.Default` (the **presence** of a default; the value is in bytecode, not yet read), `@Length`/`@Size`, polymorphism (`@JsonTypeInfo` property + `@JsonSubTypes` names), enum values (string literals in `<clinit>`) | class files | yes, see the version-bump table |
| `webapps/ROOT/api-docs/integration.json` | the official Integration API (44 paths, 379 schemas) | OpenAPI 3.1 | JSON | ships per version (10.5.67: SHA-256 `a1aa75b9…`) |
| `sensitive_metadata.json` (same nested jar) | the support-dump redaction list: 78 fields in 17 Mongo collections | marks sensitive fields (passphrases, keys, names) | JSON | not compared |
| `legacy_endpoint_segments.json` (same jar) | 298 legacy path segments (`rest`, `cmd`, `stat` names) | none | JSON | not compared |
| Web UI bundle (`webapps/ROOT/app-unifi`, 132 MB, minified) | URL templates (`/api/s/{site}`, `/v2/api/site/{site}`) | none (types erased) | JS | not useful as a field source |
| No OpenAPI or JSON Schema for the private API | — | — | — | searched the full image file list |

### go-unifi's 9 hand-written kinds: covered by the bytecode

Paths are flattened field paths (nested and per-variant), recovered from the `POST` body DTO and
compared against `cmd/fields/custom/*.json` (go-unifi main, written for 10.6.x).

| go-unifi custom kind | v2 route | Bytecode paths (10.4.57) | go-unifi paths | Shared | go-unifi only |
|---|---|---|---|---|---|
| FirewallPolicy | `firewall-policies` | 82 | 64 | 61 | `destination.client_macs`, `destination.match_mac`, `source.web_domains` |
| FirewallZone | `firewall/zone` | 3 | 4 | 2 | `default_zone`, `zone_key` (response-only) |
| DnsRecord | `static-dns` | 16 | 8 | 8 | — |
| Nat | `nat` | 34 | 31 | 31 | — |
| TrafficRoute | `trafficroutes` | 40 | 28 | 28 | — |
| QOSRule | `qos-rules` | 41 | 37 | 36 | `source.port_matching_type` |
| BgpConfig | `bgp/config` | 6 | 4 | 4 | — |
| OSPFRouter | `ospf/router` | 28 | 20 | 20 | — |
| ContentFiltering | `content-filtering/create` | 25 | 17 | 17 | — |

The 5 go-unifi-only paths are response-only fields (the extractor reads request bodies only) or fields
added after 10.4.57 (go-unifi targets 10.6.x); which applies to each is NOT checked. Beyond these 9,
the bytecode also exposes 23 more CRUD-shaped v2 collections, among them `trafficrules`,
`firewall-app-blocks`, `apgroups`, `wan-slas`, `network-members-group`, `ssl-inspection/profiles`,
`network/static-routes`, `acl-rules` and `radius/profiles`.

### Field facts the frame needs, by source

| Fact (frame `Field`) | `api/fields` | v2 bytecode | Elsewhere |
|---|---|---|---|
| type | inferred from the rule (292 leaves have none) | yes (Java type) | — |
| validation / enum | yes (regex or pipe list) | `@Length`/`@Size`, enum literals; enum literal lists mix constant names and wire values (`ALLOW`; `ADDRESS_MASK_REPLY` and `address-mask-reply`), still to untangle | — |
| required | no | `@NotNull` vs `Optional<>` | — |
| default | **no** | that one exists (`@Value.Default`); the value is readable from bytecode, not yet read | `GET …/defaults` routes (e.g. `firewall-policies/defaults`, `settings/super-mgmt/defaults`); a fresh controller's reads |
| readOnly | no | partly: Jackson `READ_ONLY` appears in 15 classes; response vs request DTOs differ | — |
| secret | naming convention only (`x_` prefix: 68 leaves) | `WRITE_ONLY` in 3 classes | `sensitive_metadata.json` (78 fields) |
| createOnly, notReturned, off-encoding, replacedWhole | no | no | the mapping file and the tracer, as the frame plans |

## What a version bump looks like (5.1.15 → 5.1.42; Network 10.4.57 → 10.5.67)

| Surface | Change |
|---|---|
| `api/fields` | same 28 files; 9 changed; kinds 72 → 74 (`Setting.super_application_degradation`, `Setting.test_and_commit`); leaves 1,437 → 1,469; +43 field paths; 1 shape change (`Device.oob_port_config` object → list); 18 rules changed (mostly id rules `[\d\w]+` → `[\d\w-]+`; `FirewallGroup.group_type` gains `domain-group`; `PortConf.stp_edge_state` loses `auto`) |
| route table | 620 → 628; +15 (e.g. `observability/*`, `settings/test-and-commit/defaults`, `sdwan/underlay/test-connection`), −7 (`gateway/engine/*`, `stacking` POST/DELETE) |
| v2 body DTOs (9 kinds) | **every DTO class renamed** (e.g. `YPBkdOLFaXsxg` → `oiFBAnX`), yet re-extraction by route needs no change: 0 fields removed, 0 changed, +10 added (`cloud_template.allow_overrides` on 4 kinds, `hits`/`last_hit` on FirewallPolicy, 4 on BgpConfig) |
| Integration spec | still 44 paths, `info.version` 10.5.67, new SHA-256 |
| Image | same layer layout, but the Network layer moved (layer 7 → layer 8); the script finds it by content |

## The image as a Tier-2 target

| Check | Result |
|---|---|
| `docker load` of the extracted `image.tar` | **works**: `uosserver:f77bca81ecbf` (manifest `sha256:c81b67db…`) |
| What hieutq changes | its layers 2-10 have Ubiquiti's digests; layer 1 is Ubiquiti's base recompressed (identical `diff_ids`). It adds one layer: `/entrypoint.sh` symlinks the state dirs into a single `/unifi` volume, writes `/usr/lib/version`, `/usr/lib/platform` and a UUID, then `exec /sbin/init` |
| Ubiquiti's image under Docker (`--privileged --cgroupns=host`, cgroup v1, `-e UOS_UUID APP_MODEL=UOSSERVER APP_VERSION=5.1.15 PRODUCT_NAME=uosserver FIRMWARE_PLATFORM=linux-x64`, the variables its own `/root/uos-entrypoint.sh` reads) | systemd `running` within about 3 minutes; Network (`:8443`, internal `:8081`), unifi-core, Mongo, RabbitMQ up |
| nginx edge | failed on `listen [::]:80` because **this sandbox kernel has no IPv6**; after deleting the `[::]` lines from two unifi-core nginx files inside the running container, `:443` came up. Not checked on a normal IPv6 host (NOT RUN) |
| `GET /proxy/network/integration/v1/sites` with no key, or a bogus key | **401** JSON `api.authentication.missing-credentials`, served by the Network app (`requestPath` `/integration/v1/…`): the route is served and gated by unifi-core |
| Authenticated Integration call | **NOT RUN** (needs the first-run wizard and a minted key) |
| Private routes before the wizard | `api/s/default/rest/networkconf`, `v2/api/site/default/firewall-policies` and `…/trafficroutes` returned **200 with no credentials** on the fresh, un-set-up controller (setup-mode behaviour; not checked after setup) |
| Seed compatibility with `test/e2e/unifi-seed.tgz` | **NOT RUN** (the seed's paths follow hieutq's `/unifi` layout) |

So Tier 2 can be built from Ubiquiti's image plus a thin in-repo layer (the volume layout and the
version stamp), with no third-party image. The cost: Ubiquiti publishes no registry image, so
building it means downloading and unpacking the 875 MB installer.

## Gaps and risks

| Risk | Impact | Mitigation |
|---|---|---|
| No licence on any of these files | none may be committed or redistributed | fetch at build by pinned version + SHA-256, as `openapi/fetch.sh` does; commit only scripts and counts |
| The v2 model comes from obfuscated bytecode | an obfuscator or framework change (e.g. dropping Immutables, or encrypting strings) breaks extraction | anchor on routes and annotations, never class names; one Tier-1 golden per kind catches a silent loss |
| `api/fields` states no defaults, read-only or secret flags; 292 leaves have no type | the frame's `Field` facts stay hand-written or come from bytecode | `x_` prefix plus `sensitive_metadata.json` for secret (fail closed, as the frame says); defaults from the `@Value.Default` bytecode or the `/defaults` routes |
| Request and response DTOs differ | read-only and response-only fields (e.g. `default_zone`) are missed | extend `dto.py` to the `GET` return type and diff the two |
| Enum literals mix constant names and wire values | wrong enum values | read the enum's constructor arguments and `@JsonValue` (not done yet) |
| `routes.py` misses some class-level prefixes (e.g. the Integration controllers print as `/acl-rules`) | prefixes missing on some routes | parse the class `RuntimeVisibleAnnotations` instead of the text heuristic |
| 875 MB per version, plus about 1 GB unpacked | slow and CI-unfriendly | cache by SHA-256; the extracted model is a few MB |
| The `.deb` fallback | NOT RUN: not downloaded; the jar layout is presumably the same (go-unifi already reads `internal-dependencies.jar` for 10.x) | only needed if UniFi OS Server lags a Network release |

## Where the findings contradict the frame doc

| Frame says | Finding |
|---|---|
| "the 12 v2 kinds without shipped definitions are hand-written data" (F7); "Neither … gap" row | the bytecode gives a machine-readable model for them; there are **32** CRUD-shaped v2 collections, not 12 |
| `default` comes "from the private definitions or the mapping file" | `api/fields` holds **no** defaults; they come only from the v2 bytecode, the `/defaults` routes or a fresh controller |
| the official spec is "pinned, sanitized, as today" (from beezly) | the same file ships first-party in the image; the beezly pin can become a UniFi OS Server pin, so one version pin feeds both bindings and the test image |
| `api/fields/*.json` "inside the Network jar" | inside `BOOT-INF/lib/internal-dependencies.jar`, nested in `ace.jar` (a Spring Boot 3.5 fat jar) |
| Day-one sizes "~270" network and "~110" Wi-Fi private fields | at 10.4.57: `NetworkConf` 258 leaves, `WlanConf` 157 |
| Source table: UniFi OS Server "probe pending" | done: layout, Docker boot and v2 routes as above; the authenticated round-trip is still open |
| `test/e2e/README.md`: hieutq is "the only image that can authenticate the Integration API" | Ubiquiti's own image serves and gates the same endpoint; hieutq only adds the volume layout and the version stamp (authenticated call NOT RUN) |

## Reproducible extraction

Nothing below writes into the repo. Output directories hold Ubiquiti's files: keep them out of git.

```sh
# 1. Download (SHA-256 checked against fw-update), unpack, and pull the model sources:
#    fields/*.json, integration.json, classes/, versions.txt
tools/uos-extract/extract.sh 5.1.15 /tmp/uos-5.1.15    # about 875 MB download; needs curl jq unzip tar

# 2. Route table from the Spring annotations (needs a JDK's javap)
cd /tmp/uos-5.1.15/classes
grep -rlE --include=*.class 'annotation/(Get|Post|Put|Delete|Patch|Request)Mapping' . \
  | sed 's#^\./##; s#\.class$##; s#/#.#g' > ../mapping-classes.txt
JAVA_TOOL_OPTIONS= xargs -a ../mapping-classes.txt javap -v -p -cp . > ../mapping.javap
cd - && python3 -I tools/uos-extract/routes.py /tmp/uos-5.1.15/mapping.javap | sort -u -k2,2 > /tmp/uos-5.1.15/routes.tsv

# 3. One v2 kind's request model (JSON names, types, enums, required, defaults, variants)
python3 -I tools/uos-extract/dto.py /tmp/uos-5.1.15/classes POST '/api/site/{siteName}/trafficroutes' /tmp/uos-5.1.15/routes.tsv

# 4. Optional: boot Ubiquiti's image (systemd needs privileges; offline tier only)
docker load -i /tmp/uos-5.1.15/image.tar
docker run -d --name uos --privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  --tmpfs /run:exec --tmpfs /run/lock --tmpfs /tmp:exec --stop-signal SIGRTMIN+3 \
  -e UOS_UUID="$(cat /proc/sys/kernel/random/uuid)" -e APP_MODEL=UOSSERVER -e APP_VERSION=5.1.15 \
  -e PRODUCT_NAME=uosserver -e FIRMWARE_PLATFORM=linux-x64 -p 127.0.0.1:11443:443 uosserver:f77bca81ecbf
```

| Pin | Value |
|---|---|
| 5.1.15 installer | `fw-download.ubnt.com/data/unifi-os-server/24e0-linux-x64-5.1.15-926621de-c9d7-48cd-8921-a0ff3eebd3f4.15-x64`, 874,927,011 bytes, SHA-256 `04c8e401eb34330fe99d94f35aa351e0e0e97895f0d9a4b459ff34fb50cad2bb` |
| 5.1.42 installer | `fw-download.ubnt.com/data/unifi-os-server/5172-linux-x64-5.1.42-12e9e3cf-8f8b-4e54-928c-76b80a10c8a4.42-x64`, 785,796,978 bytes, SHA-256 `f6111e9396a42c74016f5dde9b01fbef486a6fe69efe137935e6e38b7c22f94d` |
| Installer layout | Rust ELF + appended ZIP: `image.tar` (OCI, docker-format manifest, gzip layers), `uosserver`, `uosserver-service`, `updater-service`, `discovery`, `purge`, `pasta`, `portmap.json`, `mounts.json` |
| Network layer (5.1.15) | `sha256:3451ba77d19c4033300bd1e08040adde44afb7e7d2062ef8bc3f79ba77e545dc` (835 MB, gzip) |

`dto.py` and `routes.py` are prototypes: heuristic parsers of `javap` text, good enough for these
counts, not yet a codegen input. The counts above come from those scripts plus short one-off analyses
that are not committed.
