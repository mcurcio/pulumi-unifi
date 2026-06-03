# Pulumi Provider Best-Practices Review — pulumi-unifi

**Reviewer role:** adversarial Pulumi provider specialist
**Scope:** Pulumi-specific correctness and conventions (schema, tokens, config/secrets,
distribution, SDK, diff/import behavior). Spec-quality issues are flagged separately from
this project's own design choices.
**Artifacts inspected:** `provider/pkg/gen/schema.go`, `provider/pkg/gen/openapi_fixes.go`,
`provider/pkg/provider/provider.go`, `provider/cmd/pulumi-gen-unifi/main.go`,
generated `schema.json` / `metadata.json`, and the generated `sdk/python` tree (resources,
data sources, provider, config, `pulumi-plugin.json`).

---

## Executive summary

The hand-written glue is genuinely thin and mostly correct: provider-level `apiKey` is secret
end-to-end (`pulumi.Output.secret` wraps it in the SDK), env-var defaults are wired on every config
key, `allowInsecure` is modeled sensibly, and `apiHost` validation is a nice defensive touch. The
codegen determinism work (`ensureSchemaTitles`, `coalesceDiscriminatedCRUD`) is thoughtful.

The serious problems are almost all in **what the generated schema looks like to an end user**, and
they will produce a poor `pulumi up`/`preview`/`refresh` experience:

1. **The discriminated-variant split is leaky and unsafe at the CRUD layer.** Seven DNS record
   "resources" (`ARecord`, `AaaaRecord`, …) all share one create endpoint and one read endpoint, with
   the discriminator (`type`) modeled as a *free-form string input*. The read path cannot distinguish
   which variant an id belongs to, and nothing stops a user creating an `ARecord` with `type:
   "AAAA_RECORD"`. Same for WiFi `Standard`/`IotOptimized`, networks `Gateway`/`Switch`/`Unmanaged`,
   ACL `Mac`/`Ipv4`, traffic-matching `Ports`/`Ipv4Addresses`/`Ipv6Addresses`. (Root cause: pulschema +
   upstream spec; severity is the project's because it ships as-is.)
2. **Resource and function tokens are non-idiomatic and information-losing.** `unifi:sites/v1:Standard`,
   `:Mac`, `:Ports`, `:Ipv4`, `:Unmanaged` give no hint of the entity. Functions include typos
   (`getCountrie`, `getFirewallPolicie`), snake_case leakage (`getWired_client_details`), and internal
   DTO names (`getIntegrationDnsARecordDto`).
3. **Zero descriptions on all 21 resources and all 50 functions.** Property descriptions are only
   partially present (from the spec). This is the single biggest registry/IDE/`pulumi up` UX gap.
4. **No `replaceOnChanges` anywhere**, including immutable/identity fields (the `type` discriminator,
   `vlanId`, `siteId`). Mutating one of these will attempt an in-place update that the API rejects, or
   silently no-ops, instead of a replace.
5. **Two resources are actions/batches, not lifecycles.** `Voucher` is a *batch-create* (`count` in,
   `vouchers[]` out); `AdoptDevice` is an adopt action whose "delete" forgets the device. Both
   misrepresent the resource model.
6. **`DISPLAY/PluginDownloadURL` and versioning need a release to validate.** `PluginDownloadURL` is
   `github://api.github.com/mcurcio/pulumi-unifi` (no OS/ARCH/VERSION template — correct for the
   `github://` scheme, but DESIGN §7 describes a different, templated URL). Schema `version` is blank
   and `pulumi-plugin.json` carries no version; nothing has cut a release yet, so auto-install is
   unproven.

None of these block the read-path MVP, but several (1, 4, 5) will produce broken or surprising
`pulumi up` behavior the moment writes are exercised against a live controller.

---

## Findings (severity-ordered)

### Critical

#### C1. Discriminated-variant resources share one create + one read endpoint with a free-string discriminator
`provider/cmd/pulumi-resource-unifi/metadata.json` (crudMap), e.g. `ARecord`/`AaaaRecord`/`CnameRecord`/`MxRecord`/`SrvRecord`/`TxtRecord`/`ForwardDomain` all bind
`c: /v1/sites/{siteId}/dns/policies`, `r/d/p: /v1/sites/{siteId}/dns/policies/{dnsPolicyId}`.
`schema.json` resource `unifi:sites/v1:ARecord.inputProperties.type = {"type":"string"}`.

**What's wrong.** The per-variant split (`coalesceDiscriminatedCRUD` fills R/U/D/P from the shared
item path) gives every variant *the same* read endpoint. On `pulumi refresh`/read, GET
`/dns/policies/{id}` returns whatever record type that id actually is — there is no variant filtering.
If the stored record is an `AAAA` but the program declares an `ARecord`, the read either mis-maps
fields onto the `ARecord` schema or diffs spuriously forever. Worse, `type` is a plain `string` input
with no `const`/enum binding it to the variant, so a user can create an `ARecord` resource that POSTs
`type: "AAAA_RECORD"`; Pulumi then tracks an `ARecord` URN over an AAAA record.

**Why it matters for Pulumi users.** Non-deterministic refresh, perpetual diffs, and the ability to
silently create the wrong entity. This is exactly the class of bug Pulumi's resource model is meant to
prevent.

**Root cause.** Upstream spec (`oneOf`+`discriminator`) + pulschema's per-variant strategy. The
*shipping decision* is this project's.

**Recommended fix.** At minimum, in the gen layer, pin each variant's `type` input to a `const`/enum
equal to its discriminator value (pulschema knows the mapping; bake it into the schema property so the
variant is self-consistent). Longer term, evaluate collapsing each discriminated family back into a
single resource with a required discriminator enum (a tagged-union-style resource), which is closer to
how a hand-written provider would model `dns/policies`. Document loudly that refresh/import on these
families is only safe when the live record's type matches the chosen variant.

#### C2. `Voucher` and `AdoptDevice` are modeled as CRUD resources but are actions/batches
`metadata.json`: `Voucher` `{c: …/hotspot/vouchers, r/d: …/{voucherId}}` (no update);
`schema.json` `Voucher` has input `count` and output `vouchers` (plural).
`AdoptDevice` `{c: …/devices, r: …/{deviceId}, d: …/{deviceId}}` (no update); inputs
`macAddress`, `ignoreDeviceLimit`.

**What's wrong.** `POST /hotspot/vouchers` is a *batch create* — `count` vouchers are minted and
returned in `vouchers[]`. Modeling it as one Pulumi resource with a single `{voucherId}` read path
means the resource id maps to one voucher while the create produced N; refresh/update/delete cannot
round-trip. `AdoptDevice` is an imperative adopt; its "delete" un-adopts/forgets a physical device,
and `ignoreDeviceLimit` is a create-time flag that is also surfaced as an output property.

**Why it matters.** `pulumi up` then `pulumi refresh` will not converge; `pulumi destroy` on
`AdoptDevice` performs a destructive device operation the user may not expect from "deleting a Pulumi
resource."

**Root cause.** Upstream API shape; shipping decision is the project's.

**Recommended fix.** Add both collection POSTs to `ExcludedPaths` (or a new "actions" exclusion set)
so they do not surface as resources, or model them deliberately (e.g. `Voucher` as `count == 1` only,
documented). At minimum document the batch/action semantics; do not present them as ordinary CRUD.

### High

#### H2. No `replaceOnChanges` / immutable-field handling anywhere
`schema.json`: 0 occurrences of `replaceOnChanges`. Resources expose mutable inputs for fields that are
identity/immutable: the `type` discriminator (every variant), `vlanId` (`Gateway`), and `siteId` (all
site-scoped resources).

**What's wrong.** Changing any of these should force a *replace*. With the generic framework's
PATCH/PUT update path and no `replaceOnChanges`, Pulumi will attempt an in-place update. The API will
either reject it (apply fails) or ignore it (state drifts from program). Moving a resource between
sites (`siteId`) is the clearest case: it must be a replace, not an update.

**Why it matters.** Failed applies and silent drift — both erode trust in `preview`.

**Recommended fix.** In the gen layer, set `replaceOnChanges` (and ideally drop from `inputProperties`
or mark output-only where the API never accepts them) for the discriminator `type`, `siteId`, and any
field the spec marks read-only/immutable. pulschema does not infer this; it needs an editorial pass
analogous to `ExcludedPaths`.

#### H3. Zero top-level descriptions on every resource and function
`schema.json`: all 21 resources and all 50 functions have no `description`. Verified
programmatically.

**What's wrong.** These descriptions are what Pulumi surfaces in the registry, in editor hovers, and
in `pulumi up` resource summaries. The package, config keys, and *some* properties have descriptions
(from the spec), but no resource/data-source does.

**Why it matters.** A registry-published provider with no resource docs is effectively undocumented;
this is a headline best-practices miss.

**Root cause.** The upstream spec has sparse `summary`/`description` on operations/schemas, but the
gen layer makes no attempt to synthesize even a one-line description (e.g. from the entity name + verb,
or the OpenAPI operation `summary`). Project choice.

**Recommended fix.** In `PulumiSchema`/post-process, backfill a `Description` on each resource and
function from the OpenAPI operation `summary`/`description` (pulschema has these on the source op) or a
deterministic template. Even `"Manages a UniFi <entity>."` / `"Looks up a UniFi <entity>."` is a large
improvement.

#### H4. Non-idiomatic, information-losing, and typo'd tokens
`schema.json` resources: `unifi:sites/v1:Standard`, `:IotOptimized`, `:Mac`, `:Ports`, `:Ipv4`,
`:Ipv4Addresses`, `:Ipv6Addresses`, `:Unmanaged`, `:Gateway`, `:Switch`.
Functions: `getCountrie`, `getFirewallPolicie` (typo'd plurals), `getWired_client_details`,
`getWireless_client_details`, `getTeleport_client_connection_details` (snake_case),
`getIntegrationDnsARecordDto`, `getIntegrationStandardWifiBroadcastDetailDto` (internal DTO names).

**What's wrong.** Pulumi convention is PascalCase resources and camelCase functions whose names read
as the *entity*. `Standard`/`Mac`/`Ports`/`Unmanaged` are discriminator values with no entity context
— a user reading a program sees `unifi.sites.v1.Standard("x")` and cannot tell it is a WiFi broadcast.
`getCountrie`/`getFirewallPolicie` are mangled singularizations. `getWired_client_details` mixes
camel and snake. `getIntegration…Dto` leaks the upstream type system.

**Why it matters.** This is the public API surface across every language SDK; it is hard to change
after publish without breaking consumers.

**Root cause.** pulschema derives names from schema titles/discriminator keys; the bad singularization
and snake_case are pulschema/spec artifacts. Project ships them.

**Recommended fix.** Add a deterministic token-rename map in the gen layer (e.g. `Standard` →
`StandardWifiBroadcast`, `Mac` → `MacAclRule`, `getCountrie` → `getCountries`/`getCountry`,
strip the `Integration…Dto` wrapper). This is the same kind of editorial layer as `ExcludedPaths` and
keeps the spec as source of truth while fixing names once.

#### H5. Redundant data sources: many functions bind the same read endpoint
`metadata.json`: 7 functions on `/dns/policies/{dnsPolicyId}`, 4 on `/clients/{clientId}`, 3 on
`/networks/{networkId}`, 3 on `/switching/lags/{lagId}`, 2 on `/wifi/broadcasts/{wifiBroadcastId}`,
2 on `/acl-rules/{aclRuleId}`. Plus list-vs-item duplicates (`getFirewallPolicie` list +
`getFirewallPolicy` item; `listTrafficMatching` item + `listTrafficMatchings` list).

**What's wrong.** Each `oneOf` variant produces its own getter against the *same* endpoint, returning
the *same* bytes typed to a different DTO. A user has 7 ways to read one DNS policy, none of which can
guarantee the returned record matches the chosen variant (same issue as C1, read-side).

**Why it matters.** Bloated, confusing data-source surface; the variant getters are traps (wrong-type
results). 50 data sources is inflated by this fan-out.

**Recommended fix.** Prune the per-variant `getIntegration…Dto` getters via `ExcludedPaths`/a getter
allowlist, keeping one canonical getter per endpoint (the page getter + a single item getter).

### Medium

#### M1. DESIGN §7 misdescribes `PluginDownloadURL`; auto-install is unproven
`provider/pkg/gen/schema.go:119` `PluginDownloadURL: "github://api.github.com/mcurcio/pulumi-unifi"`.
DESIGN §7 claims it is "templated with `${VERSION}/${OS}/${ARCH}`."

**What's wrong.** The actual value uses the `github://` scheme, where Pulumi *computes* the release
asset name (`pulumi-resource-unifi-vX.Y.Z-os-arch.tar.gz`) itself — there is no `${OS}/${ARCH}`
template, so the value is *correct* for that scheme but the DESIGN doc is wrong. The risk is that the
eventual GitHub release assets must match Pulumi's exact naming convention, and there is no release
workflow yet (`.github/workflows` absent), so this is untested. `github://api.github.com/...` also
requires the repo's releases to be public and the asset checksums to be present.

**Why it matters.** If the asset names don't match, `pulumi up` auto-install fails on a clean machine
— the Phase 5 "done when" criterion.

**Recommended fix.** Fix DESIGN §7 to describe the `github://` scheme accurately. When Phase 5 lands,
ensure the release job emits assets named exactly as Pulumi expects for `github://`, and add a
clean-machine install smoke test.

#### M2. Schema `version` blank and the `-version` flag is dead code
`provider/cmd/pulumi-gen-unifi/main.go:48` parses `-version` into `version`, but it is never used; line
~100 `pkgSpec.Version = ""` blanks it deliberately. `version.Version = "0.0.1"` is `-X`-injected into
the binary; `sdk/python/.../pulumi-plugin.json` has no `version`; `pyproject.toml` version is `0.0.0`.

**What's wrong.** Blanking the schema version for determinism is a legitimate pattern *if* the binary
injects it at `GetSchema` time — but that depends on the framework actually patching the version into
the returned schema, which is not verified by any test here. The `-version` flag being parsed-and-
discarded is a code smell that suggests the wiring was intended and dropped. The SDK `pyproject`
version `0.0.0` will publish as `0.0.0` unless the release job overrides it.

**Why it matters.** If the served schema reports no version, `pulumi plugin`/SDK version pinning and
`PluginDownloadURL` resolution can misbehave. A `0.0.0` PyPI package is not installable as a real
release.

**Recommended fix.** Either remove the dead `-version` flag or use it (write it into the schema and
let determinism be guarded by generating with a fixed version in the test). Add a test asserting
`GetSchema` returns a non-empty `version` matching the binary. Ensure the release job sets the SDK
package version from the tag.

#### M3. Config descriptions diverge between `Config` and `Provider.InputProperties`
`provider/pkg/gen/schema.go`: Config `apiHost` (line 67) = "…e.g. \"192.168.1.1\"… Overrides the host
in the generated server URL." vs InputProperties `apiHost` (line 99) = "The UniFi controller host (and
optional :port)." Same divergence for `siteId` (71 vs 106) and `allowInsecure` (75 vs 113). Only
`apiKey` (62/91) matches.

**What's wrong.** The two blocks describe the same four settings but with different, less-helpful text
on the Provider side. Pulumi surfaces both (Config block → ambient config docs; InputProperties →
explicit `Provider(...)` constructor docs), so users see inconsistent help depending on entry point.
Confirmed in the SDK: `config/vars.py` carries the richer Config strings; `provider.py` carries the
terser InputProperties strings.

**Why it matters.** Minor, but it's duplicated source that will drift further; the terser side drops
the useful examples and the "defaults to default" note.

**Recommended fix.** Define each description once (a `map[string]string`) and reference it from both
`Config.Variables` and `Provider.InputProperties`.

#### M4. `apiKey` env default and secrecy in ambient config
`sdk/python/.../config/vars.py` reads `api_key` via `__config__.get('apiKey')` (not `get_secret`).
Provider-level path (`provider.py:168`) correctly wraps with `pulumi.Output.secret`.

**What's wrong.** The ambient-config accessor for a `secret: true` config value uses the non-secret
getter, so a program reading `pulumi_unifi.config.api_key` gets an unmarked plaintext value. This is
Pulumi's Python codegen default and the secret flag *is* honored where it matters (stored config is
encrypted; provider input is secret-wrapped), so impact is limited, but a consumer who echoes
`config.api_key` would leak it un-redacted.

**Why it matters.** Low-probability leak path; worth a doc note. Not a schema bug per se.

**Recommended fix.** Document that the key should be set via `UNIFI_APIKEY`/secret config and not read
back through `config.api_key`. (The schema is correct; this is codegen behavior.)

#### M5. `siteId` modeled as both provider config and per-resource input, with no description/default
`schema.json`: every site-scoped resource has `inputProperties.siteId = {"type":"string"}` (no
`description`, no `default`), while `siteId` is also provider config with env `UNIFI_SITEID`.

**What's wrong.** The per-resource `siteId` overrides the global one (via the framework's
resource-level-beats-global rule and `GetGlobalPathParams`), which is a reasonable feature — but it is
undocumented (no description), has no default in the schema, and (per H2) is not `replaceOnChanges`.
Users won't know it exists or that setting it moves the resource to another site.

**Why it matters.** Hidden, undocumented behavior on an identity-affecting field.

**Recommended fix.** Add a description to the resource-level `siteId` input ("Overrides the
provider-level site for this resource; changing it replaces the resource.") and mark it
`replaceOnChanges` (folds into H2).

### Low

#### L1. Duplicate keyword and empty/placeholder types
`schema.json` `keywords` / `pyproject.toml` `keywords` contain `unifi` twice (`packageName` is also
`"unifi"`, added alongside the literal `"unifi"` in `schema.go:48-52`). Many generated `types` are
empty objects (`ACLRuleAction`, `State`, enum-shell types with `properties: []`), from type-less spec
schemas.

**Recommended fix.** Drop the duplicate keyword. Empty types are a spec-quality artifact; consider
pruning unreferenced empty types in the gen layer to reduce SDK noise (low priority).

#### L2. `HotspotVoucherDetails.code` (a credential) is not marked secret
`schema.json` type `unifi:sites/v1:HotspotVoucherDetails.code` (`secret: None`). A voucher `code` is a
guest-access credential. WiFi `securityConfiguration` does *not* expose a passphrase in this spec, so
there is nothing to mark there (good).

**Recommended fix.** Consider marking voucher `code` (and any future passphrase fields) `secret` in
the gen layer so they're redacted in state/CLI output.

#### L3. `allowInsecure` loses 429 retry (documented), and `injectInsecureTransport` duplicates framework transport tuning
`provider/pkg/provider/provider.go:291-305`. Already documented in code + CLAUDE.md; the hardcoded
transport will silently drift from the framework's if upstream changes its dialer settings.

**Recommended fix.** Track an upstream PR for an exported transport setter; until then the duplication
is acceptable and well-commented.

---

## What's done well (do not regress these)

- **Provider `apiKey` secrecy is correct end-to-end.** `Secret: true` in both Config and
  InputProperties (`schema.go:64,93`), and the SDK wraps it with `pulumi.Output.secret`
  (`provider.py:168`). `OnConfigure` returns `AcceptSecrets: true`.
- **Env-var defaults are wired on all four config keys** via `DefaultInfo.Environment`
  (`UNIFI_APIKEY`/`UNIFI_API_HOST`/`UNIFI_SITEID`/`UNIFI_ALLOW_INSECURE`), and the SDK honors them
  (`get_env`/`get_env_bool`). `firstEnv` mirrors the schema's declared env list rather than hardcoding.
- **`apiHost` validation** (`provider.go:127`) rejects scheme/path, pre-empting the framework's raw
  `baseURL.Host = apiHost` corruption — exactly the right defensive check, read the same way the
  framework reads it.
- **`allowInsecure` is opt-in, off by default, documented, and unit-tested**; the transport mirrors
  the framework's so only TLS verification changes.
- **Data-source result shapes are usable.** Function `outputs` use a bare `$ref`, but Pulumi flattens
  the referenced type's fields onto the `…Result` class (id/name/etc. are real typed getters) — not an
  empty result as a first glance suggests.
- **Import/`get` works structurally.** Generated resources have the standard `opts.id` get path and a
  `.get()` static method; `pulumi import`/refresh wiring exists (its *correctness* for discriminated
  variants is the C1 caveat, not a missing-feature issue).
- **Pagination auto-aggregation** (`OnPostInvoke`/`aggregatePages`) is a real correctness fix for a
  bug that would otherwise decode cleanly, and it's factored to be unit-testable.
- **Determinism discipline** (`ensureSchemaTitles`, sorted iteration in `coalesceDiscriminatedCRUD`,
  blanked schema version, `TestPipelineDeterministic`) is well thought through.
- **CRUD-map hygiene tests** (`TestCRUDMapKeysAreLiveTokens`, `TestResourceCRUDBindsItemLevel`,
  `TestDiscriminatedResourcesHaveFullCRUD`) guard the most fragile gen step.
- **`csharpNamespaces` is passed to pulschema** (`schema.go:126`) even though only Python is generated
  today — harmless and correct for when other SDKs are enabled (it is consumed by pulschema, not dead).

---

## Spec-vs-project attribution summary

| Finding | Upstream spec / pulschema | This project's choice |
|---|---|---|
| C1 variant CRUD leakage | root cause | ships without `const` discriminator / docs |
| C2 Voucher/AdoptDevice as resources | API shape | no exclusion/modeling |
| H2 no replaceOnChanges | pulschema doesn't infer | no editorial pass |
| H3 no descriptions | sparse spec | no backfill |
| H4 bad tokens | pulschema naming | no rename map |
| H5 redundant getters | pulschema variant split | no getter prune |
| M1 PluginDownloadURL doc | — | doc/release |
| M2 version wiring | — | dead flag / blank version |
| M3 description drift | — | duplicated source |
| M5 siteId modeling | — | undocumented/no replace |
</content>
</invoke>
