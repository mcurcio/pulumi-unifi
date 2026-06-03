#!/usr/bin/env bash
# Fetch the pinned UniFi Network Integration API OpenAPI spec used as the
# code-generation input. Pinned by commit SHA for reproducibility; the download
# is verified against the recorded checksum. See ./SOURCE for provenance.
set -euo pipefail

REPO="beezly/unifi-apis"
SHA="ea6a5bc3bb7a8744768fb64f5717b3694db104c7"
SRC_PATH="unifi-network/10.4.57.json"
OUT="unifi-network-10.4.57.json"
WANT_SHA256="ee1492cf23390482e4c1fd263dd199c5e0650959a1b60bb946a5e773da3d035b"

cd "$(dirname "$0")"
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
