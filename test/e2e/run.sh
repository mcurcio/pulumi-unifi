#!/usr/bin/env bash
# run.sh — the Tier-2 live e2e pipeline, end to end, as ONE executable entrypoint.
#
#   restore the committed seed into a fresh volume
#     → boot the heavy UniFi OS controller (the only image that authenticates the
#       Integration API — see README.md "Why the heavy image")
#       → wait for the API to serve AUTHENTICATED JSON (not just an edge 200)
#         → run the e2e-tagged Go tests against the live controller
#           → tear everything down (always, via trap)
#
# This is the OFFLINE / local tier: the heavy image runs systemd + needs caps +
# a host cgroup mount, so it is NOT stock CI. Tier-1 (`make test`, `make
# test-mock`) stays the portable, zero-privilege gate.
#
# Usage:
#   test/e2e/run.sh [--keep-up] [-- <extra go test args>]
#     --keep-up   leave the controller running after the tests (skip teardown)
#                 for debugging; print how to remove it manually.
#     everything after `--` is forwarded to `go test` (e.g. -v).
#
# Prereqs: Docker (privileged/caps — local Docker, not a constrained CI service
# container), git-lfs (the seed is an LFS object), Go. The image is pulled by
# digest. `make test-e2e` calls this after `make build`; run directly, it will
# `make build` the generated artifacts if they are missing.
set -euo pipefail

# --- resolve paths so this works from any cwd -------------------------------
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
COMPOSE="$SCRIPT_DIR/docker-compose.yml"
SEED="$SCRIPT_DIR/unifi-seed.tgz"
ENV_FILE="$SCRIPT_DIR/seed.env"
PROJECT="pulumi-unifi-e2e"
VOLUME="${PROJECT}_uos_data"
ADDR="127.0.0.1:11443"
SITES_URL="https://${ADDR}/proxy/network/integration/v1/sites"
ARTIFACT="$ROOT/provider/cmd/pulumi-resource-unifi/schema.json"

# Make `go` discoverable even when invoked from a bare shell (cron, IDE, etc.).
command -v go >/dev/null 2>&1 || export PATH="$PATH:/usr/local/go/bin:$HOME/go/bin:/opt/homebrew/bin"

# --- args --------------------------------------------------------------------
KEEP_UP=
GOTEST_ARGS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --keep-up) KEEP_UP=1; shift ;;
    --) shift; GOTEST_ARGS=("$@"); break ;;
    *) echo "unknown arg: $1 (use --keep-up, or -- <go test args>)" >&2; exit 2 ;;
  esac
done

# --- preflight ---------------------------------------------------------------
command -v docker >/dev/null 2>&1 || { echo "ERROR: docker not found on PATH" >&2; exit 1; }
command -v go     >/dev/null 2>&1 || { echo "ERROR: go not found on PATH" >&2; exit 1; }
[ -f "$SEED" ]     || { echo "ERROR: $SEED missing — run 'make e2e-bootstrap' (and 'git lfs pull')" >&2; exit 1; }
[ -f "$ENV_FILE" ] || { echo "ERROR: $ENV_FILE missing — run 'make e2e-bootstrap'" >&2; exit 1; }
# A tiny seed means an un-fetched git-lfs pointer.
if [ "$(wc -c < "$SEED")" -lt 1048576 ]; then
  echo "ERROR: $SEED looks like an un-fetched git-lfs pointer — run 'git lfs install && git lfs pull'" >&2
  exit 1
fi
# The e2e Go test reads the generated artifacts; build them if absent.
if [ ! -f "$ARTIFACT" ]; then
  echo "==> generated provider artifacts missing — building (make build)"
  make -C "$ROOT" build
fi

# --- key from the seed env ---------------------------------------------------
set -a; . "$ENV_FILE"; set +a
: "${UNIFI_APIKEY:?seed.env must define UNIFI_APIKEY}"

# --- teardown (always, unless --keep-up) ------------------------------------
teardown() {
  if [ -n "$KEEP_UP" ]; then
    echo "==> --keep-up set: controller left running on https://$ADDR"
    echo "    remove it with: docker compose -p $PROJECT -f $COMPOSE down -v && docker volume rm $VOLUME"
    return
  fi
  echo "==> tearing down"
  docker compose -p "$PROJECT" -f "$COMPOSE" down -v >/dev/null 2>&1 || true
  docker volume rm "$VOLUME" >/dev/null 2>&1 || true
}
trap teardown EXIT

# --- 1. restore seed into a fresh volume ------------------------------------
"$SCRIPT_DIR/restore.sh" "$SEED" "$VOLUME"

# --- 2. boot -----------------------------------------------------------------
echo "==> booting heavy UniFi OS controller (image pinned by digest)"
docker compose -p "$PROJECT" -f "$COMPOSE" up -d

# --- 3. readiness gate: AUTHENTICATED JSON, not just an edge 200 ------------
# During boot the edge nginx answers 200 with an HTML loading page well before
# unifi-core + the Network app are ready, so gate on a real JSON body ('{'…).
echo "==> waiting for the Integration API to serve authenticated JSON (heavy boot ~1-4 min)"
ready=
for i in $(seq 1 120); do
  body=$(curl -sk --max-time 3 -H "X-API-KEY: $UNIFI_APIKEY" "$SITES_URL" 2>/dev/null || true)
  case "$body" in
    '{'*) echo "==> controller ready (authenticated JSON, attempt $i)"; ready=1; break ;;
    *)    sleep 5 ;;
  esac
done
[ -n "$ready" ] || { echo "ERROR: integration API not serving JSON on https://$ADDR after timeout" >&2; exit 1; }

# --- 4. run the live e2e tests ----------------------------------------------
echo "==> running e2e tests against the live controller"
UNIFI_E2E_ADDR="$ADDR" UNIFI_APIKEY="$UNIFI_APIKEY" \
  go -C "$ROOT/provider" test -tags e2e -count=1 -run TestE2E ./pkg/provider/ ${GOTEST_ARGS[@]+"${GOTEST_ARGS[@]}"}
