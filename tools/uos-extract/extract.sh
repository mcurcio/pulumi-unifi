#!/usr/bin/env bash
# Extract the UniFi Network model sources from Ubiquiti's official UniFi OS Server
# release, WITHOUT executing the installer. Research tool (docs/research/private-api-model-source.md).
#
#   tools/uos-extract/extract.sh <uos-version> <out-dir>      e.g. 5.1.15 /tmp/uos-5.1.15
#
# Output (never commit it: Ubiquiti's files carry no license):
#   <out>/installer             the downloaded installer (SHA-256 checked against fw-update)
#   <out>/image.tar             the OCI image archive embedded in the installer
#   <out>/ace.jar               /usr/lib/unifi/lib/ace.jar (Spring Boot fat jar)
#   <out>/internal-dependencies.jar  BOOT-INF/lib/internal-dependencies.jar inside ace.jar
#   <out>/fields/*.json         api/fields/*.json (the classic /api/s/{site}/rest field definitions)
#   <out>/integration.json      webapps/ROOT/api-docs/integration.json (official Integration API OpenAPI)
#   <out>/classes/              every .class from internal-dependencies.jar (for routes.py / dto.py)
#   <out>/versions.txt          the image's build ARGs (UNIFI_VERSION, UNIFI_CORE_VERSION, ...)
#
# Needs: curl, jq, unzip, tar, sha256sum. Network: fw-update.ubnt.com, fw-download.ubnt.com.
set -euo pipefail

VER="${1:?usage: extract.sh <uos-version> <out-dir>}"
OUT="${2:?usage: extract.sh <uos-version> <out-dir>}"
PLATFORM="${PLATFORM:-linux-x64}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

API="https://fw-update.ubnt.com/api/firmware?filter=eq~~product~~unifi-os-server&filter=eq~~platform~~${PLATFORM}&limit=100"
ENTRY="$(curl -fsS "$API" | jq -c --arg v "v${VER}" '._embedded.firmware[] | select(.version == $v)')"
[[ -n "$ENTRY" ]] || { echo "no unifi-os-server ${PLATFORM} release v${VER} on fw-update" >&2; exit 1; }
URL="$(jq -r '._links.data.href' <<<"$ENTRY")"
WANT="$(jq -r '.sha256_checksum' <<<"$ENTRY")"

if [[ ! -f "$OUT/installer" ]] || [[ "$(sha256sum "$OUT/installer" | cut -d' ' -f1)" != "$WANT" ]]; then
  echo "downloading $URL" >&2
  curl -fsS -o "$OUT/installer.part" "$URL"
  mv "$OUT/installer.part" "$OUT/installer"
fi
GOT="$(sha256sum "$OUT/installer" | cut -d' ' -f1)"
[[ "$GOT" == "$WANT" ]] || { echo "sha256 mismatch: want $WANT got $GOT" >&2; exit 1; }
echo "installer sha256 $GOT (matches fw-update)" >&2

# The installer is a Rust ELF with a ZIP archive appended; unzip reads it in place
# (it warns about the ELF prefix as "extra bytes", which is expected).
unzip -o -q "$OUT/installer" image.tar -d "$OUT" 2>/dev/null || [[ -f "$OUT/image.tar" ]]

OCI="$(mktemp -d "$OUT/oci.XXXX")"
trap 'rm -rf "$OCI"' EXIT
tar -xf "$OUT/image.tar" -C "$OCI" manifest.json
CONFIG="$(jq -r '.[0].Config' "$OCI/manifest.json")"
tar -xf "$OUT/image.tar" -C "$OCI" "$CONFIG"
jq -r '.history[].created_by | select(startswith("ARG ")) | sub("^ARG "; "")' "$OCI/$CONFIG" > "$OUT/versions.txt"

# Find the layer that carries the Network app, then pull only what we need from it.
for L in $(jq -r '.[0].Layers[]' "$OCI/manifest.json"); do
  tar -xf "$OUT/image.tar" -C "$OCI" "$L"
  tar -tf "$OCI/$L" > "$OCI/layer.list"    # list to a file: `grep -q` on a pipe trips pipefail
  if grep -qx 'usr/lib/unifi/lib/ace.jar' "$OCI/layer.list"; then
    tar -xf "$OCI/$L" -C "$OCI" usr/lib/unifi/lib/ace.jar usr/lib/unifi/webapps/ROOT/api-docs/integration.json
    LAYER="$L"
    break
  fi
  rm -f "$OCI/$L"
done
[[ -n "${LAYER:-}" ]] || { echo "no layer carries usr/lib/unifi/lib/ace.jar" >&2; exit 1; }
cp "$OCI/usr/lib/unifi/lib/ace.jar" "$OUT/ace.jar"
cp "$OCI/usr/lib/unifi/webapps/ROOT/api-docs/integration.json" "$OUT/integration.json"

# 10.x: the field JSON and the app classes live in the nested internal-dependencies.jar.
unzip -o -q -j "$OUT/ace.jar" BOOT-INF/lib/internal-dependencies.jar -d "$OUT"
rm -rf "$OUT/fields" "$OUT/classes"
mkdir -p "$OUT/fields" "$OUT/classes"
unzip -o -q -j "$OUT/internal-dependencies.jar" 'api/fields/*.json' -d "$OUT/fields"
unzip -o -q "$OUT/internal-dependencies.jar" '*.class' -d "$OUT/classes"

echo "layer          $LAYER"
grep -E '^(UNIFI_VERSION|UNIFI_CORE_VERSION|UOS_VERSION)=' "$OUT/versions.txt"
echo "field files    $(ls "$OUT/fields" | wc -l)"
echo "classes        $(find "$OUT/classes" -name '*.class' | wc -l)"
echo "integration    $(jq -r '.info.version' "$OUT/integration.json") ($(jq '.paths | length' "$OUT/integration.json") paths, sha256 $(sha256sum "$OUT/integration.json" | cut -d' ' -f1))"
