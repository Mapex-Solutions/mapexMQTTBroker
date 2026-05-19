# Smoke Test

Minimal smoke test for the broker plugin's TieredAuthStore cascade
and FANOUT invalidation path.

## What it tests

| Step | Check |
|---|---|
| First CONNECT | L2 hit (L1 cold) |
| Second CONNECT | L1 hit (warmed by step 1) |
| FANOUT invalidate | L1 entry dropped |
| Third CONNECT | L2 hit again (L1 was invalidated) |
| Wrong password | Auth denied |

## Run

```bash
cd tests/smoke
docker compose up -d
./run.sh
docker compose down -v
```

Override the broker image:

```bash
BROKER_IMAGE=mapexos/mapex-broker-mqtt:0.1.0 ./run.sh
```

## Stack

- **NATS Core** — fanout subject for invalidation
- **MinIO** — L2 auth projection bucket
- **Broker** — the image under test

No toolbox containers. The `run.sh` script uses `docker run --rm` for
one-shot MQTT, NATS, and MinIO operations on the smoke network.
