#!/bin/bash
# =============================================================================
# Mapex MQTT Broker — smoke-test orchestrator
#
# Brings up the smoke stack, seeds one asset entry, runs a CONNECT, and
# tails the broker logs so you can SEE the L1/L2/L3 cascade.
#
# This is a developer/ops tool — not a CI assertion. It prints what
# happened so you can eyeball it. Use `docker compose down -v` when
# you're done.
#
# Usage:
#   ./run.sh                              # default test asset (org-1:asset-aaa / secret)
#   ASSET_UUID=asset-x ORG_ID=org-2 PASSWORD=hunter2 ./run.sh
#   BROKER_IMAGE=docker.io/mapexos/mapex-broker-mqtt:0.1.0 ./run.sh
#
# Environment overrides:
#   ASSET_UUID            default: asset-aaa
#   ORG_ID                default: org-1
#   PASSWORD              default: secret
#   BROKER_IMAGE          default: mapexos/mapex-broker-mqtt:dev
#   MINIO_ROOT_USER       default: mapex-smoke-admin
#   MINIO_ROOT_PASSWORD   default: mapex-smoke-password-1234
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

ASSET_UUID="${ASSET_UUID:-asset-aaa}"
ORG_ID="${ORG_ID:-org-1}"
PASSWORD="${PASSWORD:-secret}"

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

step() {
    echo ""
    echo -e "${CYAN}=== $* ===${NC}"
    echo ""
}

step "1. Bring up the stack"
docker compose up -d
echo "Waiting for broker to become healthy..."
for _ in $(seq 1 30); do
    if docker compose ps broker | grep -q "Up"; then
        sleep 2
        break
    fi
    sleep 1
done

step "2. Show broker startup logs (expect: L1 Pebble ready, L2 MinIO ready, fanout consumer started)"
docker compose logs broker | tail -20

step "3. Seed the L2 read model"
"./seed-asset.sh" "$ASSET_UUID" "$ORG_ID" "$PASSWORD"

step "4. First CONNECT — L1 is empty → expect L2 hit log"
docker exec mapex-smoke-tools-mqtt timeout 5 mosquitto_pub -h broker -p 1883 \
    -u "${ORG_ID}:${ASSET_UUID}" -P "${PASSWORD}" \
    -t "events/${ORG_ID}/${ASSET_UUID}" -m "smoke-test-1" 2>&1 || true
sleep 1
echo "Recent broker logs:"
docker compose logs --tail 5 broker | grep -E "auth_store|Authenticate" || docker compose logs --tail 8 broker

step "5. Second CONNECT — L1 is now warm → expect L1 hit log"
docker exec mapex-smoke-tools-mqtt timeout 5 mosquitto_pub -h broker -p 1883 \
    -u "${ORG_ID}:${ASSET_UUID}" -P "${PASSWORD}" \
    -t "events/${ORG_ID}/${ASSET_UUID}" -m "smoke-test-2" 2>&1 || true
sleep 1
docker compose logs --tail 5 broker | grep -E "auth_store|Authenticate" || docker compose logs --tail 8 broker

step "6. Invalidate via FANOUT — expect 'invalidated L1' log"
"./invalidate-asset.sh" "$ASSET_UUID"
sleep 1
docker compose logs --tail 5 broker | grep -E "fanout|invalidate" || docker compose logs --tail 8 broker

step "7. Third CONNECT — L1 was invalidated → expect L2 hit again"
docker exec mapex-smoke-tools-mqtt timeout 5 mosquitto_pub -h broker -p 1883 \
    -u "${ORG_ID}:${ASSET_UUID}" -P "${PASSWORD}" \
    -t "events/${ORG_ID}/${ASSET_UUID}" -m "smoke-test-3" 2>&1 || true
sleep 1
docker compose logs --tail 5 broker | grep -E "auth_store|Authenticate" || docker compose logs --tail 8 broker

step "8. Cross-tenant deny — username claims org-WRONG, entry has ${ORG_ID} → expect AuthDeny"
docker exec mapex-smoke-tools-mqtt timeout 5 mosquitto_pub -h broker -p 1883 \
    -u "org-WRONG:${ASSET_UUID}" -P "${PASSWORD}" \
    -t "events/org-WRONG/${ASSET_UUID}" -m "should-fail" 2>&1 || true
sleep 1
docker compose logs --tail 5 broker | grep -E "Deny|denied|auth_store" || docker compose logs --tail 8 broker

echo ""
echo -e "${GREEN}Smoke run complete.${NC}"
echo ""
echo "Inspect further with:"
echo "  docker compose logs -f broker"
echo "  docker exec mapex-smoke-tools-mc mc ls mapex/mapex-mqtt-auth"
echo "  docker exec mapex-smoke-tools-nats nats --server nats://nats:4222 sub '>'"
echo ""
echo -e "${YELLOW}Cleanup when you're done:${NC}"
echo "  docker compose down -v"
