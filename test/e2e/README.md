# Tier-2 e2e — real UniFi OS controller (heavy stack + seed-restore)

End-to-end verification against a **real, running UniFi OS controller** serving the
official Integration API. This is the only tier that proves real **X-API-Key
auth**, real decode of live data, and a **true create→read→update→delete
round-trip** with persisted state — things the stateless Prism mock (Tier-1)
structurally cannot.

| | Tier-1 (`make test-mock`) | Tier-2 (`make test-e2e`) |
|---|---|---|
| What it is | Prism mock of `openapi_generated.yml` behind a Caddy TLS front | real UniFi OS Server controller, restored from a committed seed |
| Proves | the API **shape** (paths, schemas, CRUD dispatch) — unauthenticated | real **auth**, real JSON decode, real persisted **CRUD round-trip** |
| Privilege | none — portable, runs anywhere (incl. CI) | systemd + caps + cgroup — **offline / local only** |

The hard part — the UniFi OS first-run wizard + API-key minting, which has no
headless flow — is solved **once** by hand and frozen into a committed **seed**.
Thereafter every run is deterministic: restore the seed, boot, test.

## Why the heavy image (the crux — get this right)

The official UniFi **Integration API** (`/proxy/network/integration/v1/...`,
`X-API-KEY` header auth) is **architecturally coupled to UniFi OS**. It **cannot**
be served by the standalone `unifi-network-application`. This was proven
empirically on Network 10.4.57:

- On a real UniFi OS controller, the Network app serves the Integration API on a
  **dedicated internal port `:8081`**, and authorization is **delegated to
  `unifi-core`** (a Node service backed by Postgres). unifi-core validates the
  `X-API-KEY` against Postgres, then an nginx `auth_request` injects **trusted
  headers** (`X-UserAccessMask`, `X-UserPermissionMask`, `X-ApiKeyId`) and proxies
  to the Network app, which trusts those headers.
- The **standalone Network app does NOT open `:8081`** and returns a hard
  **`403 api.forbidden`** on `/integration` for **every** request — including with
  the exact captured trusted headers. This is **mode-refusal**, NOT "the surface is
  present, just needs a key."

  > **Correcting the record.** Earlier docs interpreted that `403` as "the
  > integration surface is routed and merely auth-gated — supply a key and it
  > works." **That is false.** The standalone app never serves the integration
  > endpoint at all; no key, header set, or proxy shim makes it. The lean
  > Network-app approach (and the `controller/` image, the Caddy prefix-rewrite,
  > the `mongodump`-transplant) cannot work and have been removed.

- Therefore the **only** image that can authenticate the Integration API is the
  **full UniFi OS Server** image — here `hieutq/unifi-os-server`, an unofficial
  community repackage bundling UniFi Network 10.4.57 in UniFi OS 5.1.15. It carries
  **both halves**: the Network app *and* unifi-core + Postgres + the nginx
  auth_request edge.

### Why offline, not CI

The full UniFi OS image runs **systemd as PID 1** with ~10 services, so it needs
`cgroup: host` + a capability set + a `/sys/fs/cgroup` mount (see
[docker-compose.yml](docker-compose.yml)). A CI runner *can* technically grant
those in a `run:` step, but this is deliberately treated as a **local / offline
tier**. Tier-1 (`make test`, `make test-mock`, the Prism mock) remains the
portable, zero-privilege gate, and it already validates the full API **shape**
unauthenticated.

## The single-volume seed model

The heavy controller keeps **all** persistent state in **one** Docker volume,
because `/data` is a symlink to `/unifi/data`:

- **Mongo** → `/unifi/db`
- **Postgres** → `/unifi/data/postgresql` — **this is where the `api_key` secret
  lives**
- **unifi-core** config + Network app data → also under `/unifi`

So the committed seed (`unifi-seed.tgz`) is simply a `tar czf` of the whole
`/unifi` volume (~1 GB raw, ~167 MB gzipped). **This is why a UniFi-OS-minted key
reuses forever:** the seed carries Postgres — i.e. the secret itself. There is no
hash to recompute and no re-mint on each run.

> The old lean design tried to transplant a `mongodump` to the standalone app.
> That can **never** work: the key secret is in **Postgres**, not Mongo, *and* the
> standalone app refuses the endpoint anyway.

`restore.sh` is the inverse: recreate the volume and untar the snapshot into it.
Then `docker compose up` boots UniFi OS against pre-seeded state, and the minted
key validates immediately — no wizard, no re-mint.

## Prerequisites

- **Docker** (Compose v2) able to run **privileged / capability** containers —
  i.e. a normal local Docker daemon, **not** a constrained CI service container.
  The heavy stack needs `cgroup: host`, a cap set, and a `/sys/fs/cgroup` mount.
- **git-lfs** — the seed (`unifi-seed.tgz`, ~167 MB) is an LFS object. One-time per
  clone:
  ```sh
  git lfs install
  git lfs pull        # fetch the actual seed bytes — a checkout gets only a pointer
  ```
  A *tiny* `unifi-seed.tgz` (a few hundred bytes) means an un-fetched LFS pointer;
  `restore.sh` detects and rejects that.

## Repeatable run — one executable entrypoint

The whole pipeline is a single executable, **[`run.sh`](run.sh)**. `make test-e2e`
just builds the provider and calls it; you can also run it directly (it self-builds
the generated artifacts if they're missing):

```sh
make test-e2e                 # build the provider, then run the pipeline
# or, directly:
./test/e2e/run.sh             # the pipeline on its own
./test/e2e/run.sh --keep-up   # leave the controller up afterwards (debugging)
./test/e2e/run.sh -- -v       # forward extra args (after --) to `go test`
```

The deterministic path; no manual steps. `run.sh`:

1. **Restores** `unifi-seed.tgz` into a fresh `uos_data` volume (`restore.sh`).
2. `docker compose up -d` — boots the heavy UniFi OS Server
   ([docker-compose.yml](docker-compose.yml)) against the pre-seeded volume.
3. **Readiness gate on AUTHENTICATED JSON.** It polls the Integration API **with**
   the key and waits for a body that starts with `{` (real JSON). This matters:
   during boot the edge nginx returns **`200` with an HTML loading page** well
   before unifi-core + the Network app are ready, so a naive "any 200 = ready" gate
   fails with `invalid character '<'`. Heavy boot is ~1–4 min; the gate loops
   generously and fails loud on timeout.
4. Runs the e2e-tagged Go tests with `UNIFI_E2E_ADDR=127.0.0.1:11443` and
   `UNIFI_APIKEY` sourced from `seed.env`.
5. Tears the stack down via `trap … EXIT` (even if bring-up fails, unless
   `--keep-up`): `down -v` + volume removal.

The provider's generated server URL is
`https://<host>/proxy/network/integration/v1`, and the heavy edge serves the
Integration API at **exactly** that path on `:11443`. So pointing the provider at
`:11443` matches the real path verbatim — **no Caddy / prefix-rewrite is needed**
(unlike the Tier-1 mock).

### What the tests cover (`provider/pkg/provider/e2e_test.go`, `//go:build e2e`)

- **TestE2ELiveRead** — invokes the `getCountry` data source and asserts a
  non-empty decoded result. `getCountry` has no path params, so it isolates the
  fundamentals: the minted key authorizes, the URL composes, TLS connects, JSON
  decodes.
- **TestE2ECRUDRoundTrip** — a `DnsARecord` (a flat discriminated variant, no
  foreign keys, so it provisions on a bare site):
  Create → Read → Diff (DIFF_NONE, the no-op second-`up` invariant) → Update →
  Read → Delete → Read (gone). Two live-API specifics it handles:
  - it supplies the `type` discriminator (`A_RECORD`) **and** `ttlSeconds` — the
    real controller rejects the create without them (`400 "ttlSeconds must not be
    null"`);
  - it **discovers the real site UUID at runtime** via the `listSiteOverviews` data
    source, because the live API rejects the `"default"` siteId alias on
    siteId-scoped paths (`"'default' is not a valid 'siteId' value"`) — it requires
    the UUID.

The tests are env-gated: without `UNIFI_E2E_ADDR` they **skip**, so a bare
`go test -tags e2e ./...` (the compile check) needs no controller.

## TLS

UniFi OS serves its **own self-signed cert** on the edge. The tests trust it via
the provider's `allowInsecure=true` flag (the same approach as Tier-1) to keep the
focus on the live CRUD round-trip. No Caddy, no generated certs.

## The one-time bake

Producing the seed is a one-off (and again only on a version bump). Because the
wizard + key mint have no headless flow, it's mostly manual; `bootstrap.sh`
(`make e2e-bootstrap`) drives the scriptable parts and pauses for the manual ones.
The detailed runbook is **[mint/README.md](mint/README.md)**. In summary
`bootstrap.sh`:

1. boots the heavy stack **BLANK** on a fresh volume using the **mint** compose
   ([mint/docker-compose.yml](mint/docker-compose.yml), OFFSET ports 11444/8444 so
   it never collides with a live `make test-e2e` run);
2. readiness-gates the edge (any of 200/401/403 = up), then **PAUSES**: you open
   `https://localhost:11444`, complete the UniFi OS first-run wizard (LOCAL admin
   `test`/`test`/`test@example.com`, skip cloud, disable auto-update), then Network
   app → Settings → Integrations → **Create API Key** and copy the plaintext (shown
   once), and paste it back;
3. **gracefully stops** the container (`docker stop -t 90`) so Postgres + Mongo
   flush a consistent snapshot;
4. **snapshots** the whole `uos_data` volume into `test/e2e/unifi-seed.tgz`
   (pruning logs first to shrink it);
5. writes `test/e2e/seed.env` (`UNIFI_APIKEY` + `NETWORK_VERSION` +
   `UNIFI_OS_VERSION`);
6. tells you to **self-validate** with `make test-e2e`, then tears the mint stack
   down.

You end up with two committed artifacts:

| File | What | Tracked via |
|------|------|-------------|
| `test/e2e/unifi-seed.tgz` | full `uos_data` volume tar (Mongo + Postgres + configs) | **git-lfs** |
| `test/e2e/seed.env` | `UNIFI_APIKEY` + `NETWORK_VERSION` + `UNIFI_OS_VERSION` | plain git |

> **These seed credentials are throwaway test-controller values** baked into a
> local, disposable fixture — **not** real secrets. Committing them is explicitly
> OK. (Still: never bake a real controller's key.)

Review both, then commit:

```sh
git add test/e2e/unifi-seed.tgz test/e2e/seed.env   # tgz goes through git-lfs
git commit
```

## Re-baking on a version bump

When the controller version moves, re-pin the image **by digest in lockstep** in
**both** compose files and bump the spec pin, then re-bake:

1. update the `hieutq/unifi-os-server@sha256:…` digest in
   [docker-compose.yml](docker-compose.yml) **and**
   [mint/docker-compose.yml](mint/docker-compose.yml) (keep them identical);
2. bump `openapi/pin.env` to the matching spec version;
3. re-run `bootstrap.sh` (`make e2e-bootstrap`) to re-mint + re-snapshot;
4. `make test-e2e`, then commit the new `unifi-seed.tgz` + `seed.env`.

The symptom that *forces* a re-mint is `make test-e2e` **failing auth after a
version bump** even though the seed restored cleanly — the `api_key` schema/hash
format changed under the new controller.
