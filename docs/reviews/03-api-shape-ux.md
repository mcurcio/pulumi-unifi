# Review 03 — API / Resource Shape Soundness & Developer UX

**Reviewer hat:** Infrastructure engineer writing Pulumi programs against `pulumi-unifi`.
**Scope:** Consumer-facing surface only — generated `schema.json` + `sdk/python/pulumi_unifi/`.
**Artifacts inspected (build outputs, regenerated):**
`provider/cmd/pulumi-resource-unifi/schema.json` (21 resources, 50 functions, 158 types),
`sdk/python/pulumi_unifi/**`.

---

## Executive summary

The pipeline produces a *working* provider, but the public API as it stands would frustrate and
mislead a real consumer. The damage clusters in four areas:

1. **The per-variant resource split leaks its mechanism into the API.** Resources are named after the
   *discriminator variant* (`Standard`, `IotOptimized`, `Mac`, `Ports`, `Gateway`, `Unmanaged`,
   `Ipv4`, `Ipv4Addresses`, …) with **no parent entity in the token**. A bare
   `unifi:sites/v1:Standard` does not tell you it is a WiFi network; `Mac`/`Ports` are meaningless;
   `Ipv4` vs `Ipv4Addresses` are two near-collision names for completely different shapes. Worse, the
   split does **not** remove the discriminator — every variant still has a **required, free-form,
   un-enumerated `type` string input** the consumer must guess. So the consumer pays the cost of the
   split (token explosion, lost context) *and* still pays the cost of the union (must set a magic
   discriminator). This is the single biggest UX problem.

2. **Upstream operationId / schema-name garbage is passed through verbatim.** `getCountrie`,
   `getFirewallPolicie`, `getDpiApplicationCategorie` (broken singularization);
   `getIntegrationDnsARecordDto` and 14 other `getIntegration*Dto` (internal DTO schema names leaked
   as public data-source tokens); `getVPN_client_connection_details`, `getGateway_managed_network_details`
   (snake_case bleeding into camelCase tokens); `listTrafficMatching` vs `listTrafficMatchings` and
   `getFirewallPolicie` vs `getFirewallPolicy` (near-duplicate functions distinguished only by an `s`
   / an `e`). The pipeline has a sanitize layer (`SanitizeSpecBytes`) but it does not normalize
   operationIds, so all of this reaches the SDK.

3. **Zero descriptions at the resource and function level.** All 21 resources and all 50 functions
   have an **empty top-level `description`**. Every data source's generated docstring is the literal
   filler *"Use this data source to access information about an existing resource."* Property-level
   docs survive for *some* fields, but the registry/IDE entry point for each resource is blank.

4. **Pagination internals are public.** `*Page` data-source names plus `count`/`limit`/`offset`/
   `total_count` result fields are exposed even though this provider auto-aggregates pages — so the
   name promises a page and the result advertises paging knobs that no longer mean anything.

There are real wins too (clean flat resources like `FirewallZone`/`Voucher`, preserved string enums,
well-written provider-config docs) — see the last section.

---

## Findings (severity-ordered)

### F1 — Discriminator `type` is still a required, unguided input on every split variant — CRITICAL
The whole justification for one-resource-per-variant is "consumers pick the concrete variant instead
of setting a discriminator." That promise is broken: the discriminator survives the split as a
**required** input with **no enum, no default, no const**.

- `schema.json` — `unifi:sites/v1:ARecord`: `requiredInputs: ["enabled","type"]`, and
  `inputProperties.type == {"type":"string"}`. No `default`, no `enum`.
- Same for `AaaaRecord`, `CnameRecord`, `MxRecord`, `SrvRecord`, `TxtRecord`, `ForwardDomain`,
  `Standard`, `IotOptimized`, `Mac`, `Ipv4`, `Ports`, `Ipv4Addresses`, `Ipv6Addresses`, and the
  managed-network `management` discriminator on `Gateway`/`Switch`/`Unmanaged` (`management` is a
  bare required `string`).
- In Python this means `ARecord("rec", enabled=True, type="???")` — the consumer must know the exact
  upstream magic string (`"A"`? `"A_RECORD"`? `"a"`?) with **nothing in the type, no enum dropdown,
  no docstring** to tell them, and a wrong value is only caught at apply time by the controller.

**Why it hurts:** maximally surprising. The consumer already chose `ARecord`; being forced to *also*
type a redundant required string they can't discover is the worst-of-both-worlds.
**Fix (in-pipeline):** in the gen post-process (alongside `coalesceDiscriminatedCRUD`), for each
split variant set the discriminator property to a single-value `enum`/`const` and a `default`, and
drop it from `requiredInputs` (the framework can inject it on the wire). This is deterministic and
local to the same fix layer that already rewrites these resources.

### F2 — Variant resource tokens lose all parent context / collide — CRITICAL (UX)
Tokens are named after the variant alone, so the resource list reads:
`Standard`, `IotOptimized`, `Mac`, `Ports`, `Ipv4`, `Ipv4Addresses`, `Ipv6Addresses`, `Gateway`,
`Switch`, `Unmanaged`, `ARecord`…`TxtRecord`, `ForwardDomain` (`__init__.py` resource_modules block).

- *Standard what?* `unifi:sites/v1:Standard` / `:IotOptimized` are WiFi networks — unguessable.
- `Mac`, `Ports` as top-level resource names are content-free.
- `Ipv4` (a full traffic-match rule: `action`, `destinationFilter`, `sourceFilter`, `protocolFilter`,
  `enforcingDeviceFilter`, `index`…) vs `Ipv4Addresses` (just `items`, `name`, `type`) are two
  near-identical names for radically different resources — a guaranteed mistake.

And the split is mostly redundant: `Standard` vs `IotOptimized` **share 18 input properties** and
differ in only ~10; `Gateway`/`Switch`/`Unmanaged` share 6 core props (`enabled`, `management`,
`name`, `vlanId`, `dhcpGuarding`, `siteId`) and differ by 0–4. So the consumer is asked to memorize
which of several 80%-identical resources to instantiate, with no naming help.

**Why it hurts:** poor discoverability (you can't find "the WiFi resource" by name), high error rate
(`Ipv4` vs `Ipv4Addresses`), and the cognitive load the split was supposed to remove.
**Fix:** *Best UX* would be a single resource per entity with a discriminated/tagged-union body
(`WifiNetwork`, `ManagedNetwork`, `DnsRecord`, `TrafficMatchingList`) — that is the idiomatic Pulumi
shape and matches the REST entity. That is an upstream pulschema behavior change. *Cheaper in-pipeline
mitigation:* prefix variant tokens with the parent entity so they at least carry context, e.g.
`WifiBroadcastStandard` / `WifiBroadcastIotOptimized`, `ManagedNetworkGateway`,
`TrafficMatchIpv4` / `TrafficMatchIpv4Addresses`, `DnsARecord`. Even a token rename hugely improves
discoverability while keeping the per-variant model.

### F3 — Internal DTO/schema names leak as public data-source tokens — HIGH
Fifteen data sources are named after internal serialization DTOs:
`getIntegrationDnsARecordDto`, `getIntegrationDnsAaaaRecordDto`, `getIntegrationDnsCnameRecordDto`,
`getIntegrationDnsForwardDomainPolicyDto`, `getIntegrationDnsMxRecordDto`, `…SrvRecordDto`,
`…TxtRecordDto`, `getIntegrationIotOptimizedWifiBroadcastDetailDto`,
`getIntegrationStandardWifiBroadcastDetailDto`, `getIntegrationIpAclRuleDto`,
`getIntegrationMacAclRuleDto`, `getIntegrationLocalLagGlobalDto`, `getIntegrationMcLagGlobalDto`,
`getIntegrationSwitchStackLagGlobalDto` (schema.json `functions`; SDK files
`sdk/python/pulumi_unifi/sites/v1/get_integration_*_dto.py`).

**Why it hurts:** `unifi.sites.v1.get_integration_dns_a_record_dto()` is not something a consumer can
guess or wants to type; `Dto` and `Integration` are implementation words that should never appear in
a public API. It also creates a confusing **double surface** — there is both `getIntegrationDnsARecordDto`
(read one A-record DTO) and the `ARecord` resource, with no obvious relationship.
**Fix (in-pipeline):** strip `Integration` prefix and `Dto` suffix and re-singularize during gen
(operationId/token normalization pass). Upstream fix is cleaner operationIds, but a token rewrite in
the existing fix layer is deterministic and within scope.

### F4 — Broken singularization & casing in tokens — HIGH
- `getCountrie` (module `countries/v1`), `getFirewallPolicie`, `getDpiApplicationCategorie` — naive
  trailing-`s` stripping produced `Countrie`/`Policie`/`Categorie`. (`get_countrie.py` even returns a
  *list* of all countries in `data`, so the singular is doubly wrong.)
- `getGateway_managed_network_details`, `getSwitch_managed_network_details`,
  `getUnmanaged_network_details`, `getTeleport_client_connection_details`,
  `getVPN_client_connection_details`, `getWired_client_details`, `getWireless_client_details` —
  snake_case fragments wedged into otherwise-camelCase tokens; `getVPN_…` also keeps an all-caps acronym.

**Why it hurts:** these read as bugs to anyone scanning the API; inconsistent casing breaks
auto-complete muscle memory and looks unmaintained.
**Fix (in-pipeline):** a token-normalization pass (proper singularization with an exceptions table,
consistent camelCase). Upstream operationIds are the root cause but the pipeline already owns a
spec-fix layer.

### F5 — Near-duplicate functions distinguished only by a letter — HIGH
- `listTrafficMatching` (inputs `siteId`, `trafficMatchingListId` → it's a **get-one**) vs
  `listTrafficMatchings` (inputs `siteId` → list-all). Both prefixed `list`; the singular one is
  actually a get-by-id mislabeled `list`.
- `getFirewallPolicie` (inputs `siteId` → list-all) vs `getFirewallPolicy` (inputs `firewallPolicyId`,
  `siteId` → get-one).

**Why it hurts:** picking the wrong one is trivially easy and the names give no hint which is which.
**Fix (in-pipeline):** rename to a consistent `get<Entity>` (one) / `list<Entities>` (many) scheme
during the token pass; this also subsumes F4's pluralization issue.

### F6 — No resource- or function-level descriptions anywhere — HIGH
All 21 resources and all 50 functions have `description == ""` in schema.json. SDK data-source
docstrings are the filler *"Use this data source to access information about an existing resource."*
(e.g. `get_countrie.py:85`, `get_wifi_broadcast_page.py:86`). Resource class docstrings are only
*"Create a X resource with the given unique name, props, and options."* (e.g. `a_record.py:117`,
`standard.py:425`).

**Why it hurts:** the Pulumi Registry page and IDE hover for every resource/data source is blank or
filler — the consumer can't tell what `Standard` *is* without reading source. Property-level docs do
exist for some fields (good — see Voucher), which makes the empty top level more jarring.
**Fix (in-pipeline):** synthesize a description from the entity + variant (e.g. "An A (IPv4 address)
DNS record." / "A WiFi network broadcast (standard profile).") in the gen layer; upstream the proper
fix is operation `summary`/`description` in the spec.

### F7 — Pagination internals leak into the public API — MEDIUM/HIGH
List data sources are named `*Page` (`getWifiBroadcastPage`, `getAclRulePage`, `getDnsPolicyPage`,
`getNetworksOverviewPage`, `getLagPage`, `getPendingDevicePage`, …) and their result type exposes
`count`, `limit`, `offset`, `total_count` alongside `data` (`get_wifi_broadcast_page.py:44-67`).

Per the design (CLAUDE.md / DESIGN §0), `OnPostInvoke` **auto-aggregates all pages** into one result.
So a consumer who calls `get_wifi_broadcast_page()` gets *every* broadcast in `data`, but the name
says "Page" and the result hands them `limit`/`offset`/`total_count` that are now meaningless
(there is no second page to fetch, and these fields reflect a single underlying request, not the
aggregate). This is actively misleading: a consumer may try to paginate and get inconsistent numbers.

**Why it hurts:** the name promises pagination the provider has deliberately removed; the leftover
knobs invite incorrect usage and clutter the result.
**Fix (in-pipeline):** rename `*Page` → plural list (`getWifiBroadcasts`, etc.) and drop
`limit`/`offset`/`total_count` (or at least `limit`/`offset`) from the aggregated result type, keeping
`data` (and optionally `count`/`total_count` as a true aggregate length). The aggregation already
happens in this repo's `OnPostInvoke`, so trimming the result type belongs here, not upstream.

### F8 — Verb-as-resource: `AdoptDevice` and `Voucher` are RPCs modeled as declarative resources — MEDIUM/HIGH
- `AdoptDevice` (`adopt_device.py`) is an **imperative verb** as a resource token, with required
  `macAddress` + `ignoreDeviceLimit` and no nominal entity. "Adopt" is an action; what does
  `pulumi destroy` mean — un-adopt? What does `pulumi up` after a drift do? The lifecycle semantics
  are undefined for an action verb. A reader expects a noun (`Device` / `AdoptedDevice`).
- `Voucher` (`voucher.py`) takes `count` ("Number of vouchers to generate", default 1) and emits a
  **list** output `vouchers: Sequence[HotspotVoucherDetails]`. This is a batch-generate RPC behind a
  single resource with a single `id`. `count` also visually collides with Pulumi's own resource
  options idiom. One logical resource that secretly manages N vouchers has ambiguous identity/update
  semantics (change `count` from 3→2 — which two survive?).

**Why it hurts:** these break the declarative mental model; drift/update/delete behavior is
surprising. `AdoptDevice` in particular will confuse anyone expecting CRUD.
**Fix:** these are upstream API shapes (the official API genuinely models adoption and voucher-batch
as actions). In-pipeline, at minimum rename `AdoptDevice` → a noun and document the batch/RPC
semantics; consider listing them in `ExcludedPaths` as resources and exposing them via a more honest
mechanism if true declarative semantics can't be provided.

### F9 — Numeric enums dropped → lost validation — MEDIUM
`broadcastingFrequenciesGHz` is `array<number>` with no enum (`Standard`/`IotOptimized`
inputProperties), because numeric enums are stripped in sanitization. The valid set (2.4 / 5 / 6) is
lost, so `broadcasting_frequencies_g_hz=[3.0]` type-checks fine and only fails at the controller.

**Why it hurts:** silent loss of compile-time/preview validation for a field where the legal values
are tiny and well-known; the consumer gets no IDE hint.
**Fix (in-pipeline):** preserve numeric enums where the sanitizer currently drops them (or
re-attach known numeric enums for specific fields). Note string enums *are* preserved and are good
(F-win below), so this is an inconsistency, not a blanket limitation.

### F10 — `siteId` is global-config-only; no per-resource multi-site — MEDIUM
`siteId` is a provider-config var (schema.json `config.variables.siteId`, default `"default"`). Each
resource *does* expose an optional `siteId` input (e.g. `a_record.py:82`, `firewall_zone.py:63`), but
DESIGN §8 states per-resource override is a future item and the framework sets the path param once at
configure time — so the per-resource `siteId` input is present but **not honored**. A consumer
managing two sites today must instantiate **two provider instances** and pass `provider=` on every
resource.

**Why it hurts:** the optional `siteId` on every resource is a **trap** — it looks settable but
silently does nothing, which is worse than not exposing it. Multi-site (a common UniFi setup) is
clunky.
**Fix:** either honor the per-resource `siteId` in the framework glue (preferred), or remove the
non-functional per-resource `siteId` input until it works so it doesn't mislead. Per-resource wiring
is in-repo glue territory.

### F11 — Duplicate enum types from create/update/object variants — LOW
`ACLRuleAction`, `ACLRuleObjectAction`, `ACLRuleUpdateAction` are three identical `["ALLOW","BLOCK"]`
enums; `ConnectionStateFilterItem` vs `FirewallPolicyConnectionStateFilterItem`; `IpsecFilter` vs
`FirewallPolicyIpsecFilter`. Type-name fragmentation bloats the `outputs`/`_enums` surface and makes
imports confusing (which `…Action` do I import for an update?).
**Why it hurts:** minor, but adds noise and import ambiguity.
**Fix (in-pipeline):** de-dupe structurally identical enums to a shared type name. Upstream the root
cause is per-operation schema duplication.

### F12 — Property-name mangling around digits/acronyms — LOW
`channel2g_locked_to6` (`standard.py:117`), `dtim_period2g_locked_to3`,
`data_usage_limit_m_bytes` (`voucher.py:99`, from `dataUsageLimitMBytes` — "MBytes" split to
`m_bytes`), `basic_data_rate_kbps_by_frequency_g_hz`, `broadcasting_frequencies_g_hz` (`GHz` →
`g_hz`). These are the language-codegen's camel→snake rules meeting awkward source names.
**Why it hurts:** mildly ugly and hard to type; `m_bytes`/`g_hz` look like bugs.
**Fix:** largely cosmetic and partly intrinsic to Pulumi's casing rules; cleaner source property
names (acronym handling) upstream would help. Low priority.

### F13 — No documented import ID / import UX — LOW/MEDIUM
Every resource's `get()` takes an opaque `id` (`a_record.py:179`, `standard.py:557`) and the only
extra output is `metadata` (which for `ARecord` is just `{origin}` — no `id` echoed). There is no
`description` documenting the import ID format or a `pulumi import` example, so a consumer importing
an existing record won't know what string to supply.
**Why it hurts:** import ("brownfield adoption") is a common first task and is undocumented.
**Fix (in-pipeline):** add per-resource import docs/examples in the synthesized description (F6).

---

## What's good (UX wins worth keeping)

- **Provider config is excellent.** `apiKey` (marked `secret`), `apiHost`, `siteId`, `allowInsecure`
  all have clear, accurate descriptions — `allowInsecure` even warns about losing 429 retry
  (schema.json `config.variables`). This is the best-documented part of the surface.
- **Flat, non-discriminated resources are clean and idiomatic.** `FirewallZone`
  (`name`, `network_ids`) and `Voucher`'s property set are exactly what you'd hand-write; property
  docstrings are genuinely useful ("List of Network IDs", the detailed `time_limit_minutes` note).
- **String enums survive and are good.** `ACLRuleAction` (`ALLOW`/`BLOCK`),
  `RouterAdvertisementConfigurationPriority` (`LOW`/`MEDIUM`/`HIGH`),
  `WirelessRadioOverviewWlanStandard` (802.11a…be), day-of-week, PoE standards — 25 enums give real
  validation and IDE completion.
- **Required-vs-optional is mostly sensible** for the flat resources (`FirewallZone` requires only
  `networkIds`; `Voucher` only `timeLimitMinutes`).
- **CRUD coalescing works** — every resource has a real `get()`/round-trip, so the create-only-stub
  problem called out in CLAUDE.md is genuinely fixed at the consumer surface.
- **Module layout is reasonable** (`sites/v1`, `countries/v1`, `dpi/v1`, `info/v1`,
  `pending_devices/v1`) and `apiKey` secrecy is correct.

---

## Priority recommendation

If only a few things get fixed, do these — all are in-pipeline (token/description rewrites in the
same deterministic gen-fix layer that already hosts `coalesceDiscriminatedCRUD`):

1. **F1** — make the discriminator `type`/`management` a defaulted const, drop from required. (Removes
   the worst day-one trap.)
2. **F2** — prefix variant tokens with the parent entity (`WifiBroadcastStandard`, `DnsARecord`,
   `TrafficMatchIpv4Addresses`, `ManagedNetworkGateway`). (Restores discoverability.)
3. **F3 + F4 + F5** — one token-normalization pass: strip `Integration*Dto`, fix singularization and
   casing, settle `get`(one)/`list`(many).
4. **F6** — synthesize resource/function descriptions.
5. **F7** — drop `Page` from list names and trim `limit`/`offset`/`total_count` from aggregated results.

These together convert the surface from "obviously machine-generated, several apparent bugs" to
"clean and idiomatic" without changing the underlying per-variant model or requiring upstream changes.
