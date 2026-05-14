# Mapex MQTT Broker — Smoke Test Stack

Self-contained docker-compose that exercises the broker plugin's
**TieredAuthStore** cascade (L1 Pebble → L2 MinIO → L3 HTTP) and the
**FANOUT invalidation** path, without needing the real Assets MS.

## What it proves

Running `./run.sh` walks the broker through 8 steps and prints the log
lines that should fire at each step:

| Step | What | Expected log line |
|---|---|---|
| 1 | Stack up | `auth_store: L1 Pebble ready ...`, `auth_store: L2 MinIO ready ...`, `fanout consumer started` |
| 2 | Seed L2 | (no broker log; just `mc cp` confirms object) |
| 3 | First CONNECT | `auth_store: L2 hit assetUUID=... orgId=... warmed=L1` |
| 4 | Second CONNECT | `auth_store: L1 hit assetUUID=... orgId=...` |
| 5 | FANOUT invalidate | `auth_store: invalidated L1 assetUUID=... next_read=L2` |
| 6 | Third CONNECT | `auth_store: L2 hit ...` (back to L2) |
| 7 | Cross-tenant attempt | `Authenticate ... Deny` (username-claimed org ≠ stored org) |

## Quick start

```bash
cd tests/smoke

# Build the broker image first (from the repo root)
( cd ../.. && make build )

# Run the full scripted smoke
./run.sh

# Tail the broker logs while it's up
docker compose logs -f broker

# Clean up
docker compose down -v
```

## Manual exploration

You can also drive the stack by hand:

```bash
docker compose up -d

# Seed an entry into L2 (MinIO). bcrypt hash is computed locally via a
# one-shot Python container — your password never touches the broker
# image except as the actual bcrypt hash.
./seed-asset.sh asset-aaa org-1 secret

# Tail the broker logs in another terminal
docker compose logs -f broker

# CONNECT — first time will L2-hit, subsequent ones L1-hit
docker exec -it mapex-smoke-tools-mqtt mosquitto_sub \
    -h broker -p 1883 \
    -u 'org-1:asset-aaa' -P secret \
    -t '#' -v -d

# Invalidate the L1 entry
./invalidate-asset.sh asset-aaa

# Inspect MinIO directly
docker exec mapex-smoke-tools-mc mc ls mapex/mapex-mqtt-auth

# Inspect NATS
docker exec mapex-smoke-tools-nats nats --server nats://nats:4222 sub mapexos.fanout.asset.invalidate
```

## What's NOT covered here

- **Cert-based auth (mTLS)**. The broker supports it (`TLS_ENABLED=true`
  + `TLS_CA_FILE` + `ActiveCertSerials` in the AuthEntry), but the smoke
  stack runs plain TCP on 1883 to keep the surface small. Add the
  `--mtls` profile in a follow-up if needed.
- **Real Assets MS write path**. This stack mocks the L2 by writing
  directly into the bucket via `mc`. To exercise the full cycle (CRUD
  → assets MS → MinIO write → broker reads), use the mapexOS deployment
  compose under `deployment/docker-compose/services_required/`.
- **L3 HTTP fallback**. The broker is configured with
  `ASSETS_HOST=assets-not-running`; an L1+L2 miss will fail at L3 and
  log `auth_store: L3 unavailable ... decision=fail_closed`. That's the
  intended fail-closed behaviour and is asserted by the unit tests in
  `src/auth_store_test.go`.

## Credential policy

The MinIO root user/password defaults are LOCAL TEST CREDENTIALS hard-coded
into `docker-compose.yml`. They are never used outside this file and never
touch production. Override via env if you want:

```bash
MINIO_ROOT_USER=tester MINIO_ROOT_PASSWORD=changeme docker compose up -d
```

The release script (`scripts/release/start.sh`) and the broker image
itself contain **no credentials**. Registry auth must be set up out of
band via `docker login` before running `./scripts/release/start.sh ... --push`.
