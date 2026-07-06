package tiered

import (
	"sync/atomic"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/minio/minio-go/v7"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/entities"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// TieredAuthStoreConfig is the operator-supplied wiring. Each layer is
// optional — an absent layer is skipped on Get; tests and pre-production
// deploys may run with just L3 (HTTP) for example.
type TieredAuthStoreConfig struct {
	// L1Path is the Pebble directory on NVMe. Empty disables L1.
	L1Path string

	// L2Endpoint, L2AccessKey, L2SecretKey, L2Bucket configure the MinIO
	// client. Empty endpoint disables L2.
	L2Endpoint  string
	L2AccessKey string
	L2SecretKey string
	L2Bucket    string
	L2UseSSL    bool

	// L2AuthIsNeeded selects the credential provider. When true, the L2 client
	// uses the static L2AccessKey/L2SecretKey; when false, it uses the ambient
	// IAM credential chain (EC2/ECS instance profile) and the keys are ignored.
	L2AuthIsNeeded bool

	// L3Client falls back to the assets service HTTP endpoint when L1 and
	// L2 miss. Required — without a fallback, a fresh deploy with empty L1
	// + missing L2 entry would deny every CONNECT.
	L3Client ports.AuthLookup

	// L1TTL bounds how long an L1 entry stays valid without a fresh
	// invalidation. The fanout path keeps L1 coherent with Mongo at write
	// time; this TTL is just a safety net for a missed fanout message.
	L1TTL time.Duration

	// Log surfaces lifecycle + per-request decisions. nil → NopLogger.
	Log logging.Logger
}

// TieredAuthStore is the default AuthStore. Wraps a Pebble database, a
// MinIO client, and an HTTP fallback client behind a single Get method that
// walks the layers in latency order.
//
// Layering:
//
//	L1 — Pebble on NVMe, embedded KV, ~50µs hit, persists across restarts
//	L2 — MinIO bucket mapex-asset-auth, slim AuthProjection; ~10ms hit
//	L3 — HTTP fallback to assets service; last resort, ~50ms warm
//
// Self-healing: every L2 hit writes to L1; every L3 hit writes to L1. The
// plugin invalidates L1 on the asset-invalidate fanout; L2 stays
// authoritative because the assets service is the only writer.
type TieredAuthStore struct {
	cfg TieredAuthStoreConfig
	log logging.Logger

	pebbleDB *pebble.DB
	minio    *minio.Client

	// Counters surface cache layer effectiveness. atomic.Uint64 so a
	// concurrent Get from the broker thread doesn't race with stat
	// collectors.
	l1Hits        atomic.Uint64
	l1Misses      atomic.Uint64
	l2Hits        atomic.Uint64
	l2Misses      atomic.Uint64
	l3Hits        atomic.Uint64
	l3Misses      atomic.Uint64
	storeErrors   atomic.Uint64
	invalidations atomic.Uint64
}

// pebbleEntry is what we serialize to Pebble. Wraps the AuthEntry with
// metadata (CachedAt) so the L1TTL safety net works without pulling time
// into the AuthEntry contract.
type pebbleEntry struct {
	Entry    entities.AuthEntry `json:"entry"`
	CachedAt time.Time          `json:"cachedAt"`
}
