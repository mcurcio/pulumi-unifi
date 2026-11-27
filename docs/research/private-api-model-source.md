# Private API model source: can we extract it from the official UniFi OS Server release?

**Status:** research finding for step 0 ("Discovery first") of
[the provider frame](../design/private-api-provider-frame.md). Probed 2026-11-26/27 on UniFi OS Server
5.1.15 (Network 10.4.57) and 5.1.42 (Network 10.5.67), linux-x64. Every number below comes from the
scripts in `tools/uos-extract/` (see [Reproduce](#reproduce)).

## Verdict: yes, with gaps in the field facts

One official, versioned, checksummed artifact gives us both private surfaces **and** the official
spec, at one Network version, and the same image runs as a test controller. The extracted model was
checked against the official spec and against a live controller:

| Question | Answer | Evidence |
|---|---|---|
| Versioned, checksummed download? | **Yes** | `fw-update.ubnt.com` lists every release with `sha256_checksum`; both downloads matched |
| Unpackable without running the installer? | **Yes**, `unzip` + `tar` only | the installer is a Rust ELF with a ZIP appended; the ZIP holds an OCI `image.tar` |
| Network version stated inside? | **Yes** | image build arg `UNIFI_VERSION=10.4.57-34628-1` (5.1.15), `10.5.67-35187-1` (5.1.42) |
| Classic `/api/s/{site}/rest` kinds? | **Yes**, the same `api/fields/*.json` go-unifi uses | 28 files, 72 kinds, 1,437 leaf fields; a live controller's 237 values all pass their rules |
| `/v2/api/site/{site}/...` kinds (no field files)? | **Yes, from bytecode** (prototype) | Spring, Jackson and Jakarta Validation annotations survive obfuscation: 484 v2 site routes, 31 CRUD-shaped collections, 726 request fields with types, enums, required flags, defaults and constraints |
| Is the bytecode reading accurate? | **Yes, measured** | on the Integration API's own controllers: **73/73** route+verb pairs and **157/161** request fields match the official spec; live defaults match **15/15** |
| Official Integration spec? | **Yes, first-party** | `webapps/ROOT/api-docs/integration.json` is **byte-identical** to the beezly 10.4.57 spec we pin (SHA-256 `ee1492cf…`) |
| Machine-readable defaults, read-only, secret? | **Partly** | not in `api/fields`; defaults and response-only fields from bytecode; secrets only by naming and a redaction list |
| Does the model alone build a valid create? | **Partly: 6 of 15** v2 kinds round-trip from required fields alone | the rest need business rules in code (seen as error messages) or a set-up controller; three hit 500s on fields the annotations don't mark required |
| Ubiquiti's image boots without hieutq's changes? | **Yes, under Docker** (one sandbox-only workaround) | systemd up; `/proxy/network/integration/v1/sites` returns a JSON 401 from the Network app. An **authenticated** call was NOT RUN (it needs the wizard and a key) |

## Sources and what each covers

| Source (inside the 5.1.15 image) | Covers | Field facts stated | Stable across versions? |
|---|---|---|---|
| `api/fields/*.json` in `BOOT-INF/lib/internal-dependencies.jar`, nested in `/usr/lib/unifi/lib/ace.jar` | classic `rest/*` kinds: 27 files + `Setting.json` (45 sections) = 72 kinds | one validation string per field. Of 1,437 leaves: 412 bool, 233 enum, 238 numeric regex, 262 other regex, **292 empty (no type or rule)**; arrays and nesting from JSON shape | yes: same 28 files in 10.5.67 |
| Spring MVC annotations in the jar's classes | the route table: 614 routes; 484 under `/api/site/{siteName or siteId}` (served as `/v2/api/site/{site}`, confirmed by the web UI bundle); 73 Integration routes | path, verb, the `@RequestBody` parameter, the return type. Class prefixes are often set through a custom meta-annotation, which `routes.py` resolves | yes: class names change, annotation values do not |
| Request and response DTOs: Immutables interfaces (v2) and Lombok-style classes (Integration) | the v2 kinds' fields | JSON name (`@JsonProperty`, or the real field name, which survives), Java type, `@NotNull`/`@NotEmpty`/Swagger `REQUIRED`, `Optional<>`, `@Value.Default` with its **value** (42 decoded, 1 computed), `@Length`/`@Size`, 63 custom validators with their messages (e.g. "must be valid IPv4 address"), polymorphism (`@JsonTypeInfo` + `@JsonSubTypes`), enum wire values (following `@JsonValue`) | yes, see the version-bump table |
| `webapps/ROOT/api-docs/integration.json` | the official Integration API (44 paths, 379 schemas) | OpenAPI 3.1 | ships per version (10.5.67: SHA-256 `a1aa75b9…`) |
| `sensitive_metadata.json` (same nested jar) | the support-dump redaction list: 78 fields in 17 Mongo collections | marks sensitive fields (passphrases, keys, names) | not compared |
| `legacy_endpoint_segments.json` (same jar) | 298 legacy path segments (`rest`, `cmd`, `stat` names) | none | not compared |
| Web UI bundle (`webapps/ROOT/app-unifi`, 132 MB, minified) | URL templates (`/api/s/{site}`, `/v2/api/site/{site}`) | none (types erased) | not useful as a field source |
| No OpenAPI or JSON Schema for the private API | — | — | searched the full image file list |

### Field facts the frame needs, by source

| Fact (frame `Field`) | `api/fields` | v2 bytecode | Live controller |
|---|---|---|---|
| type | inferred from the rule (292 leaves have none) | yes (Java type) | — |
| validation / enum | yes (regex or pipe list); 237/237 live values pass | `@Length`/`@Size`, custom validator messages; enum wire values | the validator's error names the field and rule |
| required | no | `@NotNull`/`@NotEmpty`: 166 fields; **misses implicit ones** (see the round-trip table) | a 500 with a `NullPointerException` names them |
| default | **no** | 42 decoded values; 15/15 comparable ones match live | `GET …/firewall-policies/defaults` and similar routes |
| readOnly | no | response-only fields: 167 (the response type minus the request type) | every returned key of the created objects is modelled (44/44) |
| secret | naming only (`x_` prefix: 68 leaves; e.g. `mgmt.x_api_token` is live but not in the files) | `WRITE_ONLY` in 3 classes | `sensitive_metadata.json` (78 fields) |
| createOnly, notReturned, off-encoding, replacedWhole | no | no | the mapping file and the tracer, as the frame plans |

## Accuracy checks

### Against the official spec (the bytecode of the Integration API's own controllers)

The image ships both the controllers and the spec generated from them, so the bytecode reader can be
scored on the one API whose contract is published. `check/spec_check.py`, 10.4.57:

| Check | Result | Disagreements |
|---|---|---|
| Route+verb pairs (`routes.py`) | **73 / 73**, none missing, none extra | — |
| Request-body fields found (22 bodies) | **157 / 161** | the 4 misses are `blackoutScheduleConfiguration.days[].{type,day}` on POST and PUT (a nested list of variants) |
| Required flag agrees | **154 / 157** | firewall policy `connectionStateFilter` (POST, PUT) and `loggingEnabled` (PATCH): the bytecode says required, the spec optional. Which is right is NOT checked live |
| Enum values agree | **8 / 12** | the frequency-keyed map `basicDataRateKbpsByFrequencyGHz.{2.4,5}` (a `Map` in Java, named properties in the spec), on POST and PUT |

### Against a live controller (Ubiquiti's 10.4.57 image, fresh, before the setup wizard)

Before the wizard, Network's internal `http://127.0.0.1:8081` answers `/api/s/…` and `/v2/api/…`
without credentials inside the container, so a throwaway controller can be read and written with no
key. `check/live_*.py` and `check/roundtrip.py`:

| Check | Result |
|---|---|
| Classic objects vs `api/fields` rules (`live_fields.py`): 1 network, 32 settings sections, groups, profiles | **237 values checked, 0 rejected** |
| Keys the controller returns that no field file defines | `networkconf.mdns_enabled`, `mdns.enabled_for`, `mdns.enabled_for_network_ids`, `radio_ai.auto_enabled`, `mgmt.x_api_token`, `ips.endpoint_scanning`, `guest_access.restricted_subnet_{1,2,3}`, `ether_lighting.{network,speed}_defaults`; 5 settings sections have no definition (`super_fingerbank`, `super_fabric_system_log`, `ugw`, `peer_to_peer`, `global_network`). The first four are **exactly the fields go-unifi patches in by hand**: a live read finds the field files' gaps automatically |
| Decoded defaults vs `GET …/firewall-policies/defaults` (`live_defaults.py`) | **15 / 15** comparable values match; 8 more belong to `matching_target` variants absent from the default shape |
| v2 returned keys vs model, for created objects | **44 / 44** modelled |

**Minimal-create round trip.** For each kind, a body built from the model's required (and
minimum-size) fields only is POSTed, read back and deleted:

| Outcome | Kinds | What it shows |
|---|---|---|
| Create → read → delete succeeds | `qos-rules`, `static-dns`*, `acl-rules`, `firewall-app-blocks`, `network-members-group`, `apgroups` | the model is enough for a valid create |
| 500 `NullPointerException` on a field not marked required | `firewall/zone` (`network_ids`), `static-dns` (`record_type`), `nat` (`source_filter`) | **implicitly required fields**: invisible to annotations, found by one probe. The zone case is go-unifi's documented hand patch |
| 400 from business rules in code | `trafficroutes` ("Missing domain"), `trafficrules` ("Missing Application"), `content-filtering` ("No Clients or Networks Specified"), `wan-slas` ("At least one monitor must be defined") | cross-field rules: mapping-file data, not extractable |
| Needs a set-up controller or a device | `firewall-policies` ("zone does not exist"), `firewall/zone` ("Could not find hotspot firewall zone"), `ospf/router` (networks that allow OSPF), `bgp/config` ("Device doesn't support BGP") | NOT RUN to completion; needs the wizard, a key and an adopted gateway |

\* passes once `record_type` is supplied. Item `GET` returns **405** for `acl-rules`, `static-dns`,
`firewall-app-blocks` and `apgroups`: those kinds are read by listing and filtering, a binding fact the
route table shows.

### go-unifi's 9 hand-written kinds

Paths are flattened field paths (nested and per-variant) from the `POST` body DTO, compared with
`cmd/fields/custom/*.json` (go-unifi main, written for 10.6.x):

| go-unifi custom kind | v2 route | Bytecode paths (10.4.57) | go-unifi paths | Shared | go-unifi only |
|---|---|---|---|---|---|
| FirewallPolicy | `firewall-policies` | 82 | 64 | 61 | `destination.client_macs`, `destination.match_mac`, `source.web_domains` |
| FirewallZone | `firewall/zone` | 3 | 4 | 2 | `default_zone`, `zone_key` (both in the response model) |
| DnsRecord | `static-dns` | 16 | 8 | 8 | — |
| Nat | `nat` | 34 | 31 | 31 | — |
| TrafficRoute | `trafficroutes` | 40 | 28 | 28 | — |
| QOSRule | `qos-rules` | 41 | 37 | 36 | `source.port_matching_type` |
| BgpConfig | `bgp/config` | 6 | 4 | 4 | — |
| OSPFRouter | `ospf/router` | 28 | 20 | 20 | — |
| ContentFiltering | `content-filtering/create` | 25 | 17 | 17 | — |

Beyond these 9, `dto.py --crud` models 22 more collections, among them `trafficrules`,
`firewall-app-blocks`, `apgroups`, `wan-slas`, `network-members-group`, `ssl-inspection/profiles`,
`network/static-routes`, `acl-rules`, `radius/profiles`, `object-oriented-network-config` and `uid/wlan`.

## What a version bump looks like (5.1.15 → 5.1.42; Network 10.4.57 → 10.5.67)

| Surface | Change |
|---|---|
| `api/fields` | same 28 files; 9 changed; kinds 72 → 74 (`Setting.super_application_degradation`, `Setting.test_and_commit`); leaves 1,437 → 1,469; +43 field paths; 1 shape change (`Device.oob_port_config` object → list); 18 rules changed (mostly id rules `[\d\w]+` → `[\d\w-]+`; `FirewallGroup.group_type` gains `domain-group`; `PortConf.stp_edge_state` loses `auto`) |
| route table | 614 → 622; +15 (e.g. `observability/*`, `settings/test-and-commit/defaults`, `sdwan/underlay/test-connection`), −7 (`gateway/engine/*`, `stacking` POST/DELETE); Integration routes still 73 |
| v2 models (`--crud`) | 31 → 30 collections (`stacking` loses create); fields 726 → 729; +12 paths, 0 removed, 9 facts changed; **every DTO class renamed** (e.g. `YPBkdOLFaXsxg` → `oiFBAnX`) yet the extractor needs no change |
| spec check | unchanged on 10.5.67: 73/73 routes, 157/161 fields, 154/157 required |
| Integration spec | still 44 paths, `info.version` 10.5.67, new SHA-256 |
| Image | same layout, but the Network layer moved (layer 7 → layer 8); `extract.sh` finds it by content |

## The image as a Tier-2 target

| Check | Result |
|---|---|
| `docker load` of the extracted `image.tar` | **works**: `uosserver:f77bca81ecbf` (manifest `sha256:c81b67db…`) |
| What hieutq changes | its layers 2-10 have Ubiquiti's digests; layer 1 is Ubiquiti's base recompressed (identical `diff_ids`). It adds one layer: `/entrypoint.sh` symlinks the state dirs into a single `/unifi` volume, writes `/usr/lib/version`, `/usr/lib/platform` and a UUID, then `exec /sbin/init` |
| Ubiquiti's image under Docker (`--privileged --cgroupns=host`, cgroup v1, with the variables its own `/root/uos-entrypoint.sh` reads) | first boot: systemd `running` within about 3 minutes; Network (`:8443`, internal `:8081`), unifi-core, Mongo and RabbitMQ up |
| nginx edge | failed on `listen [::]:80` because **this sandbox kernel has no IPv6**; deleting the `[::]` lines in two unifi-core nginx files brought `:443` up. On a second boot unifi-core did not write its edge config within 10 minutes (it logged "Can't find lanIp", likely the missing host-side `discovery` helper the installer normally runs). Not checked on a normal IPv6 host (NOT RUN) |
| `GET /proxy/network/integration/v1/sites` with no key, or a bogus key | **401** JSON `api.authentication.missing-credentials`, served by the Network app: the route is served and gated by unifi-core |
| Authenticated Integration call | **NOT RUN** (needs the first-run wizard and a minted key) |
| Private routes before the wizard | answer **200 with no credentials** (through the edge, and on internal `:8081`); not checked after setup |
| Seed compatibility with `test/e2e/unifi-seed.tgz` | **NOT RUN** (the seed's paths follow hieutq's `/unifi` layout) |

So Tier 2 can be built from Ubiquiti's image plus a thin in-repo layer (the volume layout and the
version stamp), with no third-party image. The costs: Ubiquiti publishes no registry image, so building
it means downloading and unpacking the 875 MB installer; and the edge's dependence on the host-side
helpers needs one more look on a normal host.

## Gaps and risks

| Risk | Impact | Mitigation |
|---|---|---|
| No licence on any of these files | none may be committed or redistributed | fetch at build by pinned version + SHA-256, as `openapi/fetch.sh` does; commit only scripts and counts |
| The v2 model comes from obfuscated bytecode | an obfuscator or framework change (e.g. encrypted strings) breaks extraction | anchor on routes and annotations, never class names; the spec check (73/73, 157/161) is a ready regression gate per version |
| Implicitly required fields and business rules are not in any annotation | creates fail at runtime (500s, rule errors) | a Tier-2 probe per kind turns each failure into mapping-file data; the error text names the field |
| `api/fields` states no defaults, read-only or secret flags; 292 leaves have no type; settings drift (10 live keys and 5 sections undefined) | facts stay hand-written for classic kinds | a live read lists the undefined keys; `x_` prefix plus `sensitive_metadata.json` for secret (fail closed) |
| Polymorphic lists and keyed maps (`days[]`, frequency maps) | 4 spec fields missed | handle `Map` keys and list variants in `dto.py` |
| The scripts are prototypes: they parse `javap` text | brittle to `javap` output changes | move to a class-file parser before codegen depends on it |
| 875 MB per version, plus about 1 GB unpacked | slow and CI-unfriendly | cache by SHA-256; the extracted model is a few MB |
| The `.deb` fallback | NOT RUN: not downloaded; the jar layout is presumably the same (go-unifi already reads `internal-dependencies.jar` for 10.x) | only needed if UniFi OS Server lags a Network release |

## Where the findings contradict the frame doc

| Frame says | Finding |
|---|---|
| "the 12 v2 kinds without shipped definitions are hand-written data" (F7); "Neither … gap" row | the bytecode gives a machine-readable, spec-checked model for them: **31** CRUD-shaped v2 collections. Hand-written data shrinks to implicit requirements and cross-field rules |
| `default` comes "from the private definitions or the mapping file" | `api/fields` holds **no** defaults; the v2 bytecode does (42 decoded, live-verified), plus the `/defaults` routes |
| the official spec is "pinned, sanitized, as today" (from beezly) | the same file ships first-party in the image; the beezly pin can become a UniFi OS Server pin, so one version pin feeds both bindings and the test image |
| `api/fields/*.json` "inside the Network jar" | inside `BOOT-INF/lib/internal-dependencies.jar`, nested in `ace.jar` (a Spring Boot 3.5 fat jar) |
| Day-one sizes "~270" network and "~110" Wi-Fi private fields | at 10.4.57: `NetworkConf` 258 leaves, `WlanConf` 157 |
| Private DNS records add "NS, PTR, SOA" | the 10.4.57 `static-dns` enum is `A, AAAA, CNAME, MX, NS, SRV, TXT`: NS, but no PTR or SOA |
| Source table: UniFi OS Server "probe pending" | done: layout, Docker boot, v2 routes, spec and live checks as above; the authenticated round-trip is still open |
| `test/e2e/README.md`: hieutq is "the only image that can authenticate the Integration API" | Ubiquiti's own image serves and gates the same endpoint; hieutq only adds the volume layout and the version stamp (authenticated call NOT RUN) |

## Reproduce

Nothing below writes into the repo. Output directories hold Ubiquiti's files: keep them out of git.
Needs curl, jq, unzip, tar, a JDK (`javap`), Python 3, and Docker for the live checks.

```sh
X=tools/uos-extract
# 1. Download (SHA-256 checked against fw-update), unpack, pull the model sources:
#    fields/*.json, integration.json, classes/, versions.txt          (~875 MB download)
$X/extract.sh 5.1.15 /tmp/uos-5.1.15
# 2. Route table, then every CRUD collection's request/response model
python3 -I $X/routes.py /tmp/uos-5.1.15/classes | sort -u > /tmp/uos-5.1.15/routes.tsv
python3 -I $X/dto.py /tmp/uos-5.1.15/classes /tmp/uos-5.1.15/routes.tsv --crud > /tmp/uos-5.1.15/crud.json
#    (or one route: dto.py <classes> <routes.tsv> POST '/api/site/{siteName}/trafficroutes')
# 3. Accuracy against the official spec in the same image
python3 -I $X/check/spec_check.py /tmp/uos-5.1.15/classes /tmp/uos-5.1.15/routes.tsv /tmp/uos-5.1.15/integration.json
# 4. Live checks against a THROWAWAY controller (systemd needs privileges; offline tier only)
docker load -i /tmp/uos-5.1.15/image.tar
docker run -d --name uos-probe --privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  --tmpfs /run:exec --tmpfs /run/lock --tmpfs /tmp:exec --stop-signal SIGRTMIN+3 \
  -e UOS_UUID="$(cat /proc/sys/kernel/random/uuid)" -e APP_MODEL=UOSSERVER -e APP_VERSION=5.1.15 \
  -e PRODUCT_NAME=uosserver -e FIRMWARE_PLATFORM=linux-x64 -p 127.0.0.1:11443:443 uosserver:f77bca81ecbf
#    wait until: docker exec uos-probe curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8081/v2/api/site/default/apgroups  -> 200
$X/check/live_fetch.sh uos-probe /tmp/uos-live
python3 -I $X/check/live_fields.py /tmp/uos-5.1.15/fields /tmp/uos-live/classic
python3 -I $X/check/live_defaults.py /tmp/uos-5.1.15/crud.json /tmp/uos-live/v2/firewall-policies-defaults.json
python3 -I $X/check/roundtrip.py /tmp/uos-5.1.15/crud.json qos-rules,static-dns,acl-rules,apgroups \
  '{"static-dns":{"record_type":"A","value":"192.0.2.10"}}'
```

| Pin | Value |
|---|---|
| 5.1.15 installer | `fw-download.ubnt.com/data/unifi-os-server/24e0-linux-x64-5.1.15-926621de-c9d7-48cd-8921-a0ff3eebd3f4.15-x64`, 874,927,011 bytes, SHA-256 `04c8e401eb34330fe99d94f35aa351e0e0e97895f0d9a4b459ff34fb50cad2bb` |
| 5.1.42 installer | `fw-download.ubnt.com/data/unifi-os-server/5172-linux-x64-5.1.42-12e9e3cf-8f8b-4e54-928c-76b80a10c8a4.42-x64`, 785,796,978 bytes, SHA-256 `f6111e9396a42c74016f5dde9b01fbef486a6fe69efe137935e6e38b7c22f94d` |
| Installer layout | Rust ELF + appended ZIP: `image.tar` (OCI, docker-format manifest, gzip layers), `uosserver`, `uosserver-service`, `updater-service`, `discovery`, `purge`, `pasta`, `portmap.json`, `mounts.json` |
| Network layer (5.1.15) | `sha256:3451ba77d19c4033300bd1e08040adde44afb7e7d2062ef8bc3f79ba77e545dc` (835 MB, gzip) |

The go-unifi comparison and the `api/fields` statistics used short one-off analyses that are not
committed; everything else above comes from the committed scripts.
