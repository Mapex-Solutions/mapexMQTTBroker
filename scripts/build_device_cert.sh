#!/bin/sh
###############################################################################
# build_device_cert.sh — generate a CA-signed client cert for one device.
#
# Used only when mTLS is enabled on the broker (TLS_CA_FILE +
# TLS_REQUIRE_CLIENT_CERT=true). The CN matches the MQTT username
# convention `{orgId}:{assetUUID}` so a future audit log of cert
# lineage maps cleanly to platform identities.
#
# Note: the broker keeps `use_identity_as_username false`, so the cert
# CN is NOT used as the MQTT username — that still comes from the
# device's CONNECT packet and is validated by the plugin's HTTP
# callout. The cert is transport-layer authentication only.
#
# Run with the device identity as the only argument:
#
#   ./scripts/build_device_cert.sh "org-1:asset-aaa"
#
# Output (under $DEVICE_OUT_DIR):
#   <safe-name>.key   private key (ship to device, KEEP SECRET)
#   <safe-name>.crt   certificate (ship to device)
#
# The device also needs the CA cert ($CA_DIR/ca.crt) to verify the
# broker — bundle all three into the firmware.
#
# Defaults (override via env):
#   CA_DIR            ./certs
#   DEVICE_OUT_DIR    ./certs/devices
#   DEVICE_DAYS       1825 (5 years — devices in field, hard to rotate)
#   DEVICE_KEY_BITS   2048
###############################################################################
set -eu

CA_DIR="${CA_DIR:-./certs}"
DEVICE_OUT_DIR="${DEVICE_OUT_DIR:-./certs/devices}"
DEVICE_DAYS="${DEVICE_DAYS:-1825}"
DEVICE_KEY_BITS="${DEVICE_KEY_BITS:-2048}"

DEVICE_CN="${1:-${DEVICE_CN:-}}"
if [ -z "$DEVICE_CN" ]; then
    echo "Usage: $0 <device-cn>" >&2
    echo "" >&2
    echo "Example:" >&2
    echo "  $0 'org-1:asset-aaa'" >&2
    echo "" >&2
    echo "The CN should match the MQTT username convention" >&2
    echo "({orgId}:{assetUUID}) so cert audit logs align with" >&2
    echo "platform identities." >&2
    exit 1
fi

if ! command -v openssl >/dev/null 2>&1; then
    echo "openssl not found. Install with: sudo apt-get install -y openssl" >&2
    exit 1
fi

if [ ! -f "$CA_DIR/ca.key" ] || [ ! -f "$CA_DIR/ca.crt" ]; then
    echo "CA not found in $CA_DIR. Run scripts/build_ca.sh first." >&2
    exit 1
fi

# Filename-safe rendering of the CN (replace `:` and `/` with `_`).
SAFE_NAME=$(printf '%s' "$DEVICE_CN" | tr '/:' '__')
mkdir -p "$DEVICE_OUT_DIR"

DEVICE_KEY="$DEVICE_OUT_DIR/$SAFE_NAME.key"
DEVICE_CRT="$DEVICE_OUT_DIR/$SAFE_NAME.crt"

if [ -f "$DEVICE_KEY" ] || [ -f "$DEVICE_CRT" ]; then
    echo "Device cert already exists for $DEVICE_CN at $DEVICE_OUT_DIR/$SAFE_NAME.* — delete first." >&2
    exit 1
fi

# Step 1: device key.
openssl genrsa -out "$DEVICE_KEY" "$DEVICE_KEY_BITS" 2>/dev/null
chmod 400 "$DEVICE_KEY"

# Step 2: CSR with the device CN.
openssl req -new \
    -key "$DEVICE_KEY" \
    -subj "/CN=$DEVICE_CN" \
    -out "$DEVICE_OUT_DIR/$SAFE_NAME.csr"

# Step 3: sign with the CA, add clientAuth EKU so TLS clients can use
# this cert to authenticate (vs serverAuth for the broker cert).
EXT_FILE=$(mktemp)
trap 'rm -f "$EXT_FILE"' EXIT
cat > "$EXT_FILE" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid,issuer
EOF

openssl x509 -req \
    -in "$DEVICE_OUT_DIR/$SAFE_NAME.csr" \
    -CA "$CA_DIR/ca.crt" \
    -CAkey "$CA_DIR/ca.key" \
    -CAcreateserial \
    -days "$DEVICE_DAYS" \
    -sha256 \
    -extfile "$EXT_FILE" \
    -out "$DEVICE_CRT" 2>/dev/null

rm -f "$DEVICE_OUT_DIR/$SAFE_NAME.csr"

echo "✓ Device cert generated for $DEVICE_CN"
echo "  Private key: $DEVICE_KEY  (ship to device, KEEP SECRET)"
echo "  Certificate: $DEVICE_CRT  (ship to device)"
echo "  CA bundle:   $CA_DIR/ca.crt        (ship to device for server verification)"
echo "  Validity:    $DEVICE_DAYS days"
echo "  CN:          $DEVICE_CN"
echo "  Serial:      $(openssl x509 -in "$DEVICE_CRT" -noout -serial | sed 's/^serial=//')"
