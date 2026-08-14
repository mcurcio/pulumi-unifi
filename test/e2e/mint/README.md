# One-time seed bake — mint an X-API-Key, snapshot the volume

This directory drives the one-off (or version-bump) **seed bake**: boot a blank
heavy UniFi OS Server, mint an X-API-Key by hand, and snapshot its whole state
volume into the committed seed. **Read [../README.md](../README.md) first** for the
overall Tier-2 architecture.

## The problem (why this is needed)

The Integration API (`/proxy/network/integration/v1/...`, `X-API-KEY`) is
**architecturally coupled to UniFi OS** — only the full UniFi OS Server image
serves it (the standalone Network app returns `403 api.forbidden` and never opens
the integration endpoint; see [../README.md](../README.md) "Why the heavy image").
So the live tier already runs the heavy image. The remaining awkward bit is that
**minting a key has no headless flow**: Ubiquiti gates key creation behind the
UniFi OS first-run wizard + the Integrations UI. You have to do it by hand, once.

## The solution (mint once, reuse forever)

The full controller stores the `api_key` **secret in Postgres**, and UniFi OS keeps
**all** persistent state in a single volume (`/data` is a symlink to `/unifi/data`,
so Mongo at `/unifi/db`, Postgres at `/unifi/data/postgresql`, and unifi-core +
Network app data are all under `/unifi`). So:

1. **Mint once** on a blank heavy stack (this directory's compose), letting the real
   controller generate the key and store its own (correctly-hashed) record.
2. **Snapshot the whole `uos_data` volume** into `../unifi-seed.tgz` — a plain
   `tar czf` of `/unifi`. Because that captures **Postgres (the key secret)**, the
   key reuses forever: no hash to recompute, no re-mint per run.
3. **Restore + run** on the live tier ([../docker-compose.yml](../docker-compose.yml)),
   which is the **same heavy image pinned by the same digest**, so the volume
   restores verbatim and the key validates on boot.

> **Why not a `mongodump`?** The earlier (abandoned) design tried to capture just
> the Network DB via `mongodump` and transplant it. That can **never** work here:
> the `api_key` secret lives in **Postgres**, not Mongo, so a Mongo-only dump omits
> exactly the thing that validates the key. The whole-volume tar carries both DBs.

## Provenance / privilege caveat

`hieutq/unifi-os-server` is an **unofficial community repackage** of Ubiquiti's
UniFi OS Server installer (bundles UniFi Network 10.4.57 in UniFi OS 5.1.15),
**pinned by digest** in both compose files. It boots full systemd and therefore
needs `cgroup: host` + a capability set + a `/sys/fs/cgroup` mount (see
[docker-compose.yml](docker-compose.yml)). That privilege surface is why the whole
Tier-2 tier is offline/local. It may not boot on every host (notably macOS Docker
Desktop's cgroup handling is finicky); if it won't boot for you, run this step on a
Linux host (or mint on any real UniFi OS console with a matching local admin).

## Runbook

The fully scripted flow is `make e2e-bootstrap` (which runs `../bootstrap.sh`); it
drives every step below and pauses for the manual wizard + mint. The manual
equivalent, using this directory's **mint** compose (OFFSET ports 11444/8444 so it
never collides with a live `make test-e2e` run):

```bash
# 1. Bring up the heavy stack BLANK (multi-GB pull; ~minutes to boot via systemd).
docker compose -p pulumi-unifi-e2e-mint -f test/e2e/mint/docker-compose.yml up -d
#    Wait until the edge serves: returns 200/401/403
until curl -ksf -o /dev/null -w '%{http_code}\n' https://localhost:11444/proxy/network/integration/v1/sites \
      | grep -qE '200|401|403'; do sleep 5; done

# 2. In a browser: https://localhost:11444
#    - Complete the UniFi OS first-run wizard. Create a LOCAL admin:
#        username: test   password: test   email: test@example.com
#    - Skip cloud login; disable auto-update.
#    - Open the Network app → Settings → Integrations (or Control Plane →
#      Integrations) → Create API Key. COPY the plaintext key (shown once).

# 3. Gracefully stop the container so Postgres + Mongo flush a consistent snapshot.
docker stop -t 90 "$(docker compose -p pulumi-unifi-e2e-mint \
  -f test/e2e/mint/docker-compose.yml ps -q uos)"

# 4. Snapshot the WHOLE uos_data volume into the committed seed (prune logs first).
docker run --rm \
  -v pulumi-unifi-e2e-mint_uos_data:/unifi \
  -v "$PWD/test/e2e":/out alpine:3 \
  sh -c "rm -rf /unifi/logs/* 2>/dev/null; find /unifi -name '*.log' -delete 2>/dev/null; \
         cd /unifi && tar -czpf /out/unifi-seed.tgz ./"

# 5. Record the plaintext key + versions for the seed.
cat > test/e2e/seed.env <<EOF
UNIFI_APIKEY=<paste-the-plaintext-key-here>
NETWORK_VERSION=10.4.57
UNIFI_OS_VERSION=5.1.15
EOF

# 6. Self-validate: restore into the LIVE tier and confirm the key works.
make test-e2e        # restores the seed, boots the live heavy stack, and the
                     # DnsARecord round-trip authenticates with the seeded key.

# 7. Tear the mint stack down — you're done with it (likely forever).
docker compose -p pulumi-unifi-e2e-mint -f test/e2e/mint/docker-compose.yml down -v
```

Commit `test/e2e/unifi-seed.tgz` (git-lfs) and `test/e2e/seed.env`. From now on,
`make test-e2e` reproduces the live controller from that seed.

## Re-minting (rare)

You only need to repeat this on a controller **version bump** that changes the
`api_key` schema or hash format — symptom: `make test-e2e` starts failing auth
after a bump even though the seed restored cleanly. To re-pin and re-mint:

1. bump the `hieutq/unifi-os-server@sha256:…` **digest in lockstep** in both
   [docker-compose.yml](docker-compose.yml) and
   [../docker-compose.yml](../docker-compose.yml);
2. bump `openapi/pin.env` to the matching spec version;
3. re-run this runbook (or `make e2e-bootstrap`) to re-mint + re-snapshot;
4. re-commit the seed.
