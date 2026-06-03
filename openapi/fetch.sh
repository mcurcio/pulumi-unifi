#!/usr/bin/env bash
# Fetch the pinned UniFi Network Integration API OpenAPI spec used as the
# code-generation input. Pinned by commit SHA for reproducibility; the download
# is verified against the recorded checksum. The pin values are the single source
# of truth in ./pin.env (sourced below) — see ./SOURCE for provenance.
set -euo pipefail

cd "$(dirname "$0")"

# shellcheck source=pin.env
. ./pin.env

REPO="$SPEC_REPO"
SHA="$SPEC_SHA"
SRC_PATH="unifi-network/${SPEC_VERSION}.json"
OUT="unifi-network-${SPEC_VERSION}.json"
WANT_SHA256="$SPEC_SHA256"

URL="https://raw.githubusercontent.com/${REPO}/${SHA}/${SRC_PATH}"

echo "Fetching ${URL}"
curl -fsSL "$URL" -o "${OUT}.tmp"

GOT_SHA256="$(shasum -a 256 "${OUT}.tmp" | awk '{print $1}')"
if [[ "$GOT_SHA256" != "$WANT_SHA256" ]]; then
  echo "ERROR: checksum mismatch" >&2
  echo "  want: $WANT_SHA256" >&2
  echo "  got:  $GOT_SHA256" >&2
  rm -f "${OUT}.tmp"
  exit 1
fi

mv "${OUT}.tmp" "${OUT}"
echo "OK: ${OUT} (sha256 ${GOT_SHA256})"
