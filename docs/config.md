# Configuration

How the operator configures the `mapex-broker-mqtt` container at
runtime. All knobs are exposed as **environment variables** —
`/entrypoint.sh` renders `mosquitto.conf.template` via `envsubst`
when the container starts and immediately fails if a required
variable is missing.

## Required variables

The container refuses to start unless all four are set.

| Variable | Purpose | Example |
|---|---|---|
| `INTERNAL_API_KEY` | Shared secret on the `X-API-Key` header for the auth callout. MUST equal `internal_api_key` configured in the assets MS. | `f3b1...` (high-entropy random string) |
| `NATS_URL` | NATS server URL the plugin publishes to. The container exits if the initial dial fails. | `nats://nats-core:4222` |
| `ASSETS_HOST` | Hostname of the assets MS internal listener. | `assets` (compose service name) |
| `ASSETS_PORT` | Port of the assets MS internal listener. | `5002` |

`INTERNAL_API_KEY` and `NATS_URL` MUST be set or the entrypoint fails
fast at boot. `ASSETS_HOST` / `ASSETS_PORT` have defaults (`assets` /
`5002`) but should be made explicit per environment.

## Optional variables

Defaults below are applied by `entrypoint.sh` when the variable is
absent or empty.

### Listener

| Variable | Default | Purpose |
|---|---|---|
| `MQTT_LISTENER_PORT` | `1883` | TCP port the broker binds. Match the `EXPOSE` directive and the docker-compose port mapping. |
| `MQTT_MAX_CONNECTIONS` | `-1` | `-1` = unlimited. Set a positive integer to cap concurrent clients (mosquitto-side, before the plugin sees CONNECT). |

### NATS subjects

The plugin is environment-agnostic — it doesn't read `GO_ENV`.
Operators pass full env-prefixed subject names so `dev`, `staging`,
and `prod` deployments can share a NATS cluster without collisions.

| Variable | Default | Purpose |
|---|---|---|
| `NATS_SUBJECT_PRESENCE` | `dev.mapexos.mqtt.presence.advisory` | Subject for `event:"connect"` and `event:"disconnect"` advisories. The healthmonitor module subscribes here. |
| `NATS_SUBJECT_INGRESS_PREFIX` | `dev.mapexos.mqtt.data` | Leading subject token for device PUBLISH events. The plugin appends `.{orgId}.{assetUUID}` per message — JS-Executor's wildcard consumer at `{prefix}.>` routes to per-device filter chains. |

Production deployments override the prefix, e.g.:

```yaml
NATS_SUBJECT_PRESENCE: prod.mapexos.mqtt.presence.advisory
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
  docker.io/mapexos/mapex-broker-mqtt:dev
```

Everything else falls back to defaults. The boot sequence prints the
rendered config and the plugin's init lifecycle to stderr:

```
[ENTRYPOINT] config rendered: listener=1883 nats=nats://nats:4222 assets=assets:5002
[ENTRYPOINT] subjects: presence=dev.mapexos.mqtt.presence.advisory ingress_prefix=dev.mapexos.mqtt.data
INFO  [PLUGIN:Mosquitto] plugin_init: starting
INFO  [PLUGIN:Mosquitto] NATS connected url=nats://nats:4222 server=nats://nats:4222
INFO  [PLUGIN:Mosquitto] AsyncPublisher started: workers=4 buffer=10000
INFO  [PLUGIN:Mosquitto] PluginRuntime initialized: presence=... ingress_prefix=... auth_url=...
INFO  [PLUGIN:Mosquitto] plugin_init: ready (4 callbacks registered)
```

If any required variable is missing, the entrypoint exits before
mosquitto starts:

```
/entrypoint.sh: line 26: INTERNAL_API_KEY: INTERNAL_API_KEY is required
```

## Production reference (docker-compose)

```yaml
services:
  mapex-broker-mqtt:
    image: docker.io/mapexos/mapex-broker-mqtt:${MAPEX_BROKER_VERSION:-2026.05.08}
    container_name: mapex-broker-mqtt
    ports:
      - "1883:1883"
    environment:
      INTERNAL_API_KEY: ${INTERNAL_API_KEY:?required}
      NATS_URL: nats://nats-core:4222
      ASSETS_HOST: assets
      ASSETS_PORT: "5002"
      NATS_SUBJECT_PRESENCE: ${GO_ENV:-prod}.mapexos.mqtt.presence.advisory
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
    image: docker.io/mapexos/mapex-broker-mqtt:dev
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

The plugin's username/password auth still runs — mTLS is added at the
transport layer; **the device must present a valid cert AND a valid
username+password**. The plugin does not consume the cert identity
(`use_identity_as_username false` is hard-coded so the username slot
on CONNECT is always the auth identity).

### Generating certs

The repo ships shell scripts that build a complete CA / server / device
chain. Full reference + apt prerequisites in
[scripts/README.md](../scripts/README.md). Quick paths:

**Local dev (one shot, server cert for `localhost` + sample device cert):**

```bash
sudo apt-get install -y openssl
./scripts/build_dev_certs.sh
```

Produces `certs/ca.crt`, `certs/server.{key,crt}`, and
`certs/devices/org-1__asset-aaa.{key,crt}` ready to mount.

**Production CA (10 years) + server cert (2 years) + device certs (5 years each):**

```bash
./scripts/build_ca.sh                                            # once per environment
SERVER_HOST=broker.example.com \
  SERVER_SAN="DNS:broker.example.com" \
  ./scripts/build_server_cert.sh                                 # per broker host
./scripts/build_device_cert.sh "org-1:asset-aaa"                 # per device
```

For public-facing brokers, use Let's Encrypt for the server cert
and keep this internal CA only for the device chain. Mosquitto reads
the files on startup; rotate by replacing files and restarting the
container — see the rotation table in
[scripts/README.md](../scripts/README.md).

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
| Auth backend other than HTTP | Hard-coded — the plugin only speaks HTTP to the assets MS. |
| ACL rule customization | Hard-coded in `src/acl.go`. The platform's topic structure is the contract; changing it requires editing + rebuilding. |
| Auth-side caching inside the plugin | Intentionally absent — the assets MS owns the cache (Ristretto L0). |

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

- [ ] `INTERNAL_API_KEY` matches the assets MS configured key
- [ ] `NATS_URL` resolves and is reachable from the broker network
- [ ] `ASSETS_HOST` / `ASSETS_PORT` resolve and are reachable
- [ ] `NATS_SUBJECT_PRESENCE` env-prefix matches the rest of the platform (`prod`, `staging`, `dev`)
- [ ] `NATS_SUBJECT_INGRESS_PREFIX` env-prefix matches
- [ ] Persistent volume mounted on `/mosquitto/data` if you need session durability
- [ ] Image tag pinned (`<YYYY.MM.DD>` or `<vX.Y.Z>`) — never `dev` or `latest` in prod
