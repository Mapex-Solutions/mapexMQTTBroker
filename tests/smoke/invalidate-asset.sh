#!/bin/bash
# =============================================================================
# Publish a fanout invalidate event so the broker drops its L1 entry.
#
# Mirrors what the Assets MS publishes when an asset is updated (e.g.
# password rotation or cert revocation). After this fires, the broker
# logs "auth_store: invalidated L1 ... next_read=L2" and the next
# CONNECT for that asset goes back through L2.
#
# Usage:
#   ./invalidate-asset.sh <assetUUID>
#
# Example:
#   ./invalidate-asset.sh asset-aaa
# =============================================================================
set -euo pipefail

ASSET_UUID="${1:?usage: $0 <assetUUID>}"
SUBJECT="${SUBJECT:-mapexos.fanout.asset.invalidate}"

PAYLOAD="{\"assetUUID\":\"${ASSET_UUID}\"}"

echo "Publishing FANOUT subject=${SUBJECT} payload=${PAYLOAD}"
docker exec mapex-smoke-tools-nats nats --server nats://nats:4222 pub "${SUBJECT}" "${PAYLOAD}"

echo ""
echo "Tail the broker to confirm L1 invalidation:"
echo "  docker compose logs --tail 20 broker"
