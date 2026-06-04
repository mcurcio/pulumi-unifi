#!/usr/bin/env bash
# restore.sh — repopulate fresh Docker volumes from a lean UniFi seed tarball.
#
# Used by the repeatable `make test-e2e` path and by bootstrap.sh's seed
# self-validation. The seed (unifi-seed.tgz) contains two members:
#   mongo.archive.gz  — `mongodump --archive --gzip` of the app's databases
#   config.tgz        — a tar of the app's /config volume
# This script recreates BOTH target volumes from scratch (so stale prior state
# can't leak), then:
#   - boots a THROWAWAY mongod bound to the fresh mongo volume, re-creates the
#     `unifi` app user (the admin-db user is not in the DB dump), `mongorestore`s
#     the archive, and shuts the throwaway mongod down;
#   - untars config.tgz into the fresh config volume (perms preserved).
# After this, `docker compose up` boots the app against pre-seeded volumes.
#
# Usage: restore.sh <seed.tgz> <mongo-volume> <config-volume>
#   e.g. restore.sh test/e2e/unifi-seed.tgz \
#          pulumi-unifi-e2e_mongo_data pulumi-unifi-e2e_unifi_config
set -euo pipefail

SEED="${1:?usage: restore.sh <seed.tgz> <mongo-volume> <config-volume>}"
MONGO_VOL="${2:?usage: restore.sh <seed.tgz> <mongo-volume> <config-volume>}"
CONFIG_VOL="${3:?usage: restore.sh <seed.tgz> <mongo-volume> <config-volume>}"

# Mongo creds — must match docker-compose.yml + init-mongo.js.
MONGO_ROOT_USER="root"
MONGO_ROOT_PASS="rootpass"
MONGO_APP_USER="unifi"
MONGO_APP_PASS="unifipass"

if [ ! -f "$SEED" ]; then
  echo "ERROR: seed tarball not found: $SEED" >&2
  echo "       (is git-lfs installed and the LFS object pulled? run 'git lfs pull')" >&2
  exit 1
fi

# A tiny file means a git-lfs pointer was checked out but the object never
# fetched. Catch that early with a friendly message.
SIZE=$(wc -c < "$SEED")
if [ "$SIZE" -lt 1024 ]; then
  echo "ERROR: $SEED is only ${SIZE} bytes — looks like an un-fetched git-lfs pointer." >&2
  echo "       Run: git lfs install && git lfs pull" >&2
  exit 1
fi

SEED_DIR=$(cd "$(dirname "$SEED")" && pwd)
SEED_FILE=$(basename "$SEED")

WORK="$(mktemp -d)"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

echo "==> extracting seed members"
tar -xzf "$SEED" -C "$WORK"
[ -f "$WORK/mongo.archive.gz" ] || { echo "ERROR: seed missing mongo.archive.gz" >&2; exit 1; }
[ -f "$WORK/config.tgz" ]       || { echo "ERROR: seed missing config.tgz" >&2; exit 1; }

# --- recreate target volumes from scratch -----------------------------------
for v in "$MONGO_VOL" "$CONFIG_VOL"; do
  echo "==> recreating volume $v from scratch"
  docker volume rm "$v" >/dev/null 2>&1 || true
  docker volume create "$v" >/dev/null
done

# --- restore the Mongo dump into a throwaway mongod -------------------------
echo "==> booting a throwaway mongod against $MONGO_VOL to load the dump"
MONGO_CID=$(docker run -d --rm \
  -e MONGO_INITDB_ROOT_USERNAME="$MONGO_ROOT_USER" \
  -e MONGO_INITDB_ROOT_PASSWORD="$MONGO_ROOT_PASS" \
  -v "$MONGO_VOL":/data/db \
  -v "$WORK":/seed:ro \
  mongo:7.0)

mongo_down() { docker stop "$MONGO_CID" >/dev/null 2>&1 || true; }
trap 'mongo_down; cleanup' EXIT

echo "==> waiting for the throwaway mongod to accept connections"
for i in $(seq 1 60); do
  if docker exec "$MONGO_CID" mongosh --quiet \
       -u "$MONGO_ROOT_USER" -p "$MONGO_ROOT_PASS" --authenticationDatabase admin \
       --eval 'db.adminCommand({ping:1})' >/dev/null 2>&1; then
    break
  fi
  sleep 1
  [ "$i" -lt 60 ] || { echo "ERROR: throwaway mongod never became ready" >&2; exit 1; }
done

echo "==> (re)creating the '$MONGO_APP_USER' app user on admin (not in the dump)"
docker exec "$MONGO_CID" mongosh --quiet \
  -u "$MONGO_ROOT_USER" -p "$MONGO_ROOT_PASS" --authenticationDatabase admin \
  --eval "db.getSiblingDB('admin').createUser({user:'$MONGO_APP_USER',pwd:'$MONGO_APP_PASS',roles:[{role:'dbOwner',db:'unifi'},{role:'dbOwner',db:'unifi_stat'},{role:'dbOwner',db:'unifi_audit'}]})" \
  >/dev/null 2>&1 || echo "    (app user already present — continuing)"

echo "==> mongorestore (--archive --gzip)"
docker exec "$MONGO_CID" sh -c \
  "mongorestore --username '$MONGO_ROOT_USER' --password '$MONGO_ROOT_PASS' \
     --authenticationDatabase admin --gzip --archive=/seed/mongo.archive.gz --drop"

echo "==> shutting the throwaway mongod down (volume now seeded)"
mongo_down
trap cleanup EXIT

# --- restore /config --------------------------------------------------------
echo "==> untarring config into $CONFIG_VOL (perms preserved)"
docker run --rm \
  -v "$WORK":/seed:ro \
  -v "$CONFIG_VOL":/config \
  alpine:3 \
  sh -c "tar -xpzf /seed/config.tgz -C /config"

echo "==> restore complete"
