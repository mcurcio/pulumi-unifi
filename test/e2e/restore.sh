#!/usr/bin/env bash
# restore.sh — repopulate a fresh Docker volume from the UniFi OS seed snapshot.
#
# The seed (unifi-seed.tgz) is a plain gzipped tar of the heavy controller's whole
# persistent state volume (/unifi). Because /data is a symlink to /unifi/data, that
# ONE volume holds everything: Mongo (/unifi/db), Postgres (/unifi/data/postgresql
# — which stores the api_key secret), unifi-core config, and the Network app data.
# So restoring is just: recreate the volume and untar the snapshot into it. After
# this, `docker compose up` boots UniFi OS against pre-seeded state and the minted
# key validates immediately — no wizard, no re-mint.
#
# The snapshot was taken from a GRACEFULLY stopped container (DBs flushed), so the
# data files are consistent; we still defensively clear a stale Postgres
# postmaster.pid in case a future bake stops less cleanly.
#
# Usage: restore.sh <seed.tgz> <volume>
#   e.g. restore.sh test/e2e/unifi-seed.tgz pulumi-unifi-e2e_uos_data
set -euo pipefail

SEED="${1:?usage: restore.sh <seed.tgz> <volume>}"
VOL="${2:?usage: restore.sh <seed.tgz> <volume>}"

if [ ! -f "$SEED" ]; then
  echo "ERROR: seed tarball not found: $SEED" >&2
  echo "       (is git-lfs installed and the LFS object pulled? run 'git lfs pull')" >&2
  exit 1
fi

# A tiny file means a git-lfs pointer was checked out but the object never fetched.
SIZE=$(wc -c < "$SEED")
if [ "$SIZE" -lt 1048576 ]; then
  echo "ERROR: $SEED is only ${SIZE} bytes — looks like an un-fetched git-lfs pointer." >&2
  echo "       Run: git lfs install && git lfs pull" >&2
  exit 1
fi

# Absolute path so the mount works regardless of caller cwd.
SEED_ABS=$(cd "$(dirname "$SEED")" && pwd)/$(basename "$SEED")

echo "==> recreating volume $VOL from scratch"
docker volume rm "$VOL" >/dev/null 2>&1 || true
docker volume create "$VOL" >/dev/null

echo "==> untarring seed into $VOL (perms preserved)"
docker run --rm \
  -v "$VOL":/unifi \
  -v "$SEED_ABS":/seed/unifi-seed.tgz:ro \
  alpine:3 \
  sh -c "tar -xpzf /seed/unifi-seed.tgz -C /unifi && rm -f /unifi/data/postgresql/postmaster.pid /unifi/db/mongod.lock 2>/dev/null; true"

echo "==> restore complete"
