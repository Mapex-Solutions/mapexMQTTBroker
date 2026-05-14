#!/bin/sh
###############################################################################
# build_server_cert.sh — generate a CA-signed server cert for the broker.
#
# Run after build_ca.sh. Produces a key + cert pair the broker mounts
# at /mosquitto/certs/server.{key,crt}. Devices verify the broker
# against the CA cert (ca.crt) and trust this server cert because it
# is signed by the CA.
#
# The cert's CN and SAN list MUST contain every hostname/IP that
# devices will use to reach the broker. Mismatched SAN = TLS hostname
# verification failure on the device side.
#
# Output:
#   $CA_DIR/server.key   private key (mount into broker container)
#   $CA_DIR/server.crt   certificate (mount into broker container)
#
# Defaults (override via env):
#   CA_DIR            ./certs
#   SERVER_DAYS       730 (2 years)
#   SERVER_KEY_BITS   2048
#   SERVER_HOST       localhost
#   SERVER_SAN        DNS:localhost,IP:127.0.0.1
###############################################################################
set -eu

CA_DIR="${CA_DIR:-./certs}"
SERVER_DAYS="${SERVER_DAYS:-730}"
SERVER_KEY_BITS="${SERVER_KEY_BITS:-2048}"
SERVER_HOST="${SERVER_HOST:-localhost}"
SERVER_SAN="${SERVER_SAN:-DNS:$SERVER_HOST,IP:127.0.0.1}"

if ! command -v openssl >/dev/null 2>&1; then
    echo "openssl not found. Install with: sudo apt-get install -y openssl" >&2
    exit 1
fi

if [ ! -f "$CA_DIR/ca.key" ] || [ ! -f "$CA_DIR/ca.crt" ]; then
    echo "CA not found in $CA_DIR. Run scripts/build_ca.sh first." >&2
    exit 1
fi

# Step 1: generate the server key.
openssl genrsa -out "$CA_DIR/server.key" "$SERVER_KEY_BITS" 2>/dev/null
chmod 400 "$CA_DIR/server.key"

# Step 2: build a CSR with the SAN list — required by modern TLS
# clients (Chrome, Go's tls package, etc. ignore the CN).
openssl req -new \
    -key "$CA_DIR/server.key" \
    -subj "/CN=$SERVER_HOST" \
    -addext "subjectAltName=$SERVER_SAN" \
    -out "$CA_DIR/server.csr"

# Step 3: sign the CSR with the CA. Extensions must be in a config
# file because `openssl x509 -req` does not honour -addext on
# openssl 3.x.
EXT_FILE=$(mktemp)
trap 'rm -f "$EXT_FILE"' EXIT
cat > "$EXT_FILE" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid,issuer
subjectAltName=$SERVER_SAN
EOF

openssl x509 -req \
    -in "$CA_DIR/server.csr" \
    -CA "$CA_DIR/ca.crt" \
    -CAkey "$CA_DIR/ca.key" \
    -CAcreateserial \
    -days "$SERVER_DAYS" \
    -sha256 \
    -extfile "$EXT_FILE" \
    -out "$CA_DIR/server.crt" 2>/dev/null

rm -f "$CA_DIR/server.csr"

echo "✓ Server cert generated"
echo "  Private key: $CA_DIR/server.key  (mount at /mosquitto/certs/server.key)"
echo "  Certificate: $CA_DIR/server.crt   (mount at /mosquitto/certs/server.crt)"
echo "  Validity:    $SERVER_DAYS days"
echo "  CN:          $SERVER_HOST"
echo "  SAN:         $SERVER_SAN"
echo "  Issued by:   $(openssl x509 -in "$CA_DIR/server.crt" -noout -issuer | sed 's/^issuer=//')"
