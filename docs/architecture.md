# Architecture

How the `mapex-broker.so` plugin works internally — what runs on which
thread, how data flows between Mosquitto callbacks and NATS, what
invariants the implementation relies on.

## Process model

Mosquitto runs as a single-threaded event loop. When a plugin is
loaded via `dlopen`, the broker calls `mosquitto_plugin_init()` once,
during which the plugin registers callbacks for specific events. From
that point on, every event fires on the **broker thread** — the same
thread that drives the MQTT protocol state machine.

```
┌─────────────────────────────────────────────────────────────────┐
│ Mosquitto process (single thread for protocol)                  │
│                                                                 │
│   ┌──────────────┐    dlopen()    ┌──────────────────────────┐  │
│   │ broker core  │────────────────▶│ mapex-broker.so          │  │
│   │              │                 │                          │  │
│   │              │ ◀───callbacks── │  init / cleanup          │  │
│   │              │                 │  4 hooks (auth/acl/      │  │
│   │              │                 │  disconnect/message)     │  │
│   └──────────────┘                 │                          │  │
│                                    │  Go runtime + GC         │  │
│                                    │  (cgo c-shared mode)     │  │
│                                    │                          │  │
│                                    │  AsyncPublisher  ◀────┐  │  │
│                                    │  (buffered channel)   │  │  │
│                                    │  + N worker goroutines│  │  │
│                                    └───────────────────────┼──┘  │
└────────────────────────────────────────────────────────────┼─────┘
                                                             │
                                       fire-and-forget       │
                                       NATS publish ◀────────┘
```

Two execution contexts coexist inside the broker process:

1. **Broker thread** — runs every callback synchronously. Anything
   slow here stalls every MQTT client. Contract:
   - ACL check returns in microseconds (string compare).
   - Auth callout completes within the configured timeout (default 5s).
   - Connect/disconnect/message callbacks publish to a Go channel and
     return immediately.

2. **Worker goroutines** — the AsyncPublisher pool. They drain the
   channel and call `nats.Conn.Publish` (Core, fire-and-forget). If
   NATS slows down, the channel fills, new events are dropped (counted),
   and the broker thread keeps moving. Liveness over correctness.

## The four hooks

The plugin registers for events the platform actually needs. Mosquitto
2.0.x has no `MOSQ_EVT_CONNECT` (added later in master), so the
connect signal is derived from the auth-success edge.

### MOSQ_EVT_BASIC_AUTH (synchronous, blocks broker)

Fired on every CONNECT with username + password. The plugin:

1. Reads `username`, `password`, `clientid` from the C event struct,
   copying via `C.GoString` so the Go side owns the bytes after the
   callback returns.
2. POSTs `{username, password, clientid}` to the assets MS auth URL
   with `X-API-Key` header. The HTTP client has a 5s timeout (default,
   configurable).
3. Maps the response: 200 → `MOSQ_ERR_SUCCESS`, 401 → `MOSQ_ERR_AUTH`,
   anything else (5xx, network error, timeout) → `MOSQ_ERR_UNKNOWN`
   (fail-closed). All three terminate the CONNECT immediately.
4. On 200, derives the connect signal: parses `username` as
   `{orgId}:{assetUUID}` and enqueues a presence advisory
   (`event:"connect"`) into the AsyncPublisher.

The HTTP call runs synchronously on the broker thread because
Mosquitto blocks the CONNECT handshake awaiting the auth decision —
async is impossible by protocol. The 5s timeout bounds parking time.

### MOSQ_EVT_ACL_CHECK (synchronous, in-memory)

Fired on every PUBLISH and SUBSCRIBE. Pure-Go string compare against
the topic + username structure:

```
username = "{assetUUID}"   (bare; globally unique)
allowed topics:
  events/{assetUUID}/+    acc=2 (publish)
  commands/{assetUUID}/+  acc=1 (read), acc=4 (subscribe)
```

Wildcards `+` and `#` at the event-type or command-type level are
allowed when the assetUUID slot matches the username. Cross-asset
wildcards (`+` in the assetUUID slot) are denied. No I/O,
no allocations on the hot path, sub-microsecond latency.

### MOSQ_EVT_DISCONNECT (asynchronous, fire-and-forget)

Fired when a connection drops — clean, keepalive timeout, session
takeover, admin kick. The plugin parses the username, builds a
`PresenceAdvisory{event:"disconnect", reasonCode, reasonText}`,
enqueues it, and returns. The healthmonitor downstream applies an
anti-race invariant (`disconnectAt > lastConnectAt`) to ignore stale
duplicates after a fast reconnect.

### MOSQ_EVT_MESSAGE (asynchronous, fire-and-forget)

Fired on every PUBLISH that passed ACL. The plugin builds an
`IngressMessage{topic, payload, qos, retain, ts}` and publishes it on
a per-device subject:

```
{NATS_SUBJECT_INGRESS_PREFIX}.{orgId}.{assetUUID}
```

JS-Executor's MqttDataConsumer subscribes to `{prefix}.>` so each
device's messages route directly to their downstream filter chain.

Payload size is capped at 900 KiB (NATS default max_payload is 1 MiB;
JSON envelope eats some). Oversized payloads are dropped with a WARN
log identifying the asset and topic — the underlying NATS server
would reject anyway, this surfaces it earlier with full context.

## NATS publishing

The plugin uses **Core NATS** (not JetStream) for publishes. Core has
no server-side ACK — `nats.Conn.Publish` writes to a local outbound
buffer and returns. The flusher goroutine inside `nats-go` drains the
buffer to TCP asynchronously. This is fire-and-forget by design:

- A single `Publish` call costs microseconds.
- The platform's downstream JetStream streams capture the subjects;
  durability happens server-side without producer involvement.
- If NATS is briefly unavailable, `nats-go` buffers up to 16 MiB
  during reconnect (`ReconnectBufSize`); past that, publishes return
  errors which the worker counts as `publishErr` and logs.

Resilience options (`src/connect.go`):

```
MaxReconnects(-1)              never give up
ReconnectWait(1s)              exponential-ish backoff
ReconnectBufSize(16 MiB)       drain on reconnect
NoEcho()                       don't echo our publishes back
FlusherTimeout(50ms)           push small bursts quickly
PingInterval(2m)               detect half-open connections
```

Lifecycle hooks log every disconnect, reconnect, and async error to
the structured `[PLUGIN:Mosquitto]` log so ops sees outage edges.

## AsyncPublisher

`src/nats_publisher.go`. A bounded channel + worker pool that
isolates the broker thread from NATS slowness.

```go
type AsyncPublisher struct {
    queue   chan publishJob
    workers int
    closeMu sync.RWMutex
    ...
}

func (p *AsyncPublisher) Enqueue(subject string, data []byte) bool {
    if !p.started.Load()    { return false }   // pre-Start guard
    p.closeMu.RLock(); defer p.closeMu.RUnlock()
    if p.closed             { return false }
    select {
    case p.queue <- job:   p.enqueued++; return true
    default:               p.dropped++;  return false   // overflow
    }
}
```

Invariants the implementation enforces:

1. **`Enqueue` never blocks.** Non-blocking `select` with `default`,
   so a full channel drops the job. The broker thread never waits
   on NATS.

2. **`closeMu` RWMutex serializes channel close vs send.** A naive
   `closed` flag check + send racing with `close(channel)` would
   panic with "send on closed channel" and crash the broker process.

3. **Worker panic recovery.** A panic inside `nats.Conn.Publish` (nil
   deref under reconnect, malformed subject) is contained by
   `defer recover()` per job. Worker keeps draining; counter
   `panics++`. Shipping an event that crashes the worker would
   eventually fill the channel and silently degrade.

4. **Drain idempotent.** `Drain()` can be called more than once;
   subsequent calls return immediately. The inner `wg.Wait()`
   goroutine leaks at most once per process under timeout.

5. **Counters atomic.** All five (`enqueued`, `published`,
   `publishErr`, `dropped`, `panics`) use `atomic.Uint64`, safe for
   concurrent stat reads from any thread.

## Memory safety across the cgo boundary

Every C string and byte slice is **copied** out of broker-owned
memory before reaching the worker goroutines:

```go
username := C.GoString(C.mosquitto_client_username(client))   // copies
payload  := C.GoBytes(ed.payload, C.int(ed.payloadlen))       // copies
```

Mosquitto frees the source buffers when the callback returns. If we
held aliasing pointers, the worker would read freed memory and the
broker would segfault. Every non-trivial extraction goes through
`C.GoString` / `C.GoBytes` for safety.

## Failure modes the plugin handles

| Failure | Behavior |
|---|---|
| NATS unreachable at startup | `mosquitto_plugin_init` returns `MOSQ_ERR_UNKNOWN`; broker fails to start (loud, immediate) |
| NATS dies after startup | `nats-go` reconnects automatically up to ReconnectBufSize. Past buffer, publishes error and counter `publishErr` grows |
| NATS slow | AsyncPublisher channel fills, `dropped++` per overflow. Broker thread never blocks |
| `Enqueue` before `Start` | Rejected, `dropped++` and `droppedNoStart++` (misuse signal). Items would otherwise sit in channel forever |
| Concurrent `Drain` + `Enqueue` | RWMutex serializes; no panic, no race |
| `nats.Publish` panics | `defer recover()` per worker, `panics++`, worker keeps draining |
| Assets MS down | Auth callout times out → `AuthError` → broker denies CONNECT (fail-closed) |
| Assets MS returns 5xx | Same as above — fail-closed |
| Assets MS returns 401 | `AuthDeny` → broker rejects CONNECT |
| Username with NATS-illegal chars (`.`, `*`, `>`, whitespace) | Ingress publish dropped with WARN log; nothing leaves the plugin |
| Payload > 900 KiB | Dropped with WARN identifying asset + topic + size |
| Plugin compiled against newer Mosquitto than runtime | Plugin v5 API is forward-compatible; events not present at runtime simply never fire |

## Logging

`src/log.go`. Every log line has the prefix `[PLUGIN:Mosquitto]`
followed by a level (`DEBUG INFO  WARN  ERROR`) so ops dashboards can
filter by both. Mosquitto routes plugin stderr to its own log
destination (`stdout` in the container), so collectors see plugin
events alongside broker events.

Default level is `INFO`. `DEBUG` lines are emitted for per-CONNECT
auth decisions (sample for tracing) and per-event details — enabled
explicitly via env override (see `docs/config.md`).

## Observability counters

Plugin-side state is exposed via methods on `AsyncPublisher` and
`AuthClient`:

| Counter | Meaning |
|---|---|
| `EnqueuedCount` | Total events accepted by AsyncPublisher |
| `PublishedCount` | Total successful NATS publishes |
| `PublishErrorCount` | NATS-side errors (timeout, buffer overrun, panic) |
| `DroppedCount` | Total drops — channel full + pre-Start drops |
| `DroppedNoStartCount` | Drops attributed to misuse (Enqueue before Start) |
| `PanicCount` | Recovered worker panics — should be zero |
| `Auth.AllowedCount` | HTTP auth 200s |
| `Auth.DeniedCount` | HTTP auth 401s |
| `Auth.ErrorCount` | HTTP auth infra failures |
| `Auth.RequestCount` | Total auth attempts |

A future expose-via-Prometheus pass can hook these to an HTTP
endpoint inside the plugin process. Today they live in memory only;
inspect via `pprof` or `gdb`-attach if needed.

## Deliberate non-features

What the plugin does **not** do, intentionally:

- **No internal auth cache.** The assets MS owns the cache (Ristretto
  L0). Adding a second cache here would create coherency surface and
  tie cache invalidation to plugin restarts.
- **No QoS 2 message persistence handling.** Mosquitto handles QoS 1+
  retransmits and durable subscriptions via its own persistence
  layer. The plugin only observes the post-ACL `MESSAGE` event.
- **No subscription tracking.** `MOSQ_EVT_SUBSCRIBE` exists in newer
  Mosquitto but the platform derives subscriptions from the device
  state (asset template), not runtime broker state.
- **No JetStream publishes.** Core NATS is enough; durability is the
  job of consumer-side stream config, not the producer.
