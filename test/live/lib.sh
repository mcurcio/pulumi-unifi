# lib.sh — the shared live-controller harness (docs/design/private-binding.md
# §S2.10), sourced by test/e2e/run.sh: resolve the pinned image, restore the
# seed, boot, apply the sandbox workarounds when needed, wait for authenticated
# JSON, tear down. Our own shell; no Ubiquiti files.
#
# Inputs (set by the caller before sourcing): LIVE_PROJECT, LIVE_COMPOSE,
# LIVE_VOLUME, LIVE_SEED, LIVE_ADDR. The API key is read by the caller from
# test/e2e/seed.env into UNIFI_APIKEY; this file never prints it.
#
# Environment:
#   E2E_SANDBOX  auto (default): the workarounds are on when the kernel has no
#                IPv6 (/proc/net/if_inet6 absent); 1 forces them on, 0 off.
#   E2E_IMAGE    an image reference to use instead of resolving the pin.
#   LIVE_LOG     a file that records how the image was obtained and which
#                workarounds ran (the "run log"; never holds the key).

LIVE_HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
LIVE_DIGEST="sha256:3e8854769d634ed5b7b4610b187479226fb8f47178552d1764b1b1db68c8621e"
LIVE_REPO="hieutq/unifi-os-server"
LIVE_MIRROR="mirror.gcr.io/hieutq/unifi-os-server"
LIVE_LOG="${LIVE_LOG:-/dev/null}"

live_log() { echo "==> $*"; echo "$(date -u +%FT%TZ) $*" >> "$LIVE_LOG"; }

# live_sandbox: 0 or 1.
live_sandbox() {
  case "${E2E_SANDBOX:-auto}" in
    1) echo 1 ;;
    0) echo 0 ;;
    *) if [ -e /proc/net/if_inet6 ]; then echo 0; else echo 1; fi ;;
  esac
}

# live_image sets E2E_IMAGE to a local reference of the pinned digest: already
# present, else Docker Hub, else the mirror (same digest). Both pulls retry,
# since both registries rate-limit.
live_image() {
  if [ -n "${E2E_IMAGE:-}" ]; then
    live_log "image: $E2E_IMAGE (E2E_IMAGE override)"; return 0
  fi
  local ref
  for ref in "$LIVE_REPO@$LIVE_DIGEST" "$LIVE_MIRROR@$LIVE_DIGEST"; do
    if docker image inspect "$ref" >/dev/null 2>&1; then
      export E2E_IMAGE="$ref"; live_log "image: $ref (already local)"; return 0
    fi
  done
  local i
  for ref in "$LIVE_REPO@$LIVE_DIGEST" "$LIVE_MIRROR@$LIVE_DIGEST"; do
    for i in 1 2 3 4; do
      if docker pull -q "$ref" >/dev/null 2>&1; then
        export E2E_IMAGE="$ref"; live_log "image: $ref (pulled)"; return 0
      fi
      sleep $((i * 15))
    done
    live_log "image: pull of $ref failed"
  done
  echo "ERROR: cannot get $LIVE_REPO@$LIVE_DIGEST from Docker Hub or $LIVE_MIRROR" >&2
  return 1
}

# live_compose runs docker compose with the sandbox override when it is on.
live_compose() {
  if [ "$(live_sandbox)" = 1 ]; then
    docker compose -p "$LIVE_PROJECT" -f "$LIVE_COMPOSE" -f "$LIVE_HERE/sandbox.compose.yml" "$@"
  else
    docker compose -p "$LIVE_PROJECT" -f "$LIVE_COMPOSE" "$@"
  fi
}

# live_restore recreates the volume from the seed, untarring with the UniFi OS
# image's own tar (no extra image pull).
live_restore() {
  local abs
  abs=$(cd "$(dirname "$LIVE_SEED")" && pwd)/$(basename "$LIVE_SEED")
  live_log "restoring the seed into $LIVE_VOLUME"
  docker volume rm "$LIVE_VOLUME" >/dev/null 2>&1 || true
  docker volume create "$LIVE_VOLUME" >/dev/null
  docker run --rm --entrypoint /bin/sh -v "$LIVE_VOLUME":/unifi -v "$abs":/seed/unifi-seed.tgz:ro "$E2E_IMAGE" \
    -c "tar -xpzf /seed/unifi-seed.tgz -C /unifi && rm -f /unifi/data/postgresql/postmaster.pid /unifi/db/mongod.lock 2>/dev/null; true"
}

live_container() { live_compose ps -q uos; }

# live_sandbox_fixups starts the discovery stub and the IPv6 listen stripper
# inside the container, then restarts unifi-core so it finds its LAN IP.
live_sandbox_fixups() {
  [ "$(live_sandbox)" = 1 ] || { live_log "sandbox workarounds: off"; return 0; }
  local c
  c=$(live_container)
  live_log "sandbox workarounds: on (privileged, discovery stub, IPv6 listen stripping)"
  # systemd needs a moment before systemctl answers.
  local i
  for i in $(seq 1 60); do
    docker exec "$c" systemctl is-system-running >/dev/null 2>&1 && break
    docker exec "$c" systemctl list-units >/dev/null 2>&1 && break
    sleep 2
  done
  # Not /tmp: it is a tmpfs in the container, and docker cp writes beneath it.
  docker cp "$LIVE_HERE/discovery-stub.js" "$c:/opt/e2e-discovery-stub.js"
  # The image ships node under a versioned name (node24 in 5.1.15).
  docker exec -d "$c" sh -c 'n=$(command -v node || ls /usr/bin/node* 2>/dev/null | head -n1); exec "$n" /opt/e2e-discovery-stub.js >/tmp/discovery-stub.log 2>&1'
  # Strip `listen [::]` lines as unifi-core writes them and reload nginx
  # (unifi-core writes its configs in several steps, so a failed reload is
  # retried). Never `systemctl restart nginx`: its ExecStartPre wipes
  # unifi-core's site configs. When the configs ask for :443 but nginx does not
  # listen on it, the reload could not bind (a stale uos-internal.sock, logged
  # only in nginx's error.log): remove the socket and reload again.
  docker exec -d "$c" sh -c '
    pending=
    while true; do
      for f in $(grep -rls "listen \[::\]" /data/unifi-core/config/http /etc/nginx 2>/dev/null); do
        sed -i "/listen \[::\]/d" "$f"; pending=1
      done
      if [ -n "$pending" ] && nginx -s reload 2>>/tmp/nginx-reload.err; then
        pending=; echo "$(date +%T) reloaded"; sleep 3
      fi
      if grep -qs "listen 443" /data/unifi-core/config/http/site-*.conf && ! ss -ltn | grep -q ":443 "; then
        find /data/unifi-core/config/http -name uos-internal.sock -exec rm -f {} + 2>/dev/null
        nginx -s reload 2>>/tmp/nginx-reload.err && echo "$(date +%T) not on :443: removed uos-internal.sock, reloaded"
        sleep 3
      fi
      sleep 2
    done >/tmp/ipv6-strip.log 2>&1'
  sleep 3
  docker exec "$c" systemctl restart unifi-core || true
}

# live_wait_ready polls the official sites list with the key until it answers
# JSON (the edge answers an HTML loading page well before the API is up).
live_wait_ready() {
  local url="https://$LIVE_ADDR/proxy/network/integration/v1/sites" start body
  start=$(date +%s)
  live_log "waiting for authenticated JSON on $url (limit 20 min)"
  while [ $(( $(date +%s) - start )) -lt 1200 ]; do
    # The header goes in on stdin (printf is a builtin), never in curl's argv.
    body=$(printf 'X-API-KEY: %s\n' "$UNIFI_APIKEY" | curl -sk --max-time 5 -H @- "$url" 2>/dev/null || true)
    case "$body" in
      '{'*'"data"'*) live_log "controller ready after $(( $(date +%s) - start ))s"; return 0 ;;
    esac
    sleep 5
  done
  echo "ERROR: the integration API did not serve JSON on $url within 20 min" >&2
  return 1
}

live_teardown() {
  live_compose down -v >/dev/null 2>&1 || true
  docker volume rm "$LIVE_VOLUME" >/dev/null 2>&1 || true
}
