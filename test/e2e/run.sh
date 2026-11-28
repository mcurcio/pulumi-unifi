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
# container), git-lfs (the seed is an LFS object), Go, Python 3 and the Pulumi
# CLI pinned in provider/go.mod (3.230.0; PULUMI=<path>). The image is pulled by
# digest (Docker Hub, else mirror.gcr.io with the same digest). `make test-e2e`
# calls this after `make build`; run directly, it will `make build` the
# generated artifacts if they are missing.
#
# The boot recipe (image, seed restore, sandbox workarounds, readiness) is the
# shared live harness test/live/lib.sh (docs/design/private-binding.md §S2.10).
# Up to three drivers run against one boot, chosen by E2E_DRIVERS (default
# "grpc cli"):
#   grpc  the gRPC tests (provider/pkg/provider, tag e2e)
#   cli   the Pulumi CLI driver (test/e2e/cli: ACL rules, ordering, alias
#         migration), PRIVATE=0 SDK
#   edge  the private-API driver (test/e2e-private, E2E_MODE=edge): builds the
#         PRIVATE=1 plugin + SDK (needs the pinned installer, UOS_INSTALLER=)
#         and runs the 4 private kinds through the authenticated edge with the
#         seed's key, post-wizard (private-binding.md §S2.10)
# `make test-e2e` runs "grpc cli", `make test-e2e-edge` "edge", `make test-live`
# all three on one boot. The key is read from seed.env into the environment
# only.
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

ARTIFACT="$ROOT/provider/cmd/pulumi-resource-unifi/schema.json"

# Make `go` discoverable even when invoked from a bare shell (cron, IDE, etc.).
command -v go >/dev/null 2>&1 || export PATH="$PATH:/usr/local/go/bin:$HOME/go/bin:/opt/homebrew/bin"

# --- drivers -------------------------------------------------------------------
DRIVERS="${E2E_DRIVERS:-grpc cli}"
has_driver() { case " $DRIVERS " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }
for d in $DRIVERS; do
  case "$d" in grpc|cli|edge) ;; *) echo "ERROR: unknown E2E_DRIVERS entry '$d' (grpc, cli, edge)" >&2; exit 2 ;; esac
done

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
# Only UNIFI_APIKEY is taken from seed.env, into this process's environment. It
# is never echoed, never put on a command line or in Pulumi config.
UNIFI_APIKEY=$(sed -n 's/^UNIFI_APIKEY=//p' "$ENV_FILE" | tail -n1)
export UNIFI_APIKEY
: "${UNIFI_APIKEY:?seed.env must define UNIFI_APIKEY}"

PULUMI="${PULUMI:-$(command -v pulumi || true)}"
[ -x "$PULUMI" ] || { echo "ERROR: no Pulumi CLI (set PULUMI=<path>; the pinned version is 3.230.0)" >&2; exit 1; }

WORK=$(mktemp -d "${TMPDIR:-/tmp}/unifi-e2e.XXXXXX")
export LIVE_PROJECT="$PROJECT" LIVE_COMPOSE="$COMPOSE" LIVE_VOLUME="$VOLUME" LIVE_SEED="$SEED" LIVE_ADDR="$ADDR"
export LIVE_LOG="$WORK/run.log"
# shellcheck source=../live/lib.sh
. "$ROOT/test/live/lib.sh"

# --- teardown (always, unless --keep-up) ------------------------------------
teardown() {
  rc=$?
  if [ -n "$KEEP_UP" ]; then
    echo "==> --keep-up set: controller left running on https://$ADDR; work dir $WORK"
    echo "    remove it with: docker compose -p $PROJECT -f $COMPOSE down -v && docker volume rm $VOLUME"
    return
  fi
  echo "==> tearing down"
  live_teardown
  if [ "$rc" -ne 0 ]; then
    for f in "$WORK/pulumi.log" "$WORK/edge/pulumi.log"; do
      [ -f "$f" ] || continue
      echo "==> last Pulumi output (full transcript was $f):"
      tail -n 60 "$f" || true
    done
  fi
  echo "==> run log:"; cat "$LIVE_LOG" 2>/dev/null || true
  rm -rf "$WORK"
}
trap teardown EXIT

# --- 1. image + seed ----------------------------------------------------------
live_image
live_restore

# --- 2. boot -----------------------------------------------------------------
live_log "booting the UniFi OS controller ($E2E_IMAGE), sandbox=$(live_sandbox)"
live_compose up -d
live_sandbox_fixups

# --- 3. readiness gate: AUTHENTICATED JSON, not just an edge 200 ------------
live_wait_ready

# --- 4. the gRPC e2e tests ----------------------------------------------------
if has_driver grpc; then
  echo "==> running the gRPC e2e tests against the live controller"
  UNIFI_E2E_ADDR="$ADDR" \
    go -C "$ROOT/provider" test -tags e2e -count=1 -run TestE2E ./pkg/provider/ ${GOTEST_ARGS[@]+"${GOTEST_ARGS[@]}"}
fi

# --- 5. the Pulumi CLI driver (PRIVATE=0 SDK) ---------------------------------
if has_driver cli; then
  echo "==> building the PRIVATE=0 Python SDK venv for the CLI driver"
  VENV="$ROOT/build/e2e/venv"
  [ -x "$VENV/bin/python" ] || python3 -m venv "$VENV"
  "$VENV/bin/pip" install --quiet --disable-pip-version-check "$ROOT/sdk/python"
  "$VENV/bin/pip" install --quiet --disable-pip-version-check --no-deps --force-reinstall "$ROOT/sdk/python"
  echo "==> running the CLI driver (ACL rules, ordering, alias migration)"
  (cd "$SCRIPT_DIR/cli" && PATH="$ROOT/bin:$VENV/bin:$(dirname "$PULUMI"):$PATH" \
    E2E_PULUMI="$PULUMI" E2E_API_HOST="$ADDR" E2E_WORK="$WORK" \
    go test -tags e2e_cli -count=1 -v -timeout 30m -run TestCLIE2E . ${GOTEST_ARGS[@]+"${GOTEST_ARGS[@]}"})
fi

# --- 6. the private-API driver through the authenticated edge (PRIVATE=1) -----
# Runs last: it rebuilds the plugin and SDK with PRIVATE=1, which the PRIVATE=0
# drivers above must not see.
if has_driver edge; then
  echo "==> building the PRIVATE=1 plugin and Python SDK for the edge driver"
  PATH="$(dirname "$PULUMI"):$PATH" make -C "$ROOT" PRIVATE=1 build python_sdk
  EVENV="$ROOT/build/e2e-private/venv"
  [ -x "$EVENV/bin/python" ] || python3 -m venv "$EVENV"
  "$EVENV/bin/pip" install --quiet --disable-pip-version-check "$ROOT/sdk/python"
  "$EVENV/bin/pip" install --quiet --disable-pip-version-check --no-deps --force-reinstall "$ROOT/sdk/python"
  echo "==> running the private-API driver through the edge (4 private kinds, post-wizard, with the key)"
  mkdir -p "$WORK/edge"
  (cd "$ROOT/test/e2e-private" && PATH="$ROOT/bin:$EVENV/bin:$(dirname "$PULUMI"):$PATH" \
    E2E_MODE=edge E2E_PULUMI="$PULUMI" E2E_API_HOST="$ADDR" E2E_WORK="$WORK/edge" \
    go test -tags e2e_private -count=1 -v -timeout 20m -run TestPrivateE2E . ${GOTEST_ARGS[@]+"${GOTEST_ARGS[@]}"})
  live_log "edge driver passed"
fi
echo "==> Tier-2 e2e PASSED (drivers: $DRIVERS)"
