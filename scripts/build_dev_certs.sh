#!/bin/sh
###############################################################################
# build_dev_certs.sh — one-shot dev environment cert setup.
#
# Generates a complete cert chain in ./certs/ ready to mount into a
# locally-running broker. Useful for smoke tests and CI; NOT for
# production (uses default CN=localhost and shorter validities are
# fine because the dev CA never reaches a real device).
#
# Produces:
#   certs/ca.{key,crt}                root CA (10 years)
#   certs/server.{key,crt}            broker cert for localhost (90 days)
#   certs/devices/org-1__asset-aaa.{key,crt}    sample device cert (90 days)
#
# Usage:
#   ./scripts/build_dev_certs.sh
###############################################################################
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"

# Short validities so accidental commits of dev certs don't linger
# usable for years. Real CA / server / device certs use the dedicated
# scripts directly with their longer defaults.
export CA_DAYS=3650
export SERVER_DAYS=90
export DEVICE_DAYS=90

# Refuse to overwrite an existing certs/ tree — the operator may
# have a real CA there and rebuilding it would invalidate every
# device.
if [ -d ./certs ]; then
    echo "Refusing to overwrite ./certs — delete it first if you really want to rebuild." >&2
    exit 1
fi

"$HERE/build_ca.sh"
"$HERE/build_server_cert.sh"
"$HERE/build_device_cert.sh" "org-1:asset-aaa"

echo ""
echo "Dev cert chain ready in ./certs/"
echo ""
echo "Mount into the broker (server cert + key + CA for mTLS):"
echo "  -v \"\$(pwd)/certs:/mosquitto/certs:ro\""
echo ""
echo "Use from a test mosquitto_pub client:"
echo "  mosquitto_pub --cafile certs/ca.crt --insecure \\"
echo "      -h localhost -p 8883 \\"
echo "      -u 'org-1:asset-aaa' -P 'good-pwd' \\"
echo "      -t 'events/org-1/asset-aaa/temperature' -m '{\"v\":1}'"
