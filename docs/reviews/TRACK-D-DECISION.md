# Track-D decision brief — modeling discriminated entities

**Status:** DECIDED (2026-06-03) → **per-variant + polish, shape (b)**. Rationale: cleanest developer
authoring UX (you pick the exact variant; no discriminator field; only relevant fields), which is the
primary target. HARD CONSTRAINT: the shape must be produced by deterministic codegen passes — any
hand-written surface (naming tables, irregular plurals, immutable-field lists) must be minimal,
declarative, and in one place (like `excludedPaths`), never per-resource code. Tagged-union (c) is NOT
pursued. The U1 *CRUD-binding* upstream fix (which keeps shape (b) and deletes `coalesceDiscriminatedCRUD`)
remains an optional later cleanup. First public release still gates on confirming the shipped shape.
**Audience:** maintainer review. Reference shapes are illustrative Python (the generated SDK language).

---

## 1. What we're actually talking about

Several UniFi entities are **polymorphic**: one REST endpoint accepts several differently-shaped
bodies, distinguished by a discriminator field. In OpenAPI this is `oneOf` + `discriminator`.

Concrete example — **DNS records**. There is exactly one endpoint:

```
POST   /v1/sites/{siteId}/dns/policies            (create any DNS record)
GET    /v1/sites/{siteId}/dns/policies/{dnsPolicyId}   (read one)
PUT/PATCH/DELETE  …/{dnsPolicyId}
```

The request body is a `oneOf` of 7 variants keyed by `type`:

| `type` value | variant fields (besides `enabled`, `domain`) |
|---|---|
| `A_RECORD` | `ipv4Address`, `ttlSeconds` |
| `AAAA_RECORD` | `ipv6Address`, `ttlSeconds` |
| `CNAME_RECORD` | target host, `ttlSeconds` |
| `MX_RECORD` | mail host, `priority` |
| `SRV_RECORD` | priority/weight/port/target |
| `TXT_RECORD` | text value |
| `FORWARD_DOMAIN` | upstream resolver |

Same construct elsewhere:
- **WiFi broadcast** → `Standard` / `IotOptimized` (endpoint `/wifi/broadcasts`).
- **Managed network** → discriminator `management` ∈ `UNMANAGED / GATEWAY / SWITCH` (endpoint `/networks`).
- **Traffic-matching** → `Mac` / `Ipv4` / `Ipv4Addresses` / `Ipv6Addresses` / `Ports`.
- **ACL rules** → `Mac` / `Ipv4` variants.

This is why **9 writable endpoints fan out to 21 Pulumi resources**.

---

## 2. What the pipeline produces today (the problem)

pulschema splits each `oneOf` into **one resource per variant**, but the split is leaky. Verified
against the current `schema.json` / `metadata.json` on `execute/foundation`:

- **7 separate resource tokens** for DNS: `ARecord`, `AaaaRecord`, `CnameRecord`, `MxRecord`,
  `SrvRecord`, `TxtRecord`, `ForwardDomain`.
- **All 7 bind the identical endpoints** — `c: /dns/policies`, `r/d/p: /dns/policies/{dnsPolicyId}`.
- **The discriminator survived the split** as a *required, free-form string*:
  `ARecord.requiredInputs = ["enabled", "type"]`, `type = {"type":"string"}` — no `enum`, no `default`.
- Variant tokens for other entities lose all context: bare `Standard`, `Mac`, `Ports`, `Gateway`,
  `Ipv4` vs `Ipv4Addresses`.

Three consequences:

1. **Redundant + un-discoverable discriminator.** You pick `ARecord`, then must *also* type
   `type="A_RECORD"` — a magic string with no enum/IDE hint, validated only at apply time.
2. **Context-free / collision-prone tokens.** `unifi.sites.v1.Standard` gives no clue it's a WiFi
   network; `Mac`/`Ports` are meaningless; variant names are reused across entities (future collision).
3. **Refresh/import can't disambiguate.** All 7 DNS resources read the same `/{dnsPolicyId}` endpoint.
   On `pulumi refresh`/`import` nothing ties an id to a variant — if the stored record is `AAAA` but
   the program declares `ARecord`, the read mis-maps fields or diffs forever. `pulumi import` can't
   know which of 7 resource types to import an id as.

---

## 3. Reference shapes

### (a) TODAY — raw per-variant (what ships if we do nothing)

```python
import pulumi_unifi as unifi

rec = unifi.sites.v1.ARecord("host-a",
    enabled=True,
    type="A_RECORD",            # required, free string, no hint it must be exactly this
    domain="host.example.com",
    ipv4_address="10.0.0.5",
    ttl_seconds=300,
)

wifi = unifi.sites.v1.Standard("office-ssid",   # "Standard" what? (it's a WiFi network)
    type="...",                 # required magic string again
    # ~28 fields, ~18 shared with IotOptimized
)
```

### (b) Per-variant **after M2.1 + M2.2 polish** (Options "Hybrid" / "Per-variant only")

- **M2.1** pins `type` to a `const`/`default` and drops it from required → the provider injects it.
- **M2.2** entity-prefixes the tokens → context restored.

```python
rec = unifi.sites.v1.DnsARecord("host-a",   # token carries the entity
    enabled=True,
    domain="host.example.com",
    ipv4_address="10.0.0.5",
    ttl_seconds=300,
)                               # no `type` — injected automatically

wifi = unifi.sites.v1.WifiBroadcastStandard("office-ssid", ...)
net  = unifi.sites.v1.ManagedNetworkGateway("corp-net", ...)
```

Better, and 100% in-pipeline now. **Still ships 7 DNS resource types** sharing one read endpoint, so
the refresh/import disambiguation problem (§2.3) remains — improved on the create side (const `type`),
not solved on the read/import side.

### (c) Tagged-union — one resource per entity (Option "Tagged-union first", needs upstream G-U1)

The idiomatic Pulumi modeling of a discriminated REST entity. Two sub-shapes:

**(c1) single resource + enum discriminator + variant fields optional:**

```python
rec = unifi.sites.v1.DnsRecord("host-a",
    type=unifi.sites.v1.DnsRecordType.A_RECORD,   # enum: IDE-completable, plan-time validated
    enabled=True,
    domain="host.example.com",
    ipv4_address="10.0.0.5",                       # field valid for this type
)
```

**(c2) nested per-variant block (set exactly one):**

```python
rec = unifi.sites.v1.DnsRecord("host-a",
    enabled=True,
    a_record=unifi.sites.v1.ARecordArgs(domain="host.example.com", ipv4_address="10.0.0.5", ttl_seconds=300),
)
```

**One** `DnsRecord` type, **one** read endpoint that maps back cleanly (the API returns `type`, which
selects the variant shape) → refresh/import disambiguate correctly, shared fields defined once. Cost:
pulschema does not do this today; it requires the upstream **G-U1** change (slow, external), and it's a
different SDK shape than (a)/(b).

---

## 4. The design decisions (trade-offs)

| Axis | Per-variant (b) | Tagged-union (c) |
|---|---|---|
| **# resource tokens** | 21 (7 DNS + 2 WiFi + 3 net + traffic/acl + flat) | ~9 (one per writable entity) |
| **Discriminator UX** | gone (M2.1 injects const) ✅ | enum input / variant block ✅ |
| **Token discoverability** | entity-prefixed, ok ✅ | clean, idiomatic ✅✅ |
| **refresh / import correctness** | shared read endpoint can't disambiguate variant ❌ | type from API selects shape ✅ |
| **Shared-field duplication** | duplicated across N variant resources ❌ | defined once ✅ |
| **Effort / dependency** | all in-pipeline now, we control it ✅ | upstream pulschema PR (G-U1), external/slow ❌ |
| **Time to a usable provider** | days ✅ | weeks–months (gated on upstream) ❌ |
| **Breaking-change risk** | token names are public API; switching later breaks consumers | the stable end-state |

**Key irreversibility:** resource token names are the public API. Shipping (b) publicly and later
moving to (c) is a **breaking change** for every consumer. So the model must be settled before the
first *public* (PyPI/registry) publish — not necessarily before internal/`iac` consumption off a pinned
git ref.

---

## 5. Recommendation — Hybrid

1. **Now:** run Track D as per-variant **with M2.1 (const discriminator) + M2.2 (entity-prefixed
   tokens)** and the rest of the polish passes (descriptions, de-page, enum, replaceOnChanges). This
   yields a clean, usable provider in days, unblocking internal use / `iac` off a pinned ref.
2. **In parallel:** open **G-U1** (pulschema full discriminated-CRUD + tagged-union emission) with
   shape (c1) as the target.
3. **Gate the first PUBLIC release** on which model is committed. If G-U1 lands in time, publish (c);
   if not, decide whether to publish (b) and accept a later breaking bump, or hold the public release.

**Risk to manage:** do not publicly publish (b) and then silently break it. Keep early consumers pinned
to a git ref / `0.x` pre-release until the public model is locked, and document the intent.

This captures most of the UX win immediately while keeping the idiomatic end-state reachable and the
public API uncommitted until you've seen (c) working.

---

## 6. What each choice means for the plan

- **Hybrid / Per-variant only** → unblocks Track D passes `D-M2.1`…`D-M3.3` immediately (each a
  `pass_*.go` + golden rebase). "Per-variant only" simply drops the G-U1 pursuit.
- **Tagged-union first** → Track D waits on G-U1; only the non-discriminator passes (descriptions,
  de-page, enum, replaceOnChanges) can proceed in-pipeline meanwhile.
