# Tier-2 e2e — real UniFi controller (lean stack + seed-restore)

End-to-end verification against a **real, running UniFi Network controller**
serving the official Integration API. This is the only tier that proves real
auth, real decode of live data, and a **true create→read→update→delete
round-trip** with persisted state — things the stateless Prism mock (Tier-1)
structurally cannot.

The hard part — first-run wizard + API-key minting, which has no headless flow —
is solved **once** by hand and frozen into a committed **seed**. Thereafter every
run is deterministic: restore the seed, boot, test.

## The lean stack (no privileged / caps / cgroup)

Three **plain** containers, none of which needs `privileged`, `cap_add`,
`cgroup: host`, `tmpfs`, or a host cgroupfs bind — so this runs on **standard
GitHub-hosted runners**:

| Service | Image | Role |
|---------|-------|------|
| `mongo` | `mongo:7.0` | the controller's datastore |
| `unifi` | built from `./controller` (pinned **10.4.57** .deb on Temurin-25 JRE) | the Network app |
| `tls`   | `caddy:2` | TLS terminator + `/proxy/network` prefix rewrite |

**Why the plain Network app image (not "UniFi OS Server"):** the Integration API
(`/integration/v1/...`, `X-API-KEY`) is served **directly by the Network
application** on its own TLS port 8443. UniFi OS only *prepends* the external
`/proxy/network` prefix and an edge proxy — it does **not** add the API surface.
Verified: `GET /integration/v1/sites` on the bare container returns **403**
(auth-gated, needs a key), confirming the surface is present. So Caddy stands in
for the edge proxy: it terminates TLS on `:443` (host `:11443`) and
`handle_path /proxy/network/*` strips the prefix, so the provider's
`https://…:11443/proxy/network/integration/v1/…` lands on the app's
`https://unifi:8443/integration/v1/…`. This is the same pattern as the Tier-1
mock — no heavy, unlicensed full-OS image, no elevated host integration.

## Pinned controller — EXACT spec version (10.4.57)

The `unifi` service is **built from `test/e2e/controller/`** (not pulled from a
registry), so the live tier runs on the **exact** version the provider schema is
generated against — **no version gap** between controller and spec:

- Controller: `test/e2e/controller/Dockerfile`, `UNIFI_VERSION=10.4.57`.
- Spec (`openapi/pin.env`): **10.4.57**. They match by construction; `seed.env`'s
  `NETWORK_VERSION` should read `10.4.57`.

### How the image is pinned (trust-on-first-use, cf. `openapi/fetch.sh`)

The Dockerfile downloads the official Ubiquiti `.deb` at build time and
**checksum-verifies** it against a baked sha256, so the build fails if the
upstream artifact ever changes under the same version path:

- Artifact: `https://dl.ui.com/unifi/10.4.57/unifi_sysvinit_all.deb`
- Pinned sha256: `fc378cf8cd2bec3d334bf7b72eabfcd1861e5fae67b9c16735471132105b2072`

The `.deb` is installed via `dpkg-deb -x` (a plain extract — no systemd, no
maintainer scripts) onto a **Temurin 25 JRE** base: 10.4.57's launcher is Java
class file version 69.0 (Java 25), so Java 17/21 throw `UnsupportedClassVersion`.
`entrypoint.sh` writes `/usr/lib/unifi/data/system.properties` from the `MONGO_*`
env (same contract the linuxserver image used, so the compose env is unchanged)
to point at the external `mongo` service, then launches `ace.jar` with the
`--add-opens` flags the `.deb`'s own init script specifies (required on a modern
JDK or the web context fails to initialize). See `controller/README.md`.

### Re-pinning on a spec bump

When `openapi/pin.env` moves to a new version, bump the controller in lockstep:
edit `UNIFI_VERSION` in `controller/Dockerfile`, download the new `.deb`,
`sha256sum` it, set `UNIFI_SHA256` to that value, update the sha256 here, and
re-bake (`make e2e-bootstrap`).

## Prerequisites

- **Docker** (Compose v2). **No** privileged mode, capabilities, or cgroup host
  namespace required — unlike the old heavy harness, this runs on stock runners.
- **git-lfs** — the seed (`unifi-seed.tgz`) is an LFS object. One-time per clone:
  ```sh
  git lfs install
  git lfs pull        # fetch the actual seed bytes (a checkout gets only the pointer)
  ```
- For **baking** only: a browser to reach `https://localhost:8443` for the wizard.

## One-time bake — `make e2e-bootstrap`

Run this **once** (and again only on an image/spec bump — see Re-baking). It
generates the Caddy cert (`gen-certs.sh`) and drives `test/e2e/bootstrap.sh`:

1. Boots a **blank** lean stack and waits for the Integration API (through Caddy)
   to answer (the blank app returns 403 — up, but unauthenticated).
2. **Pauses** and prints instructions. In the browser at `https://localhost:8443`
   (note: the app's **direct** port, not Caddy):
   - complete the first-run wizard, creating a **LOCAL admin** (skip the Ubiquiti
     cloud / SSO login — the baked creds are fine, it's a throwaway controller);
   - **disable** auto-update, auto-optimize, and remote access (determinism);
   - Network app → **Settings → Integrations** → create an **API key** and copy it.
3. Paste the key back at the prompt. The script verifies it authorizes (expects
   200 through Caddy), then writes it + the detected Network version to
   **`test/e2e/seed.env`**.
4. Stops the Network app (clean DB) and snapshots the seed **compactly**:
   `mongodump --archive --gzip` of the app DBs + a `tar` of the data dir
   (`/usr/lib/unifi/data`), combined into **`test/e2e/unifi-seed.tgz`**.
5. **Self-validates**: restores the fresh seed into throwaway volumes, boots, and
   curls the Integration API **with the minted key expecting 200** — failing
   loudly if the restored seed doesn't serve.

You end up with two committed artifacts:

| File | What | Tracked via |
|------|------|-------------|
| `test/e2e/unifi-seed.tgz` | `mongodump` archive + data-dir (`/usr/lib/unifi/data`) tar | **git-lfs** |
| `test/e2e/seed.env` | `UNIFI_APIKEY` + `NETWORK_VERSION` | plain git |

> **Snapshot promptly.** Bake immediately after the wizard + key. The longer the
> controller runs, the more stats/logs it accumulates — all seed bloat.

Review both, then commit. (This task leaves them uncommitted.)

### Why mongodump + data-dir tar (not a raw volume snapshot)

A `mongodump --archive --gzip` of the app DBs is **BSON, tiny** — a handful of MB
— and the data dir (`/usr/lib/unifi/data`) is mostly the generated keystore (the
entrypoint regenerates `system.properties` from env on every boot). That is far
smaller and more portable than tarring raw Mongo data files, which is the
size-minimization win the heavy harness lacked. `restore.sh` recreates the
volumes from scratch, boots a throwaway `mongod` to `mongorestore` the archive
(re-creating the `unifi` app user, which a DB-only dump doesn't carry), and
untars the data dir — then the compose stack boots against pre-seeded volumes.

## Repeatable run — `make test-e2e`

The deterministic path; no manual steps. It:

1. Generates the Caddy cert (`gen-certs.sh`).
2. Restores `unifi-seed.tgz` into fresh `mongo_data` + `unifi_data` volumes
   (`restore.sh`).
3. `docker compose build` (the pinned controller image, verifying the `.deb`
   sha256) then `docker compose up -d --wait`, and a **readiness gate** polling
   `https://127.0.0.1:11443/proxy/network/integration/v1/sites` until non-gateway
   (mirrors the `test-mock` gate); fails loud on timeout.
4. Runs the e2e-tagged Go tests with `UNIFI_E2E_ADDR=127.0.0.1:11443` and
   `UNIFI_APIKEY` sourced from `seed.env`.
5. Tears the stack down via `trap … EXIT` (even if bring-up fails), including
   `down -v` + volume removal.

```sh
make test-e2e
```

### What the tests cover (`provider/pkg/provider/e2e_test.go`, `//go:build e2e`)

- **TestE2ELiveRead** — invokes a data source (`getCountry`) and asserts a
  non-empty decoded result: the minted key authorizes, the URL composes, TLS
  connects, JSON decodes.
- **TestE2ECRUDRoundTrip** — a `DnsARecord` (the simplest discriminated variant —
  flat body, no foreign keys, so it provisions on a bare site): Create → Read →
  Diff (DIFF_NONE, the no-op second-`up` invariant) → Update → Read → Delete →
  Read (gone). Uses the gRPC lifecycle methods directly, not a full `pulumi up`.

The tests are env-gated: without `UNIFI_E2E_ADDR` they **skip**, so a bare
`go test -tags e2e ./...` (the compile check) needs no controller.

## Re-baking (spec bump)

When you bump the pinned spec (`openapi/pin.env`), bump the controller in lockstep
(`controller/Dockerfile` `UNIFI_VERSION` + `UNIFI_SHA256` — see "Re-pinning"
above) so the live tier stays on the exact spec version; the frozen seed can then
drift. Re-bake:

```sh
make e2e-bootstrap      # rebuilds the pinned image, boots blank, prompts for
                        # wizard+key, re-snapshots
# review the new seed.env NETWORK_VERSION (should be the new pinned version), then:
git add test/e2e/unifi-seed.tgz test/e2e/seed.env   # tgz goes through git-lfs
git commit
```

## CI note

`test-e2e` runs on **standard GitHub-hosted runners** — the lean stack needs no
privileged mode, capabilities, or cgroup host namespace (a change from the old
heavy harness, which required a self-hosted/privileged runner). Ensure `git-lfs`
is available so the seed object is fetched. Tier-1 (`make test`, `make
test-mock`) remains the fast portable gate; `test-e2e` is the live confirmation.

## TLS

Caddy serves a self-signed cert generated by `gen-certs.sh` (into `test/e2e/certs/`,
git-ignored). These tests trust it via the provider's `allowInsecure=true` flag
(same as Tier-1) to keep the focus on the live CRUD round-trip. The CA-pinned
secure path has separate no-Docker coverage (`securetls_test.go`).
