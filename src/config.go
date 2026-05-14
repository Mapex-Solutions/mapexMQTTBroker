package broker

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Config aggregates the runtime parameters the plugin reads from
// `plugin_opt_*` directives in mosquitto.conf. All fields are immutable
// after Load; the plugin re-reads only on broker restart (Mosquitto's
// SIGHUP cycle does NOT re-fire mosquitto_plugin_init).
type Config struct {
	// NatsURL is the URL the plugin connects to. Required.
	// Example: "nats://nats:4222".
	NatsURL string

	// SubjectPresence is the full NATS subject (env-prefixed) that
	// receives connect + disconnect advisories. Both events share
	// the subject; the payload's Event field discriminates.
	// Platform value: "{env}.mapexos.mqtt.presence.advisory"
	// (e.g. "dev.mapexos.mqtt.presence.advisory"). The operator MUST
	// supply an env-prefixed value because the plugin is environment-
	// agnostic — it does not read GO_ENV.
	SubjectPresence string

	// SubjectIngressPrefix is the leading subject token the plugin
	// uses to build per-message ingress subjects. The plugin appends
	// ".{orgId}.{assetUUID}" using the trusted orgId from the auth
	// projection (carried via the session map) so the JS-Executor
	// MqttDataConsumer's wildcard subscription on "{prefix}.>" routes
	// each device's messages to the right downstream filter. Platform
	// value: "{env}.mapexos.mqtt.data" (e.g. "dev.mapexos.mqtt.data").
	SubjectIngressPrefix string

	// AuthURL is the full HTTP endpoint of the assets MS auth
	// callout. The plugin POSTs every CONNECT here. Required.
	AuthURL string

	// AuthAPIKey is the shared secret used on the X-API-Key header
	// for the auth callout. Required.
	AuthAPIKey string

	// AuthTimeout caps each callout. Default 5s (set via
	// DefaultAuthTimeout).
	AuthTimeout time.Duration

	// WorkerPoolSize sets the number of goroutines draining the
	// publish buffer. Default 4 — enough to absorb bursts without
	// over-subscribing CPU on a single-broker host.
	WorkerPoolSize int

	// BufferSize bounds the publish channel. When full, new events
	// are dropped (counter incremented). Default 10_000 — about 600
	// connect/disconnect advisories per second sustained without
	// dropping.
	BufferSize int

	// CacheL1Path is the Pebble directory on NVMe for the L1 cache.
	// Empty disables L1 — every Get walks straight to L2/L3.
	// Operator typically mounts a Docker volume here so the cache
	// survives container restart.
	CacheL1Path string

	// CacheL1TTL is the safety-net expiry for L1 entries. The
	// FANOUT consumer drives invalidation on every CRUD; this TTL
	// catches the case where the broker missed a FANOUT message
	// (NATS disconnect during the publish window). Default 30min.
	CacheL1TTL time.Duration

	// CacheL2Endpoint, CacheL2AccessKey, CacheL2SecretKey configure
	// the MinIO client used for L2. CacheL2Endpoint empty disables L2.
	// The bucket name is NOT operator-configurable — it is fixed to
	// `mapex-asset-auth` (DefaultCacheL2Bucket), bound by the platform
	// contract with the assets MS which writes the AuthProjection
	// under key `{assetUUID}.json` in that bucket.
	CacheL2Endpoint  string
	CacheL2AccessKey string
	CacheL2SecretKey string
	CacheL2Bucket    string
	CacheL2UseSSL    bool

	// FanoutInvalidateSubject is the NATS subject the broker plugin
	// listens on to drop L1 entries when the assets MS publishes an
	// invalidation. Default "mapexos.fanout.asset.invalidate".
	FanoutInvalidateSubject string
}

// Default values applied when the operator does not supply a
// `plugin_opt_*` directive. The subjects default to the dev-environment
// values so a fresh container boots with the dev stack working out of
// the box; operators in other environments override via the conf file.
const (
	DefaultSubjectPresence         = "dev.mapexos.mqtt.presence.advisory"
	DefaultSubjectIngressPrefix    = "dev.mapexos.mqtt.data"
	DefaultWorkerPoolSize          = 4
	DefaultBufferSize              = 10_000
	DefaultAuthTimeout             = 5 * time.Second
	DefaultCacheL1Path             = "/var/cache/mqtt"
	DefaultCacheL1TTL              = 30 * time.Minute
	DefaultCacheL2Bucket           = "mapex-asset-auth"
	DefaultFanoutInvalidateSubject = "mapexos.fanout.asset.invalidate"
)

// LoadConfig parses an opts map into a Config and validates required
// fields. The opts map is what Mosquitto provides to
// mosquitto_plugin_init — the cgo entry point converts the C array
// into a Go map and hands it to this function so all parsing logic
// stays in Go, fully unit-testable.
//
// Required keys:
//   - "nats_url"
//   - "auth_url"      assets MS callout, e.g. http://assets:5002/internal/asset-auth
//   - "auth_api_key"  shared secret on the X-API-Key header
//
// Optional keys (defaults applied when absent or empty):
//   - "nats_subject_presence"        → DefaultSubjectPresence
//   - "nats_subject_ingress_prefix"  → DefaultSubjectIngressPrefix
//   - "auth_timeout_seconds"         → 5
//   - "worker_pool_size"             → DefaultWorkerPoolSize
//   - "buffer_size"                  → DefaultBufferSize
//
// Invalid integer values fall back to defaults rather than failing —
// the plugin should always boot, and operator misconfiguration is
// surfaced via the broker log instead of a crash.
func LoadConfig(opts map[string]string) (Config, error) {
	natsURL := strings.TrimSpace(opts["nats_url"])
	if natsURL == "" {
		return Config{}, errors.New("plugin_opt_nats_url is required")
	}
	authURL := strings.TrimSpace(opts["auth_url"])
	if authURL == "" {
		return Config{}, errors.New("plugin_opt_auth_url is required")
	}
	authKey := strings.TrimSpace(opts["auth_api_key"])
	if authKey == "" {
		return Config{}, errors.New("plugin_opt_auth_api_key is required")
	}

	authTimeout := DefaultAuthTimeout
	if secs := intOrDefault(opts, "auth_timeout_seconds", 0); secs > 0 {
		authTimeout = time.Duration(secs) * time.Second
	}

	cacheL1TTL := DefaultCacheL1TTL
	if mins := intOrDefault(opts, "cache_l1_ttl_minutes", 0); mins > 0 {
		cacheL1TTL = time.Duration(mins) * time.Minute
	}

	cfg := Config{
		NatsURL:                 natsURL,
		SubjectPresence:         stringOrDefault(opts, "nats_subject_presence", DefaultSubjectPresence),
		SubjectIngressPrefix:    stringOrDefault(opts, "nats_subject_ingress_prefix", DefaultSubjectIngressPrefix),
		AuthURL:                 authURL,
		AuthAPIKey:              authKey,
		AuthTimeout:             authTimeout,
		WorkerPoolSize:          intOrDefault(opts, "worker_pool_size", DefaultWorkerPoolSize),
		BufferSize:              intOrDefault(opts, "buffer_size", DefaultBufferSize),
		CacheL1Path:             stringOrDefault(opts, "cache_l1_path", DefaultCacheL1Path),
		CacheL1TTL:              cacheL1TTL,
		CacheL2Endpoint:         strings.TrimSpace(opts["cache_l2_endpoint"]),
		CacheL2AccessKey:        strings.TrimSpace(opts["cache_l2_access_key"]),
		CacheL2SecretKey:        strings.TrimSpace(opts["cache_l2_secret_key"]),
		CacheL2Bucket:           DefaultCacheL2Bucket, // fixed by platform contract — see Config comment.
		CacheL2UseSSL:           strings.EqualFold(strings.TrimSpace(opts["cache_l2_use_ssl"]), "true"),
		FanoutInvalidateSubject: stringOrDefault(opts, "fanout_invalidate_subject", DefaultFanoutInvalidateSubject),
	}
	return cfg, nil
}

// stringOrDefault returns the trimmed value at key, or fallback when
// the key is missing or whitespace-only. Defensive against operators
// who quote-escape values in mosquitto.conf and end up with spaces.
func stringOrDefault(opts map[string]string, key, fallback string) string {
	if v := strings.TrimSpace(opts[key]); v != "" {
		return v
	}
	return fallback
}

// intOrDefault parses the value at key as a positive int. Returns
// fallback when missing, malformed, or non-positive — see LoadConfig
// rationale on why this is non-fatal.
func intOrDefault(opts map[string]string, key string, fallback int) int {
	v := strings.TrimSpace(opts[key])
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
