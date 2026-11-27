#!/usr/bin/env bash
# run.sh — the private-API e2e tier (docs/design/private-binding.md §11), end to
# end, as one entrypoint (`make test-e2e-private` calls it):
#
#   1. build   make PRIVATE=1 build python_sdk (pinned installer -> model ->
#              merged schema -> plugin + Python SDK); a venv with the SDK
#   2. image   docker load the image.tar from the SAME pinned installer (so the
#              model and the target controller are one version); tag it
#   3. boot    a fresh UniFi OS Server container (no wizard, no key, no edge)
#   4. reach   copy the static forwarder in: 0.0.0.0:18081 -> 127.0.0.1:8081
#   5. ready   poll a v2 list for JSON 200 (8 min limit)
#   6. drive   go test -tags e2e_private: the pinned Pulumi CLI runs the Python
#              program (create, read, no-op, update, drift, import, destroy)
#   7. teardown (always, via trap) the container and the scratch dir
#
# OFFLINE / local tier: the image runs systemd and needs --privileged plus the
# host cgroup mount, and the installer is 875 MB. Not stock CI.
#
# Usage: test/e2e-private/run.sh [--keep-up] [-- <extra go test args>]
#
# Environment:
#   PULUMI          the Pulumi CLI (default: `pulumi` on PATH). Use the version
#                   provider/go.mod pins (3.230.0).
#   UOS_INSTALLER   reuse a local copy of the pinned installer (checked against
#                   uos/pin.env), as for `make uos-fetch`
#   UNIFI_UOS_CACHE installer + image.tar cache (default ~/.cache/pulumi-unifi/uos)
#   E2E_PORT        host port for the forwarded Network app (default 18081)
#   E2E_RELOAD=1    docker load the image again even if the tag exists
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
command -v go >/dev/null 2>&1 || export PATH="$PATH:/usr/local/go/bin"

KEEP_UP=
GOTEST_ARGS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --keep-up) KEEP_UP=1; shift ;;
    --) shift; GOTEST_ARGS=("$@"); break ;;
    *) echo "unknown arg: $1 (use --keep-up, or -- <go test args>)" >&2; exit 2 ;;
  esac
done

# shellcheck source=../../uos/pin.env
. "$ROOT/uos/pin.env"
CACHE="${UNIFI_UOS_CACHE:-$HOME/.cache/pulumi-unifi/uos}/$UOS_SHA256"
IMAGE_TAR="$CACHE/image.tar"
TAG="uosserver:uos-${UOS_VERSION}-${UOS_SHA256:0:12}"
NAME="uos-e2e-private-$$"
PORT="${E2E_PORT:-18081}"
BASE="http://127.0.0.1:$PORT"

PULUMI="${PULUMI:-$(command -v pulumi || true)}"
[ -x "$PULUMI" ] || { echo "ERROR: no Pulumi CLI (set PULUMI=<path>)" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "ERROR: docker not found" >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo "ERROR: docker daemon not reachable" >&2; exit 1; }
export PATH="$(dirname "$PULUMI"):$PATH"
echo "==> Pulumi CLI $("$PULUMI" version) ($PULUMI)"

WORK=$(mktemp -d "${TMPDIR:-/tmp}/unifi-e2e-private.XXXXXX")
teardown() {
  rc=$?
  if [ -n "$KEEP_UP" ]; then
    echo "==> --keep-up: controller $NAME left running on $BASE; work dir $WORK"
    echo "    remove with: docker rm -f $NAME; rm -rf $WORK"
    return
  fi
  echo "==> tearing down"
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  if [ "$rc" -ne 0 ] && [ -f "$WORK/pulumi.log" ]; then
    echo "==> last Pulumi output (full transcript was $WORK/pulumi.log):"
    tail -n 60 "$WORK/pulumi.log" || true
  fi
  rm -rf "$WORK"
}
trap teardown EXIT

# --- 1. build ------------------------------------------------------------------
echo "==> building the PRIVATE=1 plugin and Python SDK"
make -C "$ROOT" PRIVATE=1 build python_sdk
VENV="$ROOT/build/e2e-private/venv"
if [ ! -x "$VENV/bin/python" ]; then
  python3 -m venv "$VENV"
fi
"$VENV/bin/pip" install --quiet --disable-pip-version-check "$ROOT/sdk/python"
"$VENV/bin/pip" install --quiet --disable-pip-version-check --no-deps --force-reinstall "$ROOT/sdk/python"
"$VENV/bin/python" -c 'import pulumi_unifi.network as n; print("==> SDK private kinds:", ", ".join(getattr(n, k).__name__ for k in ("ApGroup", "FirewallAppBlock", "NetworkMembersGroup", "QosRule")))'

# --- 2. image ------------------------------------------------------------------
if [ -n "${E2E_RELOAD:-}" ] || ! docker image inspect "$TAG" >/dev/null 2>&1; then
  if [ ! -f "$IMAGE_TAR" ]; then
    echo "==> extracting image.tar from the pinned installer"
    INSTALLER=$("$ROOT/uos/fetch.sh")
    "$ROOT/bin/pulumi-gen-unifi" uos-extract -installer "$INSTALLER" -pin "$ROOT/uos/pin.env" \
      -spec-pin "$ROOT/openapi/pin.env" -out "$WORK/model.json" -image-out "$IMAGE_TAR.tmp"
    mv "$IMAGE_TAR.tmp" "$IMAGE_TAR"
  fi
  echo "==> docker load $IMAGE_TAR"
  # (Not `tee /dev/stderr`: reopening stderr truncates a redirected log.)
  out=$(docker load -i "$IMAGE_TAR")
  echo "$out"
  loaded=$(printf '%s\n' "$out" | sed -n 's/^Loaded image: //p; s/^Loaded image ID: //p' | tail -n1)
  [ -n "$loaded" ] || { echo "ERROR: docker load named no image" >&2; exit 1; }
  docker tag "$loaded" "$TAG"
fi
echo "==> image $TAG ($(docker image inspect -f '{{.Id}}' "$TAG"))"

# --- 3. boot -------------------------------------------------------------------
# The Network app needs a VERSION-5 UOS_UUID ("Invalid uuid version - 4"
# otherwise; private-binding.md §14 row 26). Fixed name -> fixed UUID.
UUID=$(python3 -c 'import uuid; print(uuid.uuid5(uuid.NAMESPACE_DNS, "pulumi-unifi-e2e-private"))')
echo "==> booting $NAME"
docker run -d --name "$NAME" --privileged --cgroupns=host \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run:exec --tmpfs /run/lock --tmpfs /tmp:exec \
  --stop-signal SIGRTMIN+3 \
  -e UOS_UUID="$UUID" -e APP_MODEL=UOSSERVER -e APP_VERSION="$UOS_VERSION" \
  -e PRODUCT_NAME=uosserver -e FIRMWARE_PLATFORM="$UOS_PLATFORM" \
  -p "127.0.0.1:$PORT:18081" "$TAG" >/dev/null

# --- 4. reach ------------------------------------------------------------------
# The Network app binds 127.0.0.1:8081 inside the container; the static
# forwarder (stdlib Go, no extra image) exposes it on the published port.
(cd "$HERE" && CGO_ENABLED=0 go build -o "$WORK/fwd" ./fwd)
docker cp "$WORK/fwd" "$NAME:/usr/local/bin/e2e-fwd"
docker exec -d "$NAME" /usr/local/bin/e2e-fwd 0.0.0.0:18081 127.0.0.1:8081

# --- 5. ready ------------------------------------------------------------------
echo "==> waiting for the Network app ($BASE/v2/api/site/default/apgroups; boot takes 3-5 min)"
ready=
start=$(date +%s)
while [ $(( $(date +%s) - start )) -lt 480 ]; do
  body=$(curl -s --max-time 5 -w '\n%{http_code}' "$BASE/v2/api/site/default/apgroups" 2>/dev/null || true)
  code=${body##*$'\n'}
  case "$code:${body:0:1}" in
    200:\[) ready=1; break ;;
  esac
  if [ "$(docker inspect -f '{{.State.Running}}' "$NAME" 2>/dev/null)" != true ]; then
    echo "ERROR: container $NAME stopped" >&2; docker logs --tail 50 "$NAME" >&2 || true; exit 1
  fi
  sleep 5
done
[ -n "$ready" ] || { echo "ERROR: Network app not serving JSON on $BASE after 8 min" >&2; exit 1; }
echo "==> controller ready after $(( $(date +%s) - start ))s"

# --- 6. drive ------------------------------------------------------------------
echo "==> running the e2e driver"
(cd "$HERE" && PATH="$ROOT/bin:$VENV/bin:$PATH" E2E_PULUMI="$PULUMI" E2E_BASE_URL="$BASE" E2E_WORK="$WORK" \
  go test -tags e2e_private -count=1 -v -timeout 15m -run TestPrivateE2E . ${GOTEST_ARGS[@]+"${GOTEST_ARGS[@]}"})
echo "==> private e2e PASSED"
