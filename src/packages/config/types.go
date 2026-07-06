package config

import "time"

// Config aggregates the runtime parameters the plugin reads from
// `plugin_opt_*` directives in mosquitto.conf. All fields are immutable
// after Load; the plugin re-reads only on broker restart (Mosquitto's
// SIGHUP cycle does NOT re-fire mosquitto_plugin_init).
type Config struct {
	// NatsURL is the URL the plugin connects to. Required.
	// Example: "nats://nats:4222".
	NatsURL string

	// SubjectPresence is the full NATS subject (env-prefixed) that receives
	// connect + disconnect advisories. Both events share the subject; the
	// payload's Event field discriminates. The operator MUST supply an
	// env-prefixed value because the plugin is environment-agnostic — it
	// does not read GO_ENV.
	SubjectPresence string

	// SubjectIngressPrefix is the leading subject token the plugin uses to
	// build per-message ingress subjects. The plugin appends
	// ".{orgId}.{assetUUID}" using the trusted orgId from the auth
	// projection so the js-executor's wildcard subscription on "{prefix}.>"
	// routes each device's messages to the right downstream filter.
	SubjectIngressPrefix string

	// AuthURL is the full HTTP endpoint of the assets service auth callout.
	// The plugin fetches AuthEntry records here on L3 miss. Required.
	AuthURL string

	// AuthAPIKey is the shared secret used on the X-API-Key header for the
	// auth callout. Required.
	AuthAPIKey string

	// AuthTimeout caps each callout. Default 5s (DefaultAuthTimeout).
	AuthTimeout time.Duration

	// WorkerPoolSize sets the number of goroutines draining the publish
	// buffer. Default 4 — enough to absorb bursts without over-subscribing
	// CPU on a single-broker host.
	WorkerPoolSize int

	// BufferSize bounds the publish channel. When full, new events are
	// dropped (counter incremented). Default 10_000 — about 600
	// connect/disconnect advisories per second sustained without dropping.
	BufferSize int

	// CacheL1Path is the Pebble directory on NVMe for the L1 cache. Empty
	// disables L1 — every Get walks straight to L2/L3. Operator typically
	// mounts a Docker volume here so the cache survives container restart.
	CacheL1Path string

	// CacheL1TTL is the safety-net expiry for L1 entries. The fanout
	// consumer drives invalidation on every CRUD; this TTL catches the case
	// where the broker missed a fanout message. Default 30min.
	CacheL1TTL time.Duration

	// CacheL2Endpoint, CacheL2AccessKey, CacheL2SecretKey configure the
	// MinIO client used for L2. CacheL2Endpoint empty disables L2. The
	// bucket name is NOT operator-configurable — it is fixed to
	// `mapex-asset-auth` (DefaultCacheL2Bucket), bound by the platform
	// contract with the assets service which writes the AuthProjection
	// under key `{assetUUID}.json` in that bucket.
	CacheL2Endpoint  string
	CacheL2AccessKey string
	CacheL2SecretKey string
	CacheL2Bucket    string
	CacheL2UseSSL    bool
	// CacheL2AuthIsNeeded selects static keys (true) vs ambient IAM (false) for
	// the L2 object-store client.
	CacheL2AuthIsNeeded bool

	// FanoutInvalidateSubject is the NATS subject the broker plugin listens
	// on to drop L1 entries when the assets service publishes an
	// invalidation. Default "mapexos.fanout.asset.invalidate".
	FanoutInvalidateSubject string

	// SubjectOTAStatus is the STATIC env-prefixed subject the plugin
	// publishes OTA status advisories to (device identity in the payload).
	// The operator overrides the env prefix like SubjectPresence.
	SubjectOTAStatus string

	// SubjectDownlink / StreamDownlink identify the STATIC env-prefixed
	// mqtt.downlink subject and its JetStream stream (provisioned by the
	// deployment nats-init; the plugin binds a durable consumer).
	SubjectDownlink string
	StreamDownlink  string

	// DownlinkDurable / DownlinkQueue name the durable consumer and its
	// queue group — one broker node handles each command.
	DownlinkDurable string
	DownlinkQueue   string
}
