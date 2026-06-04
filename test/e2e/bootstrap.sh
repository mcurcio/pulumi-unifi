#!/usr/bin/env bash
# bootstrap.sh — ONE-TIME seed bake for the lean Tier-2 e2e harness.
#
# Run by `make e2e-bootstrap`. The maintainer does this ONCE (and again only on
# an image/spec bump — see test/e2e/README.md, "Re-baking"). It:
#
#   1. boots a BLANK lean stack (mongo + plain Network app + Caddy TLS front),
#   2. pauses for the maintainer to complete the first-run wizard + mint an
#      X-API-Key by hand (there is no headless setup / key-mint API),
#   3. captures the key + the running Network version into test/e2e/seed.env,
#   4. snapshots the SEED COMPACTLY: `mongodump --archive --gzip` of the app's
#      databases (BSON, tiny) + a tar of the controller data dir
#      /usr/lib/unifi/data (keystore + system.properties — small), combined into
#      test/e2e/unifi-seed.tgz,
#   5. self-validates the fresh seed by restoring it into throwaway volumes,
#      booting, and curling the Integration API WITH the minted key (expect 200).
#
# Outputs (both COMMITTED — this is a throwaway test controller, baked creds are
# acceptable):
#   test/e2e/unifi-seed.tgz   (mongodump + data-dir tar — git-lfs)
#   test/e2e/seed.env         (UNIFI_APIKEY + NETWORK_VERSION — plain git)
#
# The mongodump+data seed is far smaller and more portable than a raw volume
# snapshot: the app's BSON gzips to a handful of MB, and the data dir is mostly
# the generated keystore. That is the size-minimization win over the heavy harness.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE="$HERE/docker-compose.yml"
SEED_TGZ="$HERE/unifi-seed.tgz"
SEED_ENV="$HERE/seed.env"
RESTORE="$HERE/restore.sh"
GENCERTS="$HERE/gen-certs.sh"

# Pin an explicit Compose project name so the named volumes are deterministic and
# match what restore.sh / the Makefile compute (volume = <project>_<name>).
PROJECT="pulumi-unifi-e2e"
MONGO_VOLUME="${PROJECT}_mongo_data"
# The custom controller image's data dir is /usr/lib/unifi/data (not /config).
DATA_VOLUME="${PROJECT}_unifi_data"

# Mongo creds — must match docker-compose.yml + init-mongo.js.
MONGO_ROOT_USER="root"
MONGO_ROOT_PASS="rootpass"
MONGO_APP_USER="unifi"
MONGO_APP_PASS="unifipass"
MONGO_DBS=(unifi unifi_stat unifi_audit)

GUI_URL="https://localhost:8443"
# The Integration API as the provider reaches it: through Caddy on host :11443.
HOST_API="https://127.0.0.1:11443/proxy/network/integration/v1/sites"

dc() { docker compose -p "$PROJECT" -f "$COMPOSE" "$@"; }

# --- readiness gate (shared shape with the test-e2e / test-mock targets) ------
# Poll the Integration API (through Caddy) until it answers non-gateway. The
# blank app returns 403 here (auth-gated); after restore + key it returns 200.
wait_ready() {
  local label="$1" tries="${2:-120}" code=000
  echo "==> waiting for the controller to serve the Integration API ($label)..."
  for i in $(seq 1 "$tries"); do
    code=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 3 "$HOST_API" || echo 000)
    case "$code" in
      000|502|503|504) sleep 5 ;;
      *) echo "    controller ready (HTTP $code, attempt $i)"; return 0 ;;
    esac
  done
  echo "ERROR: controller not ready on $HOST_API after $tries attempts (last HTTP $code)" >&2
  return 1
}

# ============================================================================
# Step 1 — boot a BLANK lean stack from clean volumes.
# ============================================================================
echo "==> generating the Caddy TLS cert"
"$GENCERTS"

echo "==> bringing up a blank lean UniFi stack (app boot is ~2-4 min)"
# Start from a clean slate so the bake never inherits stale state.
dc down -v >/dev/null 2>&1 || true
docker volume rm "$MONGO_VOLUME" "$DATA_VOLUME" >/dev/null 2>&1 || true
# Build the pinned controller image (verifies the .deb sha256) before bring-up.
dc build
dc up -d

wait_ready "blank first boot" 120

# ============================================================================
# Step 2 — manual wizard + key mint (the part that cannot be headless).
# ============================================================================
cat <<EOF

============================================================================
 MANUAL ONE-TIME SETUP  (do this now, in a browser)
============================================================================
 1. Open:  $GUI_URL
      (accept the self-signed cert warning — it's the app's own cert)
 2. Complete the first-run wizard:
      - Create a LOCAL admin (set a username/password you don't mind baking
        into the committed seed — this is a THROWAWAY test controller).
      - SKIP the Ubiquiti cloud / SSO login (choose local-only / "advanced").
      - DISABLE auto-update / "automatically optimize network" / remote
        access — anything that mutates state or phones home. Determinism > UX.
 3. Network app -> Settings -> Control Plane -> Integrations
      (older builds: Settings -> System -> Integrations / API).
      Create an API key. COPY it now — it is shown only once.

 SNAPSHOT PROMPTLY: do the above and continue immediately. The longer the
 controller runs, the more it accumulates (stats, logs) — seed bloat.
============================================================================

EOF

printf "Paste the minted X-API-Key, then press Enter: "
read -r API_KEY
if [ -z "${API_KEY:-}" ]; then
  echo "ERROR: no key entered; aborting (leaving the stack up for retry)." >&2
  exit 1
fi

# Confirm the key authorizes BEFORE we bother snapshotting.
echo "==> verifying the pasted key authorizes (expect 200)"
KCODE=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 \
  -H "X-API-KEY: $API_KEY" "$HOST_API" || echo 000)
if [ "$KCODE" != "200" ]; then
  echo "ERROR: the pasted key did not authorize (HTTP $KCODE, expected 200)." >&2
  echo "       Re-check the key / wizard and re-run e2e-bootstrap." >&2
  exit 1
fi

# Detect the running Network app version for the spec cross-check. The app
# reports it on its own /status (no key needed); fall back to "unknown".
echo "==> detecting running Network app version"
NETWORK_VERSION=$(
  curl -sk --max-time 5 "$GUI_URL/status" 2>/dev/null \
    | grep -oE '"server_version"[^,}]*' | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' || true
)
[ -n "$NETWORK_VERSION" ] || NETWORK_VERSION="unknown (fill from UI footer)"
echo "    detected Network version: $NETWORK_VERSION"

# ============================================================================
# Step 3 — persist creds + version (committed).
# ============================================================================
cat > "$SEED_ENV" <<EOF
# test/e2e/seed.env — committed creds for the THROWAWAY e2e controller.
# Baked into the seed snapshot; sourced by 'make test-e2e'. Not a real secret.
# Re-generated by 'make e2e-bootstrap'.
UNIFI_APIKEY=$API_KEY
# Network app version this seed was baked against. The custom controller image is
# pinned to the EXACT spec version (openapi/pin.env), so this should read 10.4.57
# — matching, not a gap. If it differs, the controller pin and the spec pin have
# drifted; re-check controller/Dockerfile's UNIFI_VERSION.
NETWORK_VERSION=$NETWORK_VERSION
EOF
echo "==> wrote $SEED_ENV"

# ============================================================================
# Step 4 — snapshot the seed: mongodump (--archive --gzip) + a data-dir tar.
# ============================================================================
# Stop the app first so the DB is quiescent (mongo stays up for the dump).
echo "==> stopping the Network app for a clean DB snapshot (mongo stays up)"
dc stop unifi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "==> mongodump (--archive --gzip) of: ${MONGO_DBS[*]}"
# Dump each app DB into one gzip archive. Run mongodump inside the running mongo
# container so we use its own client + localhost socket (auth as root).
DUMP_ARGS=""
for d in "${MONGO_DBS[@]}"; do DUMP_ARGS="$DUMP_ARGS --db=$d"; done
# shellcheck disable=SC2086
dc exec -T mongo sh -c \
  "mongodump --username '$MONGO_ROOT_USER' --password '$MONGO_ROOT_PASS' \
     --authenticationDatabase admin $DUMP_ARGS --archive --gzip" \
  > "$WORK/mongo.archive.gz"
echo "    mongo archive: $(du -h "$WORK/mongo.archive.gz" | cut -f1)"

echo "==> tarring the data dir /usr/lib/unifi/data (keystore + system.properties — small)"
# Exclude regenerated bloat (logs, run-time scratch); keep the app keystore +
# system.properties so the restored app keeps the same identity. entrypoint.sh
# regenerates system.properties from MONGO_* env on every boot, so the mongo
# wiring is never frozen here — only the keystore/identity matters in the tar.
docker run --rm -v "$DATA_VOLUME":/data:ro -v "$WORK":/out alpine:3 \
  sh -c "cd /data && tar -czf /out/data.tgz \
           --exclude='./logs' --exclude='./run' --exclude='./*.pid' \
           ./ "
echo "    data tar: $(du -h "$WORK/data.tgz" | cut -f1)"

echo "==> combining into $SEED_TGZ"
rm -f "$SEED_TGZ"
tar -czf "$SEED_TGZ" -C "$WORK" mongo.archive.gz data.tgz

FINAL_SIZE=$(du -h "$SEED_TGZ" | cut -f1)
echo "==> seed created: $SEED_TGZ  ($FINAL_SIZE)"

# ============================================================================
# Step 5 — self-validate: restore the fresh seed into throwaway volumes, boot,
# and prove the Integration API serves WITH the minted key (expect 200).
# ============================================================================
echo "==> self-validating the fresh seed"
VPROJECT="pulumi-unifi-e2e-validate"
VMONGO="${VPROJECT}_mongo_data"
VDATA="${VPROJECT}_unifi_data"
vdc() { docker compose -p "$VPROJECT" -f "$COMPOSE" "$@"; }

cleanup_validate() {
  echo "==> tearing down both bake + self-validation stacks"
  vdc down -v >/dev/null 2>&1 || true
  docker volume rm "$VMONGO" "$VDATA" >/dev/null 2>&1 || true
  dc down -v >/dev/null 2>&1 || true
  docker volume rm "$MONGO_VOLUME" "$DATA_VOLUME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup_validate EXIT

# Restore into the validation project's volumes (distinct from the bake volumes).
"$RESTORE" "$SEED_TGZ" "$VMONGO" "$VDATA"

vdc up -d
wait_ready "restored-seed validation" 120

echo "==> curling the Integration API WITH the minted key (expect 200)"
VCODE=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 \
  -H "X-API-KEY: $API_KEY" "$HOST_API" || echo 000)
if [ "$VCODE" != "200" ]; then
  echo "ERROR: restored seed did NOT authenticate (HTTP $VCODE, expected 200)." >&2
  echo "       The seed or key is bad — do NOT commit. Re-run e2e-bootstrap." >&2
  exit 1
fi

echo ""
echo "============================================================================"
echo " SEED VALIDATED. Outputs ready to commit:"
echo "   $SEED_TGZ   ($FINAL_SIZE)   [git-lfs]"
echo "   $SEED_ENV   [plain git]"
echo ""
echo " Next: review, then commit both. Thereafter 'make test-e2e' is repeatable."
echo "============================================================================"
