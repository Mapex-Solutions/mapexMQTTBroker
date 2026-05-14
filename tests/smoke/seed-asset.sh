#!/bin/bash
# =============================================================================
# Seed an AuthEntry into the broker's L2 (MinIO) read model.
#
# Mirrors what the Assets MS would write on every CRUD that touches the
# asset MQTT auth state. Use this to set up a smoke scenario without
# having the real Assets MS running.
#
# Usage:
#   ./seed-asset.sh <assetUUID> <orgId> <plaintextPassword> [enabled=true]
#
# Example:
#   ./seed-asset.sh asset-aaa org-1 secret
#   ./seed-asset.sh asset-bbb org-2 hunter2 false   # asset disabled
#
# After seeding:
#   docker compose logs broker      # broker has no L1 yet → L2 hit on first connect
#   docker exec mapex-smoke-tools-mc mosquitto_sub \\
#     -h broker -p 1883 -u 'org-1:asset-aaa' -P secret -t '#' -v
# =============================================================================
set -euo pipefail

ASSET_UUID="${1:?usage: $0 <assetUUID> <orgId> <plaintextPassword> [enabled=true]}"
ORG_ID="${2:?usage: $0 <assetUUID> <orgId> <plaintextPassword> [enabled=true]}"
PASSWORD="${3:?usage: $0 <assetUUID> <orgId> <plaintextPassword> [enabled=true]}"
ENABLED="${4:-true}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# bcrypt-hash the password using the broker image (it has Go + golang.org/x/crypto bundled).
# We use a tiny inline Go script via the toolbox container's apk packages instead so the
# operator doesn't need anything on the host. The toolbox doesn't have Go though, so we
# call the broker image's host plain `htpasswd` from mosquitto-passwd format → broker
# uses bcrypt on the AuthEntry.PasswordHash field directly.
#
# Simplest path: run a one-shot Python container with bcrypt to compute the hash.
HASH=$(docker run --rm python:3.12-alpine sh -c "
    pip install --quiet bcrypt 2>/dev/null
    python -c \"import bcrypt; print(bcrypt.hashpw(b'$PASSWORD', bcrypt.gensalt(rounds=10)).decode())\"
" 2>/dev/null)

case "$HASH" in
    '$2'*) ;;
    *)
        echo "ERROR: failed to compute bcrypt hash (got: '${HASH:0:20}...')" >&2
        exit 1
        ;;
esac

TMPDIR_LOCAL="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_LOCAL"' EXIT
LOCAL_FILE="${TMPDIR_LOCAL}/${ASSET_UUID}.json"
cat > "${LOCAL_FILE}" <<EOF
{
  "enabled": ${ENABLED},
  "assetUUID": "${ASSET_UUID}",
  "orgId": "${ORG_ID}",
  "passwordHash": "${HASH}"
}
EOF

echo "Seeding L2 entry for assetUUID=${ASSET_UUID} orgId=${ORG_ID} enabled=${ENABLED}"
docker cp "${LOCAL_FILE}" "mapex-smoke-tools-mc:/tmp/${ASSET_UUID}.json"
docker exec mapex-smoke-tools-mc mc cp "/tmp/${ASSET_UUID}.json" "mapex/mapex-mqtt-auth/${ASSET_UUID}.json"

echo ""
echo "Verifying:"
docker exec mapex-smoke-tools-mc mc ls "mapex/mapex-mqtt-auth/" | grep "${ASSET_UUID}.json" || true
echo ""
echo "Done. Try a CONNECT next:"
echo "  docker exec -it mapex-smoke-tools-mc mosquitto_sub -h broker -p 1883 \\"
echo "    -u '${ORG_ID}:${ASSET_UUID}' -P '${PASSWORD}' -t '#' -v -d"
