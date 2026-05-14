# Cert generation scripts

Scripts to build the TLS / mTLS cert chain for `mapex-broker-mqtt`.
Use these to bootstrap an internal CA for development and small-scale
deployments. For public-facing brokers, prefer Let's Encrypt for the
server cert and keep this CA only for the device chain.

## Prerequisites

```bash
sudo apt-get update
sudo apt-get install -y openssl
```

`openssl` is the only requirement. The scripts are POSIX `sh` (no
bash extensions), so they run on Debian, Ubuntu, Alpine, macOS — any
host with `/bin/sh` + `openssl`.

## Files

| Script | Purpose | Run when |
|---|---|---|
| `build_ca.sh` | Generate the root CA (private key + self-signed cert) | Once per environment |
| `build_server_cert.sh` | Generate a CA-signed cert for the broker | Once per broker hostname; rotate every ~2 years |
| `build_device_cert.sh` | Generate a CA-signed client cert for one device | Once per device — only used when mTLS is enabled |
| `build_dev_certs.sh` | One-shot: CA + server + sample device for local dev | When you need a complete dev chain in 5 seconds |

## Usage

### 1. Root CA (once per environment)

```bash
./scripts/build_ca.sh
```

Produces:
- `certs/ca.key` — **private key, KEEP SECRET, never leaves this host**
- `certs/ca.crt` — public cert, distribute to brokers + devices

Defaults:
- 4096-bit RSA
- **10 years** validity (`CA_DAYS=3650`)
- Subject: `CN=Mapex MQTT Root CA, O=Mapex Solutions`

Override:
```bash
CA_DAYS=1825 \
  CA_CN="MyOrg Root CA" \
  CA_ORG="MyOrg" \
  ./scripts/build_ca.sh
```

### 2. Server cert (per broker hostname)

```bash
./scripts/build_server_cert.sh
```

Defaults:
- 2048-bit RSA
- **2 years** validity (`SERVER_DAYS=730`)
- CN/SAN = `localhost` + `127.0.0.1` (override for production)

For a production broker:
```bash
SERVER_HOST=broker.example.com \
  SERVER_SAN="DNS:broker.example.com,DNS:mqtt.example.com" \
  ./scripts/build_server_cert.sh
```

Produces:
- `certs/server.key`, `certs/server.crt` — mount into the broker
  container at `/mosquitto/certs/`

### 3. Device cert (per IoT device)

```bash
./scripts/build_device_cert.sh "org-1:asset-aaa"
```

Defaults:
- 2048-bit RSA
- **5 years** validity (`DEVICE_DAYS=1825`)
- CN = the supplied identity (matches the MQTT username convention)

Produces (under `certs/devices/`):
- `org-1__asset-aaa.key`, `org-1__asset-aaa.crt` — embed in firmware
- Plus `certs/ca.crt` for server verification on the device side

### 4. Dev one-shot

```bash
./scripts/build_dev_certs.sh
```

Builds the complete chain in `./certs/` for a local broker on
`localhost`, with a sample `org-1:asset-aaa` device cert. 90-day
validity on the leaf certs so accidental commits of dev certs don't
linger usable.

## Key sizes & validity rationale

| Cert | Algorithm | Validity | Why |
|---|---|---|---|
| Root CA | RSA 4096 | 10 years | Long-lived, signs everything; security > performance |
| Server | RSA 2048 | 2 years | Rotated regularly; faster TLS handshake |
| Device | RSA 2048 | 5 years | Devices in field, hard to rotate; balance security + practicality |

For modern IoT consider switching device certs to **EC P-256**
(smaller keys, less battery, faster handshake — ESP32 / nRF52 in
particular benefit). To do that, replace the `genrsa` line in
`build_device_cert.sh`:

```sh
# RSA 2048 (default):
openssl genrsa -out "$DEVICE_KEY" "$DEVICE_KEY_BITS"

# EC P-256 alternative:
openssl ecparam -genkey -name prime256v1 -out "$DEVICE_KEY"
```

## Storage layout after running everything

```
certs/
├── ca.key                           private CA key — NEVER ships off the build host
├── ca.crt                           public CA cert — ships to brokers + devices
├── ca.srl                           CA serial counter (managed by openssl)
├── server.key                       broker private key — mounts into container
├── server.crt                       broker certificate — mounts into container
└── devices/
    ├── org-1__asset-aaa.key         device private key — ships to that one device
    ├── org-1__asset-aaa.crt         device cert
    ├── org-1__asset-bbb.key
    └── org-1__asset-bbb.crt
```

## Running the broker with these certs

Server-only TLS (no mTLS):
```bash
docker run --rm -p 8883:8883 \
  -v "$(pwd)/certs:/mosquitto/certs:ro" \
  -e INTERNAL_API_KEY=... \
  -e NATS_URL=... \
  -e ASSETS_HOST=... \
  -e ASSETS_PORT=... \
  -e TLS_ENABLED=true \
  docker.io/mapexos/mapex-broker-mqtt:dev
```

mTLS (devices must present a CA-signed client cert):
```bash
docker run --rm -p 8883:8883 \
  -v "$(pwd)/certs:/mosquitto/certs:ro" \
  -e INTERNAL_API_KEY=... \
  -e NATS_URL=... \
  -e ASSETS_HOST=... \
  -e ASSETS_PORT=... \
  -e TLS_ENABLED=true \
  -e TLS_CA_FILE=/mosquitto/certs/ca.crt \
  -e TLS_REQUIRE_CLIENT_CERT=true \
  docker.io/mapexos/mapex-broker-mqtt:dev
```

## Rotation

| Asset | Rotation cadence | How |
|---|---|---|
| Root CA | Every 8-10 years (before expiry) | Generate new CA with overlap period; cross-sign the new CA with the old CA so devices keep trusting |
| Server cert | Every 1-2 years | Re-run `build_server_cert.sh` and restart the broker — devices keep trusting because the CA is unchanged |
| Device cert | Per-device when expiring | Re-run `build_device_cert.sh "<cn>"` and OTA push to the device |

The CA private key (`ca.key`) is the most critical file in this set
— if it leaks, every device cert ever signed by it is compromised
and you must roll the whole chain. Store it offline (HSM, air-gapped
USB) for production deployments.

## What you don't get from these scripts

- **Cert revocation (CRL / OCSP)**: not generated. Mosquitto
  supports CRL via `crlfile` — extend the broker config + scripts if
  you need this. For most IoT deployments the rotation cadence above
  is the simpler answer.
- **PKI hierarchy**: single-tier (root CA signs everything directly).
  Two-tier (root → intermediate) is more typical for large
  deployments where the intermediate's private key lives online and
  the root's stays offline. Add an intermediate in
  `build_intermediate.sh` if you need it.
- **Automatic renewal**: not in scope. Wire `build_server_cert.sh`
  into a cron job + broker reload signal if you want unattended
  renewal.
