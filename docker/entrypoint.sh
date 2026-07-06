#!/bin/sh
###############################################################################
# entrypoint.sh — Mapex MQTT Broker container startup
#
# Renders /mosquitto/config/mosquitto.conf.template into mosquitto.conf
# by substituting environment variables, optionally appends a TLS
# listener block, then execs mosquitto. Defaults are applied here so
# a bare-bones run with only INTERNAL_API_KEY + NATS_URL boots
# correctly on the plaintext listener.
#
# Required env:
#   INTERNAL_API_KEY               internal API key the assets MS expects
#   NATS_URL                       NATS server URL (e.g. nats://nats:4222)
#
# Optional env (defaults below):
#   MQTT_LISTENER_PORT             1883
#   MQTT_MAX_CONNECTIONS           -1 (unlimited)
#   ASSETS_HOST                    assets
#   ASSETS_PORT                    5002
#   NATS_SUBJECT_PRESENCE          dev.mapexos.presence.advisory
#   NATS_SUBJECT_INGRESS_PREFIX    dev.mapexos.mqtt.data
#   AUTH_TIMEOUT_SECONDS           5
#   PLUGIN_WORKER_POOL_SIZE        4
#   PLUGIN_BUFFER_SIZE             10000
#
# TLS listener (optional, defaults to disabled):
#   TLS_ENABLED                    false       set "true" to enable 8883
#   MQTT_TLS_LISTENER_PORT         8883
#   TLS_CERT_FILE                  /mosquitto/certs/server.crt
#   TLS_KEY_FILE                   /mosquitto/certs/server.key
#   TLS_CA_FILE                    ""          set to a CA path to enable mTLS
#   TLS_REQUIRE_CLIENT_CERT        false       only meaningful with TLS_CA_FILE
#   TLS_MIN_VERSION                tlsv1.2
###############################################################################
set -eu

# Required vars — fail fast if missing.
: "${INTERNAL_API_KEY:?INTERNAL_API_KEY is required}"
: "${NATS_URL:?NATS_URL is required}"

# Plain-listener defaults.
export MQTT_LISTENER_PORT="${MQTT_LISTENER_PORT:-1883}"
export MQTT_MAX_CONNECTIONS="${MQTT_MAX_CONNECTIONS:--1}"
export ASSETS_HOST="${ASSETS_HOST:-assets}"
export ASSETS_PORT="${ASSETS_PORT:-5002}"
export NATS_SUBJECT_PRESENCE="${NATS_SUBJECT_PRESENCE:-dev.mapexos.presence.advisory}"
export NATS_SUBJECT_INGRESS_PREFIX="${NATS_SUBJECT_INGRESS_PREFIX:-dev.mapexos.mqtt.data}"
export AUTH_TIMEOUT_SECONDS="${AUTH_TIMEOUT_SECONDS:-5}"
export PLUGIN_WORKER_POOL_SIZE="${PLUGIN_WORKER_POOL_SIZE:-4}"
export PLUGIN_BUFFER_SIZE="${PLUGIN_BUFFER_SIZE:-10000}"

# TieredStore (auth read model) — L1 NVMe Pebble + L2 MinIO + L3
# HTTP fallback. Empty CACHE_L1_PATH disables L1; empty CACHE_L2_*
# disables L2; the plugin always retains L3 (HTTP) as last resort.
export CACHE_L1_PATH="${CACHE_L1_PATH:-/var/cache/mqtt}"
export CACHE_L1_TTL_MINUTES="${CACHE_L1_TTL_MINUTES:-30}"
export OBJECT_STORE_ENDPOINT="${OBJECT_STORE_ENDPOINT:-}"
export OBJECT_STORE_ACCESS_KEY="${OBJECT_STORE_ACCESS_KEY:-}"
export OBJECT_STORE_SECRET_KEY="${OBJECT_STORE_SECRET_KEY:-}"
export OBJECT_STORE_USE_SSL="${OBJECT_STORE_USE_SSL:-false}"
export OBJECT_STORE_AUTH_IS_NEEDED="${OBJECT_STORE_AUTH_IS_NEEDED:-true}"
# CACHE_L2_BUCKET is intentionally NOT exposed — the bucket name is
# fixed by the platform contract with the assets MS (mapex-assets,
# key `{orgId}/{assetUUID}.json`). The plugin hardcodes it; operators
# only configure endpoint + credentials.

# FANOUT consumer subject for L1 invalidation. Operator overrides
# per env if multiple deploys share a NATS cluster.
export FANOUT_INVALIDATE_SUBJECT="${FANOUT_INVALIDATE_SUBJECT:-mapexos.fanout.asset.invalidate}"

# TLS defaults — emitted into the rendered config only when enabled.
# Auto-detect: if TLS_ENABLED is unset AND the server cert + key are
# present at the default path, flip TLS on automatically. Operator may
# force-disable by setting TLS_ENABLED=false explicitly.
MQTT_TLS_LISTENER_PORT="${MQTT_TLS_LISTENER_PORT:-8883}"
TLS_CERT_FILE="${TLS_CERT_FILE:-/mosquitto/certs/server.crt}"
TLS_KEY_FILE="${TLS_KEY_FILE:-/mosquitto/certs/server.key}"
TLS_CA_FILE="${TLS_CA_FILE:-}"
TLS_REQUIRE_CLIENT_CERT="${TLS_REQUIRE_CLIENT_CERT:-true}"
TLS_MIN_VERSION="${TLS_MIN_VERSION:-tlsv1.2}"

# Auto-detect when TLS_ENABLED is unset.
if [ -z "${TLS_ENABLED:-}" ]; then
    if [ -f "$TLS_CERT_FILE" ] && [ -f "$TLS_KEY_FILE" ]; then
        TLS_ENABLED=true
        echo "[ENTRYPOINT] TLS auto-detected: server.crt + server.key present, enabling listener ${MQTT_TLS_LISTENER_PORT}"
        # If ca.pem (or ca-chain.pem) is present, default to mTLS too.
        if [ -z "$TLS_CA_FILE" ]; then
            if [ -f /mosquitto/certs/ca.pem ]; then
                TLS_CA_FILE=/mosquitto/certs/ca.pem
            elif [ -f /mosquitto/certs/ca-chain.pem ]; then
                TLS_CA_FILE=/mosquitto/certs/ca-chain.pem
            fi
            if [ -n "$TLS_CA_FILE" ]; then
                echo "[ENTRYPOINT] mTLS auto-detected: $TLS_CA_FILE present, require_certificate=${TLS_REQUIRE_CLIENT_CERT}"
            fi
        fi
    else
        TLS_ENABLED=false
    fi
fi

TEMPLATE=/mosquitto/config/mosquitto.conf.template
RENDERED=/mosquitto/config/mosquitto.conf

if [ ! -f "$TEMPLATE" ]; then
    echo "[ENTRYPOINT] template not found: $TEMPLATE" >&2
    exit 1
fi

# envsubst limited to the variables we manage so unrelated $VAR strings
# in topic/subject defaults don't get accidentally expanded.
envsubst '
    ${MQTT_LISTENER_PORT}
    ${MQTT_MAX_CONNECTIONS}
    ${ASSETS_HOST}
    ${ASSETS_PORT}
    ${INTERNAL_API_KEY}
    ${NATS_URL}
    ${NATS_SUBJECT_PRESENCE}
    ${NATS_SUBJECT_INGRESS_PREFIX}
    ${AUTH_TIMEOUT_SECONDS}
    ${PLUGIN_WORKER_POOL_SIZE}
    ${PLUGIN_BUFFER_SIZE}
    ${CACHE_L1_PATH}
    ${CACHE_L1_TTL_MINUTES}
    ${OBJECT_STORE_ENDPOINT}
    ${OBJECT_STORE_ACCESS_KEY}
    ${OBJECT_STORE_SECRET_KEY}
    ${OBJECT_STORE_USE_SSL}
    ${OBJECT_STORE_AUTH_IS_NEEDED}
    ${FANOUT_INVALIDATE_SUBJECT}
' < "$TEMPLATE" > "$RENDERED"

# Append the TLS listener block when enabled. Doing this in the
# entrypoint (rather than the template) avoids the need for
# conditional rendering — envsubst has no conditionals — and keeps
# the rendered config readable when TLS is off.
if [ "$TLS_ENABLED" = "true" ]; then
    if [ ! -f "$TLS_CERT_FILE" ]; then
        echo "[ENTRYPOINT] TLS_ENABLED=true but TLS_CERT_FILE not found: $TLS_CERT_FILE" >&2
        exit 1
    fi
    if [ ! -f "$TLS_KEY_FILE" ]; then
        echo "[ENTRYPOINT] TLS_ENABLED=true but TLS_KEY_FILE not found: $TLS_KEY_FILE" >&2
        exit 1
    fi

    {
        echo ""
        echo "###############################################################################"
        echo "# TLS listener — appended by entrypoint when TLS_ENABLED=true"
        echo "###############################################################################"
        echo "listener ${MQTT_TLS_LISTENER_PORT}"
        echo "certfile ${TLS_CERT_FILE}"
        echo "keyfile ${TLS_KEY_FILE}"
        echo "tls_version ${TLS_MIN_VERSION}"
    } >> "$RENDERED"

    if [ -n "$TLS_CA_FILE" ]; then
        if [ ! -f "$TLS_CA_FILE" ]; then
            echo "[ENTRYPOINT] TLS_CA_FILE set but file not found: $TLS_CA_FILE" >&2
            exit 1
        fi
        {
            echo "cafile ${TLS_CA_FILE}"
            echo "require_certificate ${TLS_REQUIRE_CLIENT_CERT}"
            # use_identity_as_username=false keeps the MQTT username
            # field as the auth identity. The cert is transport-layer
            # authentication; our plugin still validates username+pwd
            # against the assets MS — belt and suspenders.
            echo "use_identity_as_username false"
        } >> "$RENDERED"
    fi
    echo "[ENTRYPOINT] TLS listener appended: port=${MQTT_TLS_LISTENER_PORT} cert=${TLS_CERT_FILE} mtls=$([ -n "$TLS_CA_FILE" ] && echo true || echo false)"
fi

# Conditionally append the L2 (MinIO) plugin_opts. Mosquitto 2.0.x
# rejects empty plugin_opt values, so the L2 stanza only lands when
# the operator set the endpoint.
if [ -n "$OBJECT_STORE_ENDPOINT" ]; then
    {
        echo ""
        echo "# TieredStore L2 (object store) — appended by entrypoint when OBJECT_STORE_ENDPOINT is set."
        echo "# Bucket name is fixed by the platform contract; operators only configure endpoint + credentials."
        echo "plugin_opt_object_store_endpoint         ${OBJECT_STORE_ENDPOINT}"
        [ -n "$OBJECT_STORE_ACCESS_KEY" ] && echo "plugin_opt_object_store_access_key       ${OBJECT_STORE_ACCESS_KEY}"
        [ -n "$OBJECT_STORE_SECRET_KEY" ] && echo "plugin_opt_object_store_secret_key       ${OBJECT_STORE_SECRET_KEY}"
        echo "plugin_opt_object_store_use_ssl          ${OBJECT_STORE_USE_SSL}"
        echo "plugin_opt_object_store_auth_is_needed   ${OBJECT_STORE_AUTH_IS_NEEDED}"
    } >> "$RENDERED"
    echo "[ENTRYPOINT] L2 object store appended: endpoint=${OBJECT_STORE_ENDPOINT}"
else
    echo "[ENTRYPOINT] L2 object store disabled (OBJECT_STORE_ENDPOINT empty); plugin uses L1 + L3 only"
fi

# Sanity check: complain loudly if any unsubstituted ${VAR} survived
# in a non-comment line. Comment lines are allowed to mention ${VAR}
# names (the template documentation references them inline).
if grep -v '^[[:space:]]*#' "$RENDERED" | grep -q '\${'; then
    echo "[ENTRYPOINT] unsubstituted variable in rendered config:" >&2
    grep -nv '^[[:space:]]*#' "$RENDERED" | grep '\${' >&2 || true
    exit 1
fi

echo "[ENTRYPOINT] config rendered: listener=${MQTT_LISTENER_PORT} nats=${NATS_URL} assets=${ASSETS_HOST}:${ASSETS_PORT}"
echo "[ENTRYPOINT] subjects: presence=${NATS_SUBJECT_PRESENCE} ingress_prefix=${NATS_SUBJECT_INGRESS_PREFIX}"

exec /usr/sbin/mosquitto -c "$RENDERED"
