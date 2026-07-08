# Configuration

How the operator configures the `mapex-broker-mqtt` container at
runtime. All knobs are exposed as **environment variables**. The
`mapex-broker` plugin reads them directly through the shared
`mapexGoKit/config` flow — the same `InitConfig` / `ConfigDefinition`
mechanism every mapexOS Go service uses. `/entrypoint.sh` only renders
the mosquitto-native listener block (`mosquitto.conf.template` via
`envsubst`); business settings are no longer written into
`mosquitto.conf`.

## The production guard (`GO_ENV`)

Every credential and the `NATS_URL` (which carries inline
`user:password`) has a dev-friendly default so a bare container boots
against the local stack. Those defaults must never reach production.
`InitConfig` runs the shared sensitive-default guard at startup:

- `GO_ENV=dev` (or unset): the plugin boots; a `[SECURITY WARNING]`
  log line names any sensitive key still using its dev default.
- `GO_ENV` any non-dev value (`staging`, `prod`, …): the plugin
  **refuses to start** if any sensitive key still holds its dev
  default, logging `[SECURITY]` and exiting non-zero.

The sensitive keys are `NATS_URL`, `INTERNAL_API_KEY`,
`OBJECT_STORE_ACCESS_KEY`, and `OBJECT_STORE_SECRET_KEY`. Set them to
real values in every non-dev deployment.

## Core variables

These have dev defaults; the entrypoint no longer fails fast when they
are unset (the guard above is the enforcement point in non-dev).

| Variable | Default | Purpose |
|---|---|---|
| `GO_ENV` | `dev` | Selects warn (dev) vs abort (non-dev) for the sensitive-default guard. |
| `INTERNAL_API_KEY` | dev key | Shared secret on the `X-API-Key` header for the auth callout. MUST equal `internal_api_key` in the assets MS. Sensitive. |
| `NATS_URL` | `nats://service:service_secret@localhost:4222` | NATS server URL the plugin publishes to (credentials inline). Sensitive. |
| `ASSETS_HOST` | `assets` | Hostname of the assets MS internal listener. `AUTH_URL` is derived as `http://{host}:{port}/internal/asset_auth`. |
| `ASSETS_PORT` | `5002` | Port of the assets MS internal listener. |

## Optional variables

Defaults below are applied by the plugin's `ConfigDefinition` list
when the variable is absent (mosquitto-native listener vars are
defaulted by `entrypoint.sh`).

### Object store (TieredCache L2)

| Variable | Default | Purpose |
|---|---|---|
| `OBJECT_STORE_ENDPOINT` | `` (empty) | MinIO/S3 endpoint for the L2 auth-projection cache. Empty disables L2 (plugin falls back to L1 + L3). |
| `OBJECT_STORE_ACCESS_KEY` | `svc-broker` | Scoped object-store user. Sensitive — override in non-dev. |
| `OBJECT_STORE_SECRET_KEY` | `svc-broker-secret-change-me` | Secret for the scoped user. Sensitive — override in non-dev. |
| `OBJECT_STORE_USE_SSL` | `false` | TLS to the object store. |
| `OBJECT_STORE_AUTH_IS_NEEDED` | `true` | `true` = static keys; `false` = ambient IAM (keys ignored). |

The L2 bucket name is fixed by the platform contract
(`mapex-asset-auth`) and is not operator-configurable.

### Listener

| Variable | Default | Purpose |
|---|---|---|
| `MQTT_LISTENER_PORT` | `1883` | TCP port the broker binds. Match the `EXPOSE` directive and the docker-compose port mapping. |
| `MQTT_MAX_CONNECTIONS` | `-1` | `-1` = unlimited. Set a positive integer to cap concurrent clients (mosquitto-side, before the plugin sees CONNECT). |

### NATS subjects

The plugin reads `GO_ENV` only to drive the sensitive-default guard —
it does NOT env-prefix subjects itself. Operators still pass full
env-prefixed subject names so `dev`, `staging`, and `prod` deployments
can share a NATS cluster without collisions.

| Variable | Default | Purpose |
|---|---|---|
| `NATS_SUBJECT_PRESENCE` | `dev.mapexos.presence.advisory` | Subject for `event:"connect"` and `event:"disconnect"` advisories. The healthmonitor module subscribes here. |
| `NATS_SUBJECT_INGRESS_PREFIX` | `dev.mapexos.mqtt.data` | Leading subject token for device PUBLISH events. The plugin appends `.{orgId}.{assetUUID}` per message — JS-Executor's wildcard consumer at `{prefix}.>` routes to per-device filter chains. |

Production deployments override the prefix, e.g.:

```yaml
NATS_SUBJECT_PRESENCE: prod.mapexos.presence.advisory
NATS_SUBJECT_INGRESS_PREFIX: prod.mapexos.mqtt.data
```

### Auth (TieredCache L3 fallback)

The broker plugin makes ZERO HTTP auth callouts. Every CONNECT decision
(bcrypt for password mode, serial-equality for cert mode) is made
LOCALLY off the `AuthEntry` projection returned by the plugin's
TieredAuthStore (L1 Pebble → L2 MinIO → L3 HTTP).

The L3 fallback is a read-only GET against the assets MS internal
read-model endpoint. It runs only when both L1 and L2 miss — typical
warm path never reaches it.

| Variable | Default | Purpose |
|---|---|---|
| `AUTH_TIMEOUT_SECONDS` | `5` | HTTP timeout per L3 lookup. Mosquitto blocks the CONNECT handshake while the plugin awaits the lookup, so this caps the broker-thread parking time. Lower for fast-failing under degraded assets MS, higher only when assets MS warm path is genuinely slow. |

The L3 lookup URL is built from `ASSETS_HOST` + `ASSETS_PORT` and the
canonical base path:

```
http://${ASSETS_HOST}:${ASSETS_PORT}/internal/assets
```

The plugin appends `/{assetUUID}` per lookup. The path is hard-coded
in the template — change requires editing
`config/mosquitto.conf.template` and rebuilding the image. The
endpoint lives inside the assets MS's `assets` module (`GET
/internal/assets/:assetUUID`), gated by the standard `X-API-Key`
middleware. The response is the standard MapexOS envelope wrapping
an `AssetReadModel`; the plugin projects out `protocol.mqtt.passwordHash`
and `currentCert.serial` and discards the rest.

### Async publisher tuning

The plugin's NATS publisher is a bounded channel + worker pool that
isolates the broker thread from NATS slowness. Defaults are sized for
~1k events/sec sustained without dropping. Tune for higher volume.

| Variable | Default | Purpose |
|---|---|---|
| `PLUGIN_WORKER_POOL_SIZE` | `4` | Goroutines draining the publish channel. Each worker handles one publish at a time. Increase if `PublishedCount` grows slower than `EnqueuedCount` under load. |
| `PLUGIN_BUFFER_SIZE` | `10000` | Channel capacity. When full, new events are dropped (counted in `DroppedCount`). Increase to absorb longer NATS hiccups; decrease only if memory budget is tight (each slot holds a small struct + a byte slice copy). |

A growing `DroppedCount` is the operator-facing signal that the pool
is undersized for the deployment's event rate. Either bump
`PLUGIN_WORKER_POOL_SIZE` (more concurrent NATS publishes) or
`PLUGIN_BUFFER_SIZE` (deeper queue) — typically the former first.

## Full minimal configuration

The smallest deployment that boots and serves devices:

```bash
docker run --rm \
  -p 1883:1883 \
  -e INTERNAL_API_KEY=$(openssl rand -hex 32) \
  -e NATS_URL=nats://nats:4222 \
  -e ASSETS_HOST=assets \
  -e ASSETS_PORT=5002 \
  --network mapex-net \
  docker.io/thiagoanselmo/mapex-broker-mqtt:dev
```

Everything else falls back to defaults. The boot sequence prints the
rendered config and the plugin's init lifecycle to stderr:

```
[ENTRYPOINT] config rendered: listener=1883 nats=nats://nats:4222 assets=assets:5002
[ENTRYPOINT] subjects: presence=dev.mapexos.presence.advisory ingress_prefix=dev.mapexos.mqtt.data
INFO  [PLUGIN:Mosquitto] plugin_init: starting
INFO  [PLUGIN:Mosquitto] NATS connected url=nats://nats:4222 server=nats://nats:4222
INFO  [PLUGIN:Mosquitto] AsyncPublisher started: workers=4 buffer=10000
INFO  [PLUGIN:Mosquitto] PluginRuntime initialized: presence=... ingress_prefix=... auth_url=...
INFO  [PLUGIN:Mosquitto] plugin_init: ready (4 callbacks registered)
```

In a non-dev `GO_ENV`, if a sensitive credential is still at its dev
default the plugin guard refuses to start:

```
[SECURITY] refusing to start in GO_ENV=prod — sensitive env vars using DEV defaults: NATS_URL, INTERNAL_API_KEY. Set them to production values before deploying.
```

## Production reference (docker-compose)

```yaml
services:
  mapex-broker-mqtt:
    image: docker.io/thiagoanselmo/mapex-broker-mqtt:${MAPEX_BROKER_VERSION:-2026.05.08}
    container_name: mapex-broker-mqtt
    ports:
      - "1883:1883"
    environment:
      INTERNAL_API_KEY: ${INTERNAL_API_KEY:?required}
      NATS_URL: nats://nats-core:4222
      ASSETS_HOST: assets
      ASSETS_PORT: "5002"
      NATS_SUBJECT_PRESENCE: ${GO_ENV:-prod}.mapexos.presence.advisory
      NATS_SUBJECT_INGRESS_PREFIX: ${GO_ENV:-prod}.mapexos.mqtt.data
      AUTH_TIMEOUT_SECONDS: "5"
      PLUGIN_WORKER_POOL_SIZE: "8"
      PLUGIN_BUFFER_SIZE: "20000"
    volumes:
      - mosquitto-data:/mosquitto/data
    depends_on:
      nats-core:
        condition: service_healthy
      assets:
        condition: service_started
    restart: unless-stopped
    healthcheck:
      test: ["CMD-SHELL", "nc -z 127.0.0.1 1883 || exit 1"]
      interval: 15s
      timeout: 5s
      retries: 3
      start_period: 10s

volumes:
  mosquitto-data:
```

## TLS listener (port 8883)

Public-internet deployments and any device on cellular/NB-IoT MUST
use TLS. The container ships with a TLS listener that the entrypoint
appends to the rendered config when `TLS_ENABLED=true`.

### TLS env vars

| Variable | Default | Purpose |
|---|---|---|
| `TLS_ENABLED` | `false` | Set `true` to enable the TLS listener on `MQTT_TLS_LISTENER_PORT`. The plaintext listener on 1883 stays active in parallel — operators control exposure via docker-compose port mappings. |
| `MQTT_TLS_LISTENER_PORT` | `8883` | TCP port for the TLS listener. Match the port mapping in your compose file. |
| `TLS_CERT_FILE` | `/mosquitto/certs/server.crt` | Server certificate (PEM). Container exits at startup if `TLS_ENABLED=true` and this file is missing. |
| `TLS_KEY_FILE` | `/mosquitto/certs/server.key` | Server private key (PEM). Same fail-fast as `TLS_CERT_FILE`. |
| `TLS_CA_FILE` | `` (empty) | Optional CA certificate. When set, mTLS is enabled — clients MUST present a certificate chained to this CA. Empty disables mTLS (server-side TLS only, like HTTPS without client certs). |
| `TLS_REQUIRE_CLIENT_CERT` | `false` | Only meaningful with `TLS_CA_FILE`. When `true`, mosquitto rejects clients that do not present a valid client cert; when `false`, clients may connect with or without a cert. |
| `TLS_MIN_VERSION` | `tlsv1.2` | Minimum TLS version. `tlsv1.2` or `tlsv1.3`. |

### Cert mount (server-only TLS)

The simplest case — TLS for transport security, no client certs.
Mount your cert + key into `/mosquitto/certs/` and flip the toggle:

```yaml
services:
  mapex-broker-mqtt:
    image: docker.io/thiagoanselmo/mapex-broker-mqtt:dev
    ports:
      - "1883:1883"        # plaintext (internal/dev only)
      - "8883:8883"        # TLS (public devices)
    environment:
      INTERNAL_API_KEY: ${INTERNAL_API_KEY:?required}
      NATS_URL: nats://nats:4222
      ASSETS_HOST: assets
      ASSETS_PORT: "5002"
      TLS_ENABLED: "true"
      TLS_CERT_FILE: /mosquitto/certs/server.crt
      TLS_KEY_FILE:  /mosquitto/certs/server.key
    volumes:
      - ./certs:/mosquitto/certs:ro
```

### mTLS (mutual TLS)

For high-trust deployments where every device carries a
platform-issued client certificate. Add the CA file and require it:

```yaml
environment:
  TLS_ENABLED: "true"
  TLS_CERT_FILE: /mosquitto/certs/server.crt
  TLS_KEY_FILE:  /mosquitto/certs/server.key
  TLS_CA_FILE:   /mosquitto/certs/ca.crt
  TLS_REQUIRE_CLIENT_CERT: "true"
volumes:
  - ./certs:/mosquitto/certs:ro
```

The plugin enforces mutual exclusion between auth modes — a
password-mode asset that presents a cert is denied, and vice versa.
The `use_identity_as_username false` is hard-coded so the username
slot on CONNECT is always the auth identity (bare assetUUID).

### PKI / Certificate generation

Certificates are **not** generated by this repository. The platform
PKI is bootstrapped automatically by the `mongodb-init` container in
the [mapexOSDeploy](https://github.com/Mapex-Solutions/mapexOSDeploy)
stack:

1. `docker compose up -d` runs `mongodb-init`
2. `mongodb-init` generates root CA + intermediate + broker cert
3. Broker certs are written to `./broker-certs/` on the host
4. The broker container mounts `./broker-certs:/mosquitto/certs:ro`
5. `entrypoint.sh` auto-detects the certs and enables TLS (8883) + mTLS

**The operator never generates certificates manually.** To rotate,
clear `./broker-certs/` and restart `mongodb-init`.

### Validating the TLS listener

```bash
# Server-only TLS (server cert verified against system trust store):
mosquitto_pub --cafile /path/to/ca.pem -h broker.example.com -p 8883 \
    -u 'org-1:asset-aaa' -P 'good-pwd' \
    -t 'events/org-1/asset-aaa/x' -m '{"v":1}'

# mTLS (client cert + key required):
mosquitto_pub --cafile ca.pem --cert client.crt --key client.key \
    -h broker.example.com -p 8883 \
    -u 'org-1:asset-aaa' -P 'good-pwd' \
    -t 'events/org-1/asset-aaa/x' -m '{"v":1}'
```

### TLS healthcheck

The default healthcheck probes the plaintext listener
(`MQTT_LISTENER_PORT`, default 1883). When you disable plaintext for
a public deployment, override the healthcheck in compose:

```yaml
healthcheck:
  test: ["CMD-SHELL", "nc -z 127.0.0.1 ${MQTT_TLS_LISTENER_PORT:-8883} || exit 1"]
```

`nc -z` only verifies the TCP port is in LISTEN — it doesn't probe
the TLS handshake. For deeper validation in production, run a
synthetic client outside the container.

## What you cannot configure (yet)

| Concern | Status |
|---|---|
| WebSocket listener | Not in template. Mosquitto supports it natively (`listener 9001` + `protocol websockets`); add by editing the template if needed. |
| Per-listener auth | The plugin's auth chain runs the same on every listener. |
| Auth backend other than TieredAuthStore | Hard-coded — the plugin uses L1 Pebble → L2 MinIO → L3 HTTP. |
| ACL rule customization | Hard-coded in `src/acl.go`. The platform's topic structure is the contract; changing it requires editing + rebuilding. |

If any of these become a real requirement, file an issue describing
the use case before adding the env knob — the plugin's value is its
small surface area.

## Persistence volume

Mosquitto's session state for QoS 1+ retransmits is written to
`/mosquitto/data/`. Mount a volume here to survive container
restarts:

```yaml
volumes:
  - mosquitto-data:/mosquitto/data
```

Without a volume, every container restart drops in-flight QoS 1+
sessions and clients have to reconnect. For typical IoT workloads
(devices using QoS 0 or short-lived QoS 1) this is acceptable;
mission-critical command/control should always persist.

## Healthcheck

The Dockerfile ships a TCP probe against the listener port:

```
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
    CMD nc -z 127.0.0.1 ${MQTT_LISTENER_PORT:-1883} || exit 1
```

If the port is in `LISTEN`, the broker has gotten past plugin init —
NATS connected, callbacks registered, plugin ready. A failing probe
means either the broker crashed (plugin init returned non-zero) or
the listener is bound to a non-default port that the operator forgot
to align with the healthcheck.

To run a deeper check in production (assert the auth callout
actually works), wire a synthetic client outside the container and
publish/subscribe with known credentials.

## Override checklist before deploying to a new environment

- [ ] `GO_ENV` set to the target environment (`staging`, `prod`) so the sensitive-default guard is armed
- [ ] `INTERNAL_API_KEY` matches the assets MS configured key
- [ ] `NATS_URL`, `OBJECT_STORE_ACCESS_KEY`, `OBJECT_STORE_SECRET_KEY` overridden away from their dev defaults
- [ ] `NATS_URL` resolves and is reachable from the broker network
- [ ] `ASSETS_HOST` / `ASSETS_PORT` resolve and are reachable
- [ ] `NATS_SUBJECT_PRESENCE` env-prefix matches the rest of the platform (`prod`, `staging`, `dev`)
- [ ] `NATS_SUBJECT_INGRESS_PREFIX` env-prefix matches
- [ ] Persistent volume mounted on `/mosquitto/data` if you need session durability
- [ ] Image tag pinned (`<YYYY.MM.DD>` or `<vX.Y.Z>`) — never `dev` or `latest` in prod
