# Mapex MQTT Broker Deploy

Operator runbook for bringing the broker up with TLS/mTLS enabled.

## Prerequisites

1. Run the platform PKI pre-build from the mapexOS root:
   ```bash
   ./scripts/prebuild/pki/generate-pki.sh
   ```
2. Confirm output under `scripts/prebuild/pki/output/{ca,broker}/`.

## Deploy steps

Working dir: `mapexOS/deployment/docker-compose/services_required/`.

1. Copy broker materials to the host bind dir:
   ```bash
   mkdir -p ./broker-certs
   cp ../../../scripts/prebuild/pki/output/broker/* ./broker-certs/
   chmod 0644 ./broker-certs/server.crt ./broker-certs/ca-chain.pem
   chmod 0600 ./broker-certs/server.key
   ```

2. Copy mapexVault bootstrap materials:
   ```bash
   mkdir -p ./vault-pki-seed
   cp ../../../scripts/prebuild/pki/output/ca/* ./vault-pki-seed/
   chmod 0600 ./vault-pki-seed/*.key
   chmod 0644 ./vault-pki-seed/*.crt
   ```

3. Bring mapexVault up first:
   ```bash
   docker compose up -d mapex-vault
   docker compose logs mapex-vault | grep '[SERVICE:Pki]'
   ```
   Expect: `OnMount: bootstrap complete (root + intermediate persisted)`.

4. Bring the rest up:
   ```bash
   docker compose up -d
   ```

5. Verify broker TLS:
   ```bash
   docker compose logs mapex-mqtt-broker | grep TLS
   ```
   Expect:
   ```
   [ENTRYPOINT] TLS auto-detected: server.crt + server.key present, enabling listener 8883
   [ENTRYPOINT] mTLS auto-detected: /mosquitto/certs/ca.pem present, require_certificate=true
   ```

## Override flags (env)

| Env | Default | Purpose |
|---|---|---|
| `MAPEX_BROKER_CERTS_DIR` | `./broker-certs` | Host path mounted at `/mosquitto/certs` |
| `MAPEX_VAULT_PKI_SEED_DIR` | `./vault-pki-seed` | Host path mounted at `/etc/mapex/pki-seed` |
| `TLS_ENABLED=false` | unset (auto-detect) | Force-disable TLS even when cert files are mounted |
| `TLS_REQUIRE_CLIENT_CERT=false` | `true` | Disable mTLS while keeping the TLS listener |

## Renewal

Re-run the pre-build (it asks for confirmation), redo steps 1–2, restart the broker:

```bash
docker compose restart mapex-mqtt-broker
```

Rotating the platform CA itself requires Mongo cleanup before mapexVault sees the new seed; out of scope for the standard renewal flow.
