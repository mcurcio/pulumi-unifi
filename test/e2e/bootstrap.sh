#!/usr/bin/env bash
# bootstrap.sh — ONE-TIME (or version-bump) seed bake for the Tier-2 e2e harness.
#
# Run by `make e2e-bootstrap`. Minting an X-API-Key has NO headless flow — Ubiquiti
# gates it behind the UniFi OS first-run wizard + Integrations UI — so this script
# drives the scriptable parts and PAUSES for the two manual ones (wizard + mint).
#
#   1. Boot the heavy UniFi OS Server BLANK on a fresh volume (the mint compose, on
#      OFFSET ports so it never collides with a live `make test-e2e` run).
#   2. Readiness-gate the edge (heavy boot is multi-minute).
#   3. PAUSE: the maintainer completes the UniFi OS wizard + mints a key by hand,
#      then pastes the plaintext (shown once) back here.
#   4. Gracefully stop the container so Postgres + Mongo flush a consistent snapshot.
#   5. Snapshot the WHOLE uos_data volume into test/e2e/unifi-seed.tgz.
#   6. Write test/e2e/seed.env (key + versions).
#   7. Tell the maintainer to self-validate with `make test-e2e`.
#   8. Tear the mint stack down.
#
# The seed is a single full-volume tar because UniFi OS keeps ALL persistent state
# in one place: /data is a symlink to /unifi/data, so Mongo (/unifi/db), Postgres
# (/unifi/data/postgresql — which stores the api_key SECRET), unifi-core config, and
# the Network app data all live in the uos_data volume. Capturing the whole volume
# is why the minted key reuses forever: the seed carries Postgres (the secret) too.
# (A mongodump can NEVER work here — the key secret is in Postgres, not Mongo.)
#
# Outputs (both COMMITTED — this is a throwaway test controller, baked creds are
# acceptable):
#   test/e2e/unifi-seed.tgz   (full uos_data volume tar — git-lfs)
#   test/e2e/seed.env         (UNIFI_APIKEY + versions — plain git)
#
# This only needs re-running on a controller version bump (the api_key schema/hash
# could change); see test/e2e/README.md "Re-baking".
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MINT_COMPOSE="$HERE/mint/docker-compose.yml"
SEED_TGZ="$HERE/unifi-seed.tgz"
SEED_ENV="$HERE/seed.env"

# Versions baked into the seed env (kept in lockstep with the image digest in both
# compose files and with openapi/pin.env — see README "Re-baking").
NETWORK_VERSION="10.4.57"
UNIFI_OS_VERSION="5.1.15"

# Dedicated mint project so its volume/container names are deterministic and never
# collide with the live tier. The mint compose uses OFFSET ports (11444/8444).
MINT_PROJECT="pulumi-unifi-e2e-mint"
MINT_VOLUME="${MINT_PROJECT}_uos_data"

# The Integration API on the mint edge (offset port 11444).
MINT_API="https://localhost:11444/proxy/network/integration/v1/sites"
MINT_GUI="https://localhost:11444"

mdc() { docker compose -p "$MINT_PROJECT" -f "$MINT_COMPOSE" "$@"; }

# ============================================================================
# Step 1 — boot the heavy stack BLANK on a fresh volume.
# ============================================================================
echo "==> bringing up a BLANK heavy UniFi OS Server (multi-GB pull on first run;"
echo "    systemd boot is multi-minute)"
# Start from a clean slate so the bake never inherits stale state.
mdc down -v >/dev/null 2>&1 || true
docker volume rm "$MINT_VOLUME" >/dev/null 2>&1 || true
mdc up -d

# ============================================================================
# Step 2 — readiness-gate the edge (any of 200/401/403 = edge up + routing).
# ============================================================================
echo "==> waiting for the UniFi OS edge to come up (heavy boot ~2-5 min)..."
ready=
for i in $(seq 1 120); do
  code=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 "$MINT_API" 2>/dev/null || echo 000)
  case "$code" in
    200|401|403) echo "    edge up (HTTP $code, attempt $i)"; ready=1; break ;;
    *) sleep 5 ;;
  esac
done
if [ -z "$ready" ]; then
  echo "ERROR: the UniFi OS edge never came up on $MINT_API after the timeout." >&2
  echo "       Check 'docker compose -p $MINT_PROJECT -f $MINT_COMPOSE logs'." >&2
  echo "       (The unofficial heavy image may not boot on every host — notably" >&2
  echo "        macOS Docker Desktop's cgroup handling is finicky; see mint/README.md.)" >&2
  exit 1
fi

# ============================================================================
# Step 3 — manual wizard + key mint (the part that cannot be headless).
# ============================================================================
cat <<EOF

============================================================================
 MANUAL ONE-TIME SETUP  (do this now, in a browser)
============================================================================
 1. Open:  $MINT_GUI
      (accept the self-signed cert warning — it's UniFi OS's own cert)
 2. Complete the UniFi OS first-run wizard:
      - Create a LOCAL admin (THROWAWAY test controller, so use simple creds):
          username: test   password: test   email: test@example.com
      - SKIP the Ubiquiti cloud / SSO login (choose local-only / "advanced").
      - DISABLE auto-update / remote access — anything that mutates state or
        phones home. Determinism > UX.
 3. Network app -> Settings -> Integrations  (older builds: Control Plane ->
      Integrations) -> Create API Key. COPY it now — it is shown only once.

 Then come back here and paste the key.
============================================================================

EOF

KEY=""
printf "Paste the minted X-API-Key, then press Enter: "
read -r KEY
if [ -z "${KEY:-}" ]; then
  echo "ERROR: no key entered; aborting (leaving the stack up for retry)." >&2
  exit 1
fi

# Confirm the key authorizes BEFORE we bother snapshotting (expect real JSON).
echo "==> verifying the pasted key authorizes (expect a JSON body)"
body=$(curl -sk --max-time 5 -H "X-API-KEY: $KEY" "$MINT_API" 2>/dev/null || true)
case "$body" in
  '{'*) echo "    key authorizes (got JSON)" ;;
  *) echo "ERROR: the pasted key did not authorize (no JSON body). Re-check the" >&2
     echo "       key / wizard. Leaving the stack up for retry." >&2
     exit 1 ;;
esac

# ============================================================================
# Step 4 — graceful stop so Postgres + Mongo flush a consistent snapshot.
# ============================================================================
# Resolve the container ID robustly (don't hardcode the compose-derived name).
echo "==> gracefully stopping the controller (Postgres + Mongo flush; up to 90s)"
CID=$(mdc ps -q uos)
if [ -z "$CID" ]; then
  echo "ERROR: could not find the running mint container (service 'uos')." >&2
  exit 1
fi
docker stop -t 90 "$CID"

# ============================================================================
# Step 5 — snapshot the WHOLE uos_data volume into the seed.
# ============================================================================
echo "==> snapshotting the uos_data volume into $SEED_TGZ"
# Prune logs first to shrink the seed (regenerated on every boot), then tar the
# whole /unifi tree (perms preserved). Mount the seed dir so the output lands here.
docker run --rm \
  -v "$MINT_VOLUME":/unifi \
  -v "$HERE":/out \
  alpine:3 \
  sh -c "rm -rf /unifi/logs/* 2>/dev/null; find /unifi -name '*.log' -delete 2>/dev/null; \
         cd /unifi && tar -czpf /out/unifi-seed.tgz ./"

FINAL_SIZE=$(du -h "$SEED_TGZ" | cut -f1)
echo "==> seed created: $SEED_TGZ  ($FINAL_SIZE)"

# ============================================================================
# Step 6 — write seed.env (committed; throwaway creds).
# ============================================================================
cat > "$SEED_ENV" <<EOF
# Tier-2 live e2e seed metadata. Throwaway test-controller credentials minted on a
# heavy UniFi OS Server stack and captured into unifi-seed.tgz — NOT real secrets
# (the controller is a local, disposable test fixture). See test/e2e/README.md.
UNIFI_APIKEY=$KEY
NETWORK_VERSION=$NETWORK_VERSION
UNIFI_OS_VERSION=$UNIFI_OS_VERSION
EOF
echo "==> wrote $SEED_ENV"

# ============================================================================
# Step 7 — tell the maintainer to self-validate (do NOT call make from here).
# ============================================================================
cat <<EOF

============================================================================
 SELF-VALIDATE the fresh seed before committing:

     make test-e2e

 That restores this seed into a fresh volume, boots the live heavy stack, and
 runs the live Go tests — proving the fresh seed restores + authenticates.
============================================================================

EOF

# ============================================================================
# Step 8 — tear down the mint stack.
# ============================================================================
echo "==> tearing down the mint stack"
mdc down -v >/dev/null 2>&1 || true
docker volume rm "$MINT_VOLUME" >/dev/null 2>&1 || true

echo ""
echo "============================================================================"
echo " SEED BAKED. After 'make test-e2e' passes, commit both artifacts:"
echo ""
echo "     git add test/e2e/unifi-seed.tgz test/e2e/seed.env && git commit"
echo ""
echo "   (the .tgz goes through git-lfs). You only need to re-run this on a"
echo "   controller version bump — the api_key schema/hash could change."
echo "============================================================================"
