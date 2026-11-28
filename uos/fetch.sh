#!/usr/bin/env bash
# Get the pinned UniFi OS Server installer into the cache and print its path.
# The pin is ./pin.env (see ./SOURCE). The installer is a build INPUT: it is
# never executed and never committed.
#
#   uos/fetch.sh                       # download unless the cache holds it
#   UOS_INSTALLER=/path/to/x uos/fetch.sh   # reuse a local copy (checked first)
#
# Cache: ${UNIFI_UOS_CACHE:-$HOME/.cache/pulumi-unifi/uos}/<sha256>/installer.
# Only the PINNED SHA-256 is trusted (never fw-update's own checksum field).
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=pin.env
. "$HERE/pin.env"

CACHE_ROOT="${UNIFI_UOS_CACHE:-${HOME}/.cache/pulumi-unifi/uos}"
DIR="${CACHE_ROOT}/${UOS_SHA256}"
DEST="${DIR}/installer"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else shasum -a 256 "$1" | awk '{print $1}'; fi
}

verify() { # <file>: size + pinned sha256
  local f="$1" size got
  size="$(wc -c < "$f" | tr -d ' ')"
  if [[ "$size" != "$UOS_SIZE" ]]; then
    echo "ERROR: $f is $size bytes, the pin says $UOS_SIZE" >&2; return 1
  fi
  got="$(sha256_of "$f")"
  if [[ "$got" != "$UOS_SHA256" ]]; then
    echo "ERROR: checksum mismatch for $f" >&2
    echo "  want: $UOS_SHA256" >&2
    echo "  got:  $got" >&2
    return 1
  fi
}

mkdir -p "$DIR"

# A cached copy is trusted only after a re-check (cheap next to a download).
if [[ -f "$DEST" ]]; then
  if verify "$DEST"; then echo "$DEST"; exit 0; fi
  echo "cached installer failed verification; replacing it" >&2
  rm -f "$DEST"
fi

if [[ -n "${UOS_INSTALLER:-}" ]]; then
  echo "reusing UOS_INSTALLER=${UOS_INSTALLER}" >&2
  verify "$UOS_INSTALLER"
  ln -f "$UOS_INSTALLER" "$DEST.part" 2>/dev/null || cp "$UOS_INSTALLER" "$DEST.part"
else
  echo "downloading UniFi OS Server ${UOS_VERSION} (${UOS_PLATFORM}, ${UOS_SIZE} bytes) from ${UOS_URL}" >&2
  curl -fSL --retry 3 -o "$DEST.part" "$UOS_URL"
  verify "$DEST.part" || { rm -f "$DEST.part"; exit 1; }
fi
mv -f "$DEST.part" "$DEST"
echo "OK: UniFi OS Server ${UOS_VERSION} installer (sha256 ${UOS_SHA256})" >&2
echo "$DEST"
