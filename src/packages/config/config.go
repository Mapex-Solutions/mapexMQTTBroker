package config

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Load parses an opts map into a Config and validates required fields. The
// opts map is what Mosquitto provides to mosquitto_plugin_init — the cgo
// entry point converts the C array into a Go map and hands it here so all
// parsing logic stays in Go, fully unit-testable.
//
// Required keys:
//   - "nats_url"
//   - "auth_url"      assets service callout, e.g. http://assets:5002/internal/asset_auth
//   - "auth_api_key"  shared secret on the X-API-Key header
//
// Optional keys fall back to defaults when absent or empty. Invalid integer
// values fall back to defaults rather than failing — the plugin should
// always boot, and operator misconfiguration is surfaced via the broker log
// instead of a crash.
func Load(opts map[string]string) (Config, error) {
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

// stringOrDefault returns the trimmed value at key, or fallback when the key
// is missing or whitespace-only. Defensive against operators who
// quote-escape values in mosquitto.conf and end up with spaces.
func stringOrDefault(opts map[string]string, key, fallback string) string {
	if v := strings.TrimSpace(opts[key]); v != "" {
		return v
	}
	return fallback
}

// intOrDefault parses the value at key as a positive int. Returns fallback
// when missing, malformed, or non-positive — see Load rationale on why this
// is non-fatal.
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
