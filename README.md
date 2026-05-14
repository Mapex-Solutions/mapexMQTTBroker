# Mapex MQTT Broker

> Production MQTT broker for the MapexOS IoT platform — Eclipse
> Mosquitto v2 + a single in-house plugin that handles **auth**,
> **ACL**, **presence**, and **ingress** in one Go binary.
> Distributed as a ready-to-pull Docker image
> (`mapexos/mapex-broker-mqtt`).

## What this project is

A Mosquitto plugin that turns a stock MQTT broker into the MapexOS
edge — every device CONNECT, every authorized PUBLISH, every
DISCONNECT becomes a structured NATS event consumable by the rest of
the platform.

| Responsibility | Where it runs |
|---|---|
| **Auth** (HTTP callout) | `MOSQ_EVT_BASIC_AUTH` in this plugin → POST to assets MS `/auth/user` |
| **ACL** (allow/deny PUB+SUB) | `MOSQ_EVT_ACL_CHECK` in this plugin → pure-Go string compare, sub-µs, zero hop |
| **Presence** (online/offline) | `MOSQ_EVT_DISCONNECT` + auth-success → NATS `mqtt.presence.advisory` |
| **Ingress** (device → platform) | `MOSQ_EVT_MESSAGE` → NATS `mqtt.data.{orgId}.{assetUUID}` |

A single `.so`, four broker hooks, two NATS subjects. No external
dependencies beyond Mosquitto v2 and a NATS server.

The container exposes both the plaintext MQTT port (`1883`) and the
TLS port (`8883`). TLS is opt-in via `TLS_ENABLED=true` and supports
optional mTLS — see [docs/config.md](docs/config.md) for the cert
mount + env vars.

## Why a dedicated repo

This broker is shared infrastructure consumed by every MapexOS
deployment. Living in its own repo lets it:

- Version + tag independently from the consuming service repos.
- Publish to a Docker registry (`mapexos/mapex-broker-mqtt:<tag>`)
  so operators **only pull**, never build.
- Carry its own CI lifecycle (build → test → push) without coupling
  to the larger goKit / mapexOS pipelines.

## Layout

```
.
├── README.md              this file
├── Makefile               build / push / release targets
├── go.mod                 module: github.com/Mapex-Solutions/mapexMQTTBroket
├── src/
│   ├── *.go               broker package: ACL, NATS publisher, HTTP auth, config
│   ├── *_test.go          Go-pure unit tests (no broker, no NATS required)
│   └── plugin/
│       ├── main.go        cgo entry — plugin lifecycle + 4 hooks
│       ├── plugin_version.c
│       └── trampolines.c  C bridges between mosquitto callbacks and Go
├── config/
│   └── mosquitto.conf.template   rendered by entrypoint.sh on container start
├── docker/
│   ├── Dockerfile         multi-stage builder → mapexos/mapex-broker-mqtt
│   └── entrypoint.sh      envsubst + sanity check + exec mosquitto
├── docs/
│   ├── architecture.md    how the plugin works internally
│   └── config.md          environment-variable reference
└── tests/                 (reserved for integration tests)
```

Go sources live under `src/` per project convention. Two import
paths matter:

- `github.com/Mapex-Solutions/mapexMQTTBroket/src` — the broker
  package (Go-pure, fully testable)
- `github.com/Mapex-Solutions/mapexMQTTBroket/src/plugin` — cgo
  entry point (gated by build tag `cgo_mosquitto_plugin`)

## Running the published image

```yaml
# docker-compose.yml
services:
  mapex-broker-mqtt:
    image: docker.io/mapexos/mapex-broker-mqtt:dev
    ports:
      - "1883:1883"
    environment:
      INTERNAL_API_KEY: ${INTERNAL_API_KEY:?required}
      NATS_URL: nats://nats:4222
      ASSETS_HOST: assets
      ASSETS_PORT: "5002"
      NATS_SUBJECT_PRESENCE: dev.mapexos.mqtt.presence.advisory
      NATS_SUBJECT_INGRESS_PREFIX: dev.mapexos.mqtt.data
    depends_on:
      nats:
        condition: service_healthy
      assets:
        condition: service_started
```

Operators do not build anything. The image self-contains the broker,
the plugin, and a templated config rendered from environment variables
at startup. The full env-variable reference is in
[docs/config.md](docs/config.md). The internal request-flow,
threading model, and failure modes are in
[docs/architecture.md](docs/architecture.md).

## Building locally

The default `go build ./...` excludes the cgo entry (build tag), so
the broker package builds + tests on any host without
`libmosquitto-dev`:

```bash
make test    # go test -race ./src/...
```

To build the actual `.so` you need the broker headers; the easy path
is the Dockerfile, which installs them in the builder stage:

```bash
make build VERSION=dev
```

That produces `docker.io/mapexos/mapex-broker-mqtt:dev` ready to
run. Override `REGISTRY` and `VERSION` to publish:

```bash
docker login docker.io
make release VERSION=2026.05.08
```

`release` cross-compiles for `linux/amd64` + `linux/arm64` via buildx
and pushes `:VERSION` + `:latest` in a single command.

## Compatibility matrix

| Component | Pinned to |
|---|---|
| Mosquitto | 2.0.x (Debian bookworm package) |
| Plugin API | v5 |
| NATS server | 2.10+ (Core Pub/Sub used; JetStream optional, captured upstream) |
| Go | 1.25 |
| Runtime base | `debian:bookworm-slim` (glibc; Alpine breaks cgo TLS) |

## Tagging convention

| Tag | Source | Lifetime |
|---|---|---|
| `dev` | feature/integration branch builds | overwritten on every push |
| `<YYYY.MM.DD>` | release branch builds | immutable |
| `<vX.Y.Z>` | git semver tags | immutable |
| `latest` | most recent stable release | floats |

## License

Proprietary — Mapex Solutions.
