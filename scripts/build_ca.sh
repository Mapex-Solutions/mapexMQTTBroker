#!/bin/sh
###############################################################################
# build_ca.sh — generate the Mapex Root CA (private key + self-signed cert).
#
# The CA signs both the broker's server cert and every device cert. It
# is the trust anchor — devices ship the CA cert and verify the broker
# against it; the broker (when mTLS is enabled) verifies devices
# against the same CA.
#
# Run ONCE per environment. Re-running is refused so an accidental
# overwrite cannot invalidate every existing device cert.
#
# Output:
#   $CA_DIR/ca.key   private key — KEEP SECRET, never leaves this host
#   $CA_DIR/ca.crt   public cert — distributed to brokers + devices
#
# Defaults (override via env):
#   CA_DIR        ./certs
#   CA_DAYS       3650 (10 years)
#   CA_KEY_BITS   4096
#   CA_CN         "Mapex MQTT Root CA"
#   CA_ORG        "Mapex Solutions"
###############################################################################
set -eu

CA_DIR="${CA_DIR:-./certs}"
CA_DAYS="${CA_DAYS:-3650}"
CA_KEY_BITS="${CA_KEY_BITS:-4096}"
CA_CN="${CA_CN:-Mapex MQTT Root CA}"
CA_ORG="${CA_ORG:-Mapex Solutions}"

if ! command -v openssl >/dev/null 2>&1; then
    echo "openssl not found. Install with: sudo apt-get install -y openssl" >&2
    exit 1
fi

mkdir -p "$CA_DIR"

if [ -f "$CA_DIR/ca.key" ] || [ -f "$CA_DIR/ca.crt" ]; then
    echo "Refusing to overwrite existing CA at $CA_DIR — delete the files first." >&2
    exit 1
fi

# Step 1: generate the CA private key.
openssl genrsa -out "$CA_DIR/ca.key" "$CA_KEY_BITS" 2>/dev/null
chmod 400 "$CA_DIR/ca.key"

# Step 2: self-sign the CA certificate with the right extensions.
# basicConstraints=CA:TRUE turns this into a CA, keyUsage limits what
# the key can sign (only certs + CRLs).
openssl req -x509 -new -nodes -sha256 \
    -key "$CA_DIR/ca.key" \
    -days "$CA_DAYS" \
    -subj "/CN=$CA_CN/O=$CA_ORG" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -addext "subjectKeyIdentifier=hash" \
    -out "$CA_DIR/ca.crt"

echo "✓ CA generated"
echo "  Private key: $CA_DIR/ca.key  (mode 400, KEEP SECRET)"
echo "  Certificate: $CA_DIR/ca.crt   (distribute to brokers + devices)"
echo "  Validity:    $CA_DAYS days"
echo "  Subject:     $(openssl x509 -in "$CA_DIR/ca.crt" -noout -subject | sed 's/^subject=//')"
echo "  Fingerprint: $(openssl x509 -in "$CA_DIR/ca.crt" -noout -fingerprint -sha256 | sed 's/^.*=//')"
