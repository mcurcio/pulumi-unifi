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

## Slice 2 probe (authenticated edge, seeded controller)

Measured 2026-11-28 with raw HTTP through the UniFi OS edge (`https://127.0.0.1:11443`) of the
Tier-2 seed (`test/e2e/unifi-seed.tgz`, wizard completed, key minted) on the pinned Tier-2 image
(UniFi OS 5.1.15, Network 10.4.57). Every request carried only `X-API-KEY` (read from
`test/e2e/seed.env` at run time). All throwaway objects were deleted and the one settings change
was restored. No provider code changed.

### Boot recipe (what it took in a sandbox without IPv6)

1. **Image.** `hieutq/unifi-os-server@sha256:3e8854769d63…` from Docker Hub answered 429; the same
   digest came from `mirror.gcr.io/hieutq/unifi-os-server@<digest>` after several tries (the mirror
   rate-limits per minute; already-pulled layers are kept between tries). Tagged locally as
   `hieutq/unifi-os-server:pinned-3e8854769d63`. The image is the pinned digest, not a local rebuild.
2. **Seed.** Untar into a fresh volume (as `restore.sh` does; `caddy:2` stood in for `alpine:3`,
   which was not cached), deleting `postmaster.pid` and `mongod.lock`.
3. **Privileges.** The compose `cap_add` list failed here (`CAP_SYS_RESOURCE` not available);
   `privileged: true` with the same `cgroup: host`, tmpfs and volume mounts worked.
4. **lanIp.** unifi-core reads the network interfaces from a discovery agent at
   `discoveryClientUrl`, which the image sets to `http://host.docker.internal:11002` (env
   `NODE_CONFIG`). On a real install the installer's host-side `discovery` binary serves it; the
   in-container `uos-discovery-client` is a stub that exits. With no agent, unifi-core logs
   "Can't find lanIp" every 3 s and never writes the `site-*` nginx configs, so nothing listens
   on 443. Fix: map `host.docker.internal` to `127.0.0.1` in the container's `/etc/hosts` and run a
   10-line node HTTP server on `127.0.0.1:11002` that answers any path with
   `os.networkInterfaces()` as JSON (the shape unifi-core validates), then restart unifi-core.
5. **No IPv6.** unifi-core writes `listen [::]:…` lines into `site-local-ip.conf` and
   `site-supervisor.conf`; nginx then fails with `socket() [::]:443 … (97)`. Fix: a loop that
   deletes those lines as they appear and runs `nginx -s reload`. **Never `systemctl restart
   nginx`**: the unit's `ExecStartPre` (`stale-config-cleanup.conf`) runs
   `rm -rf /data/unifi-core/config/http/*`, wiping every config unifi-core wrote, and unifi-core
   does not rewrite them until its own restart. After one such restart a stale
   `uos-internal.sock` blocked the reload (`bind() … Address already in use`); deleting the
   socket file and reloading fixed it.
6. **Ready.** About 14 minutes from `up` to the first authenticated 200 on `GET /v1/sites`,
   most of it spent finding steps 4 and 5. A boot with both fixes applied from the start was
   not timed.

Steps 4 and 5 are sandbox workarounds, not product behaviour; they belong in the Tier-2 harness
if it must run here. A host with IPv6 and the installer's own `discovery` agent needs neither.

### a. Official ACL rules and ordering (`/proxy/network/integration/v1/sites/{siteId}/acl-rules`)

| Question | Answer (evidence) |
|---|---|
| Site id | `GET /v1/sites` → 200, `totalCount` 1, `internalReference` `default`; the UUID equals the private site's `external_id` (`GET /api/self/sites`) |
| Empty list / ordering | `GET acl-rules` → 200 `{offset,limit,count:0,totalCount:0,data:[]}`; `GET acl-rules/ordering` → 200 `{"orderedAclRuleIds":[]}` |
| Create IPV4 | `POST {type:IPV4,name,enabled,action}` → **201**; response adds `id` (UUID), `index`, `metadata.origin: USER_DEFINED`. With `protocolFilter:["TCP"]` and `sourceFilter{type:IP_ADDRESSES_OR_SUBNETS,ipAddressesOrSubnets}` → 201, echoed |
| Create MAC | without `networkIdFilter` → **400** `api.request.error` "networkIdFilter must not be null"; with the official network UUID → 201. The spec lists `networkIdFilter` as required on the MAC variant (inside `allOf`) but the generated `TrafficMatchMac.requiredInputs` is only `[action, enabled, name]`: the `allOf` requirement is lost in codegen |
| Read one | `GET acl-rules/{id}` → 200; after delete → **404** `api.resource-not-found` |
| Update | `PUT` is a **full replace**: a PUT without `sourceFilter`/`protocolFilter` → 200 and the filters are gone on re-read. A PUT without `type` → 400 `api.request.missing-type-id`. A PUT that changes `type` (MAC → IPV4) → **200, same id**: the variant can change in place |
| Deprecated `index` | `PUT … index:0` on the third rule → 200, `index` stays 2: ignored, as documented |
| Delete | → 200, empty body; again → 404 |
| `index` on create | assigned densely in creation order (0, 1, 2) |
| Ordering GET | `{"orderedAclRuleIds":[uuid…]}` in rule order (first match first) |
| Ordering PUT, full permutation | → 200, echoes the new order; the per-rule `index` values follow it (0..n-1) |
| Ordering PUT, subset | → **400** `api.acl-rule.ordering.not-all-acl-rules-specified` (also for `[]` when rules exist) |
| Ordering PUT, unknown id | → **400** `api.request.invalid-reference` |
| Ordering PUT, duplicate id | → **400** `api.acl-rule.ordering.duplicate-acl-rule-id` |

So an ordering resource must send **every** user-defined rule on the site, including rules it does
not manage (see d).

### b. Private APIs through the edge with the key

| Request | Answer |
|---|---|
| `GET /proxy/network/v2/api/site/default/acl-rules`, `qos-rules`, `apgroups` | 200, bare JSON arrays |
| `GET /proxy/network/api/s/default/rest/usergroup`, `get/setting`, `stat/sysinfo`, `/api/self` | 200, `{meta:{rc:"ok"},data:[…]}`; `/api/self` shows the key acting as the owner admin (`is_owner`, `is_super`) |
| v2 write: `POST apgroups {name,device_macs}` | **201**, response has `_id` and a server-minted `external_id` (UUID) |
| v2 `PUT apgroups/{_id}` (full body) | 200 |
| v2 `DELETE apgroups/{_id}` | **204** |
| No key / wrong key on v2, classic and official paths | 401 `{"error":{"code":401,"message":"Unauthorized"}}` (edge) |
| Session login / CSRF | **not needed**: key-only writes succeed on v2 and classic; the response sets no cookie and sends no `X-Csrf-Token` (the header is only listed in `access-control-expose-headers`) |
| Site path segment | private paths take the site **name** (`default`); the site UUID in that position → 404 |
| `:8081` inside the container after the wizard | `POST v2 acl-rules` unauthenticated → **401** (before the wizard it answered unauthenticated; the edge is now the only way in) |

Frame tracer step 1 holds: one `apiKey`/`apiHost`/`allowInsecure` serves official and private
writes. No login fallback is needed on this build.

### c. Private IPv6 ACL rule (v2 `acl-rules`, `type: IPV6`)

| Attempt | Answer |
|---|---|
| `POST {type:IPV6,name,action,enabled}` | 400 bean validation: `aclIndex`, `trafficSource`, `trafficDestination` must not be null (so `acl_index`, `traffic_source`, `traffic_destination` are required on create, as the model says) |
| Same as an IPV4 control (`acl_index`, `ip_acl_protocol:ALL`, `specific_enforcers:[]`, both endpoints `type:NETWORK` with empty lists) | IPV4 → 201; **IPV6 → 500** HTML error page. server.log: `IPv6 ACL is not supported yet`, thrown from the switch-ACL service |
| IPV6 with endpoints `type:IP_OR_SUBNET`, `ips_or_subnets:["2001:db8::/64"]` | 400: `ips_or_subnets` "must be valid IPv4 or Subnet" |
| PUT an existing private IPV4 rule to `type:IPV6` | 500, same exception |
| Read back / update / delete an IPv6 rule | **NOT RUN**: none can be created on Network 10.4.57 |

**Finding: Network 10.4.57 accepts `IPV6` in the enum but refuses to create or convert to it.**
The `IPV6` value in the private model is a placeholder, not a usable variant on the pinned
version. Owner decision 3 (serve IPv6 ACL rules as a private kind) is blocked on the server, not on
the edge: the kind would deserialize but every create would fail with a 500. The next pin
(5.1.42 / Network 10.5.67) should be probed before building it.

### d. Interplay (official ordering with rules the official API did not create)

| Question | Answer |
|---|---|
| With an IPV6 rule present | **NOT RUN** (no IPV6 rule can exist, see c) |
| Stand-in: a rule created through v2 (`type:IPV4`) | gets a server `external_id`; the official list shows it (`totalCount` +1, `origin: USER_DEFINED`) and official ordering GET lists it |
| Official ordering PUT that omits the private-created rule | **400** `not-all-acl-rules-specified`: it fails, it does not drop or reorder around |
| Official ordering PUT that includes it | 200; private `acl_index` becomes dense 0..n-1 in the given order |
| Private create with an `acl_index` already taken | 201 and **no renumbering**: two rules share `acl_index` 0. The next official ordering GET lists both (ties in an order the API chose); an ordering PUT re-densifies |
| Official DELETE of a private-created rule by its `external_id` | 200 |
| v2 item GET `acl-rules/{_id}` | 405 (list + filter, as `kinds.yaml` already records) |
| v2 PUT with only `name` | 400: v2 PUT needs the full body (`acl_index`, endpoints, …) |

Consequence for the ordering resource: it must read the live set and send all ids. If an IPv6
variant ever works and the official API cannot name it, an official ordering PUT would fail with
`not-all-acl-rules-specified` while one exists; that case must be re-measured on the version that
supports IPv6.

### e. Id correspondence

| Object | Official id | Private field |
|---|---|---|
| ACL rule | `id` (UUID) | **`external_id`** equals it, for rules created on either side; private `_id` is a Mongo ObjectId |
| Site | `id` (UUID) | site `external_id`; private paths use the site `name` (= official `internalReference`) |
| Network | `id` (UUID) | `networkconf.external_id`; but the private ACL field `mac_acl_network_id` holds the network's Mongo `_id`, not the UUID |
| AP group (v2) | n/a | server mints `external_id` on create |
| `acl_index` | `index` and the ordering position | same value, 0-based |

### f. Classic writes (`/proxy/network/api/s/default/...`)

| Request | Answer |
|---|---|
| `POST rest/usergroup {name,qos_rate_max_down,qos_rate_max_up}` | 200 (not 201), `{meta:{rc:"ok"},data:[{…,_id,site_id}]}` |
| `GET rest/usergroup/{_id}` | 200, one-element `data` |
| `GET rest/usergroup/{missing}` | **200 with `data:[]`**: not-found is an empty array, not a 404 |
| `PUT rest/usergroup/{_id} {qos_rate_max_down}` | 200, **merge** (other fields kept) |
| `POST` with a duplicate name | 200, a second object (no uniqueness) |
| `POST` with a bad value | 400 `{meta:{rc:"error",msg:"api.err.InvalidPayload"},data:[{validationError:{field,pattern},rc:"error",msg:"api.err.InvalidValue"}]}` |
| `DELETE rest/usergroup/{_id}` | 200 `{meta:{rc:"ok"},data:[]}`; again → 400 `api.err.IdInvalid` |
| Settings list `GET get/setting` | 200, 37 sections, one document each, keyed by `key` |
| Settings singleton chosen: `ntp` | `GET rest/setting/ntp` → 200; `PUT rest/setting/ntp/{_id}` with one field → 200, merge; `POST set/setting/ntp` with one field → 200, merge, same result; bad enum → 400 with `validationError{field,pattern}`; restored to the original values, verified by re-read |
| Secrets in settings | the `mgmt` section returns `x_`-prefixed credential fields in plain text; a settings kind must mark `x_*` fields secret and never log them |

### What this means for slice 2

- (a) is ready to build: key-only private writes work through the edge on v2 and classic.
- (b) the post-wizard controller closes the unauthenticated `:8081` path; the private e2e tier must
  move to the edge with a key, which means the seeded controller (the Tier-2 seed already has one).
- (c) classic needs its own read semantics (empty `data` = gone, merge PUT, 200 on create,
  `rc:"error"` bodies) and settings singletons are addressed by `key` + `_id` (`ntp` works).
- (d) the IPv6 ACL kind cannot be built on Network 10.4.57: the server refuses the variant.
- The official ordering resource must own the complete rule list, and `TrafficMatchMac` (to be
  `AclRuleMac`) needs `networkIdFilter` restored as required.

## Slice 2 step 3 probe (classic kinds; IPv6 on the next pin)

Measured while building slice 2 step 3 (docs/design/private-binding.md §S2.14 rows 20-35). Classic
rows: the Tier-2 seed (wizard completed, key minted) through the edge with the key, Network 10.4.57.
IPv6 rows: UniFi OS Server 5.1.42 (installer SHA-256 `f6111e93…c22f94d`, Network 10.5.67), booted
from its own image before the wizard and reached on `:8081` directly (no key exists before the
wizard, so the official API, and with it the ordering, could not be probed on 10.5.67).

| Request | Answer |
|---|---|
| `GET rest/setting/ntp` | 200 `{meta,data:[the one ntp document]}` with `_id`, `key: ntp`, `site_id` (`get/setting/ntp` answers the same) |
| `GET rest/usergroup` | 200 with every group, including the built-in `Default` (`qos_rate_max_*` = -1, `attr_no_delete`) |
| `POST rest/usergroup` without `qos_rate_max_up` | 200; the field is then absent from the object |
| `POST rest/usergroup` with `qos_rate_max_down: 1` | 400 `api.err.InvalidPayload`, `validationError{field: qos_rate_max_down, pattern}` |
| `DELETE rest/usergroup/{absent or malformed id}`, `GET rest/usergroup/{malformed id}` | 400 `api.err.IdInvalid` for both kinds of id; the GET answers 200 `data:[]` |
| `PUT rest/setting/ntp/{_id}` with `setting_preference: auto` and other server names | 200, but the answer and every later read hold the controller's own four servers: in `auto` mode the controller puts them back, whatever the body says (found by the edge e2e drop-reset row) |
| 10.5.67 `stat/sysinfo` before the wizard | 200, `version` 10.5.67 |
| 10.5.67 v2 `POST acl-rules` `type: IPV4` (full body) | 201, as on 10.4.57 |
| 10.5.67 v2 `POST acl-rules` `type: IPV6` with IPv6 subnets | 400 "must be valid IPv4 or Subnet" on both endpoints |
| 10.5.67 v2 `POST acl-rules` `type: IPV6` with IPv4 subnets; `PUT` of an IPV4 rule to `IPV6` | 500 (an HTML error page); `server.log`: "IPv6 ACL is not supported yet" |

So no measured Network version accepts IPv6 ACL rules yet: `unifi:network:AclRuleIpv6` stays built
and Tier-1 tested but not served (`servedFrom` unset), and its live round trip and its interplay with
the official ordering remain not run. The probe container and image were removed; the IPV4 test rule
was deleted.
