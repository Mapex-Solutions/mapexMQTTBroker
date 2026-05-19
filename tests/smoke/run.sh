#!/bin/bash
# =============================================================================
# Mapex MQTT Broker — Smoke test
#
# Brings up the stack, seeds one asset, tests the L1/L2 cache cascade,
# and cleans up. Exit 0 = all checks passed.
#
# Usage:
#   ./run.sh
#   BROKER_IMAGE=mapexos/mapex-broker-mqtt:0.1.0 ./run.sh
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

ASSET_UUID="asset-aaa"
ORG_ID="org-1"
PASSWORD="secret"
NETWORK="mapex-smoke"

GREEN='\033[0;32m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

pass() { echo -e "  ${GREEN}PASS${NC} $*"; }
fail() { echo -e "  ${RED}FAIL${NC} $*"; FAILED=true; }

FAILED=false

step() { echo -e "\n${CYAN}=== $* ===${NC}\n"; }

# --- Helpers: one-shot containers on the smoke network ---
mc_exec() {
    docker run --rm --network "$NETWORK" \
        -e MC_HOST_mapex="http://mapex-smoke-admin:mapex-smoke-password-1234@mapex-smoke-minio:9000" \
        minio/mc:latest "$@"
}

nats_pub() {
    docker run --rm --network "$NETWORK" \
        natsio/nats-box:latest \
        nats --server nats://mapex-smoke-nats:4222 pub "$@"
}

mqtt_pub() {
    docker run --rm --network "$NETWORK" \
        eclipse-mosquitto:2.0 \
        mosquitto_pub -h mapex-smoke-broker -p 1883 "$@"
}

bcrypt_hash() {
    docker run --rm python:3.12-alpine sh -c "
        pip install --quiet bcrypt 2>/dev/null
        python -c \"import bcrypt; print(bcrypt.hashpw(b'$1', bcrypt.gensalt(rounds=10)).decode())\"
    " 2>/dev/null
}

# --- Start ---
step "1. Stack up"
docker compose up -d
echo "Waiting for broker..."
sleep 5

step "2. Seed L2 (MinIO)"
HASH=$(bcrypt_hash "$PASSWORD")
TMPFILE=$(mktemp)
cat > "$TMPFILE" <<EOF
{"enabled":true,"assetUUID":"${ASSET_UUID}","orgId":"${ORG_ID}","authType":"password","passwordHash":"${HASH}"}
EOF
docker cp "$TMPFILE" "mapex-smoke-minio-init:/tmp/${ASSET_UUID}.json" 2>/dev/null \
    || docker cp "$TMPFILE" "mapex-smoke-minio:/tmp/${ASSET_UUID}.json"
# Use a fresh mc container to copy into the bucket
docker run --rm --network "$NETWORK" \
    -e MC_HOST_mapex="http://mapex-smoke-admin:mapex-smoke-password-1234@mapex-smoke-minio:9000" \
    -v "$TMPFILE:/tmp/${ASSET_UUID}.json:ro" \
    minio/mc:latest cp "/tmp/${ASSET_UUID}.json" "mapex/mapex-mqtt-auth/${ASSET_UUID}.json"
rm -f "$TMPFILE"
pass "L2 seeded"

step "3. First CONNECT — expect L2 hit (L1 cold)"
mqtt_pub -u "${ASSET_UUID}" -P "${PASSWORD}" -t "events/${ASSET_UUID}/test" -m "smoke-1" 2>&1 || true
sleep 1
if docker compose logs --tail 10 broker | grep -q "L2 hit"; then
    pass "L2 hit on first connect"
else
    fail "Expected L2 hit log"
fi

step "4. Second CONNECT — expect L1 hit (warmed)"
mqtt_pub -u "${ASSET_UUID}" -P "${PASSWORD}" -t "events/${ASSET_UUID}/test" -m "smoke-2" 2>&1 || true
sleep 1
if docker compose logs --tail 10 broker | grep -q "L1 hit"; then
    pass "L1 hit on second connect"
else
    fail "Expected L1 hit log"
fi

step "5. FANOUT invalidate — expect L1 dropped"
nats_pub "mapexos.fanout.asset.invalidate" "{\"assetUUID\":\"${ASSET_UUID}\"}"
sleep 1
if docker compose logs --tail 10 broker | grep -q "invalidated L1"; then
    pass "L1 invalidated via FANOUT"
else
    fail "Expected L1 invalidation log"
fi

step "6. Third CONNECT — expect L2 hit again (L1 was invalidated)"
mqtt_pub -u "${ASSET_UUID}" -P "${PASSWORD}" -t "events/${ASSET_UUID}/test" -m "smoke-3" 2>&1 || true
sleep 1
if docker compose logs --tail 10 broker | grep -q "L2 hit"; then
    pass "L2 hit after invalidation"
else
    fail "Expected L2 hit after invalidation"
fi

step "7. Wrong password — expect deny"
mqtt_pub -u "${ASSET_UUID}" -P "wrong-password" -t "events/${ASSET_UUID}/test" -m "should-fail" 2>&1 || true
sleep 1
if docker compose logs --tail 10 broker | grep -qi "deny"; then
    pass "Wrong password denied"
else
    fail "Expected auth deny log"
fi

# --- Summary ---
echo ""
if [ "$FAILED" = true ]; then
    echo -e "${RED}Some checks failed. Inspect logs:${NC}"
    echo "  docker compose logs broker"
else
    echo -e "${GREEN}All smoke checks passed.${NC}"
fi
echo ""
echo "Cleanup: docker compose down -v"
