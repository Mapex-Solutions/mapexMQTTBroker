# Mapex MQTT Broker Deploy

Operator runbook for bringing the broker up with TLS/mTLS enabled.

## Prerequisites

The [mapexOSDeploy](https://github.com/Mapex-Solutions/mapexOSDeploy)
stack handles everything. No manual PKI generation is needed.

## Deploy steps

Working dir: `mapexOSDeploy/`.

1. Clone and start:
   ```bash
   git clone https://github.com/Mapex-Solutions/mapexOSDeploy.git
   cd mapexOSDeploy
   docker compose up -d
   ```

2. Wait ~2 minutes. The `mongodb-init` container:
   - Initializes the MongoDB replica set
   - Generates the platform PKI (root CA + intermediate + broker cert)
   - Writes `server.crt`, `server.key`, `ca-chain.pem` to `./broker-certs/`
   - Envelope-encrypts CA keys and inserts them into mapexVault's database
   - Seeds initial data (users, orgs, roles)

3. The broker starts after `mongodb-init` completes and auto-detects TLS:
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
| `TLS_ENABLED=false` | unset (auto-detect) | Force-disable TLS even when cert files are mounted |
| `TLS_REQUIRE_CLIENT_CERT=false` | `true` | Disable mTLS while keeping the TLS listener |

## Renewal

Clear the broker certs and restart `mongodb-init` to regenerate:

```bash
rm -rf ./broker-certs/*
docker compose up -d mongodb-init --force-recreate
docker compose restart mapex-mqtt-broker
```

The `mongodb-init` container is idempotent — on re-run with empty
`broker-certs/` it generates fresh material. Existing device certs
remain valid because the CA stays the same (stored in Mongo).

Rotating the platform CA itself requires clearing the
`mapex_vault.pkiCertificateAuthorities` collection before
`mongodb-init` runs; out of scope for the standard renewal flow.
