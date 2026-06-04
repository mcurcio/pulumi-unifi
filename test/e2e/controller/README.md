# Pinned UniFi Network Application controller (e2e Tier-2)

A thin, **version-locked** UniFi Network Application image for the Tier-2 e2e
harness — the same idea as `linuxserver/unifi-network-application`, but pinned to
the **exact** spec version the provider is generated against (`openapi/pin.env`),
so the live round-trip runs on the controller the schema actually describes. No
version gap.

## What it does

- Base: `eclipse-temurin:25-jre-jammy`. **Java 25 is required** — 10.4.57's
  launcher (`com.ubnt.ace.Launcher`) is class file version **69.0**; Java 17/21
  throw `UnsupportedClassVersionError`. The `.deb`'s own `Depends:` confirms it
  (`temurin-25-jre | openjdk-25-jre-headless | ...`).
- Installs the official `.deb` via `dpkg-deb -x` (plain extract — no systemd, no
  maintainer scripts) to `/usr/lib/unifi` (data dir `/usr/lib/unifi/data`).
- Runtime deps added by hand (the bare extract pulls no apt deps):
  `ca-certificates`, `fontconfig`, `libfreetype6` (Java 2D), `tzdata`,
  `binutils`, `curl`.
- `entrypoint.sh` writes `data/system.properties` from `MONGO_*` env to point at
  the **external** `mongo` service (`db.mongo.local=false`), then launches
  `ace.jar start` with the `--add-opens` flags the `.deb`'s init script
  specifies. Those flags are **required** on a modern JDK: without
  `--add-opens java.base/java.time=ALL-UNNAMED` (etc.) Spring Data's reflective
  access is blocked and the web context fails to initialize.
- Runs as **root** (throwaway test controller) and serves HTTPS on **8443**.

## Pin (trust-on-first-use, cf. `openapi/fetch.sh`)

The `.deb` is downloaded at build time and **checksum-verified** against a baked
sha256 (`sha256sum -c`), so the build fails if the upstream artifact ever changes
under the same version path.

| | |
|---|---|
| Version | `10.4.57` (`ARG UNIFI_VERSION`) |
| Artifact | `https://dl.ui.com/unifi/10.4.57/unifi_sysvinit_all.deb` |
| sha256 | `fc378cf8cd2bec3d334bf7b72eabfcd1861e5fae67b9c16735471132105b2072` |

## Re-pinning on a version bump

Keep the controller in lockstep with `openapi/pin.env`:

1. Set `UNIFI_VERSION` in `Dockerfile` to the new version.
2. Download the new `.deb`, `sha256sum` it.
3. Set `UNIFI_SHA256` (and the value in the table above + `../README.md`) to that.
4. Re-bake the seed: `make e2e-bootstrap`.

## Build / run

Compose builds this automatically (`build: { context: ./controller }` in
`../docker-compose.yml`). Standalone:

```sh
docker build -t unifi-e2e-controller:10.4.57 .
```
