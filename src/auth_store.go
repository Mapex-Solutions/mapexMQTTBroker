package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// AuthStore is the read-side of the broker plugin's auth path. The
// broker plugin's MOSQ_EVT_BASIC_AUTH callback hits Get on every
// CONNECT, so the implementation MUST keep p99 latency well below
// the bcrypt cost (~50ms) — otherwise the broker thread serializes
// CONNECTs and a reconnect storm queues at the TCP layer.
//
// The default impl (TieredAuthStore) layers:
//
//	L1 — Pebble on NVMe, embedded KV, ~50µs hit, persists across
//	     plugin restarts when the volume is mounted
//	L2 — MinIO bucket mapex-asset-auth, slim AuthProjection written by
//	     the assets MS on every CRUD; ~10ms hit
//	L3 — HTTP fallback to assets MS /internal/asset-auth/:assetUUID,
//	     last resort when MinIO is degraded; ~50ms warm
//
// Self-healing: every L2 hit writes to L1; every L3 hit writes to L1
// AND assets MS write-throughs to L2 inside its handler. Plugin
// invalidates L1 on FANOUT mapexos.fanout.asset.invalidate; L2 stays
// authoritative because the assets MS is the only writer.
type AuthStore interface {
	// Get returns the AuthEntry for an asset, keyed only by
	// assetUUID — globally unique (Mongo idx_asset_uuid_unique), so
	// L2 key shape is `{assetUUID}.json` and L3 is a path GET.
	// Tenant scoping flows from the entry's OrgId field, not from
	// the wire username.
	//
	// Returns ErrAuthEntryNotFound when the asset does not exist
	// (caller maps to AuthDeny) and ErrAuthStoreUnavailable when
	// every layer was unreachable (caller maps to AuthError,
	// fail-closed).
	Get(ctx context.Context, assetUUID string) (*AuthEntry, error)

	// Invalidate removes the L1 entry for an asset. Idempotent; a
	// missing entry is not an error. Used by the FANOUT consumer
	// when the assets MS publishes an asset.invalidate event so
	// the next CONNECT for that asset re-fetches from L2.
	Invalidate(ctx context.Context, assetUUID string) error

	// Close releases L1 / L2 / L3 resources. Called at plugin
	// cleanup; safe to call multiple times.
	Close() error
}

var (
	// ErrAuthEntryNotFound — the asset doesn't exist in any layer.
	ErrAuthEntryNotFound = errors.New("auth entry not found")

	// ErrAuthStoreUnavailable — every cache layer is unreachable.
	// Caller MUST treat as fail-closed: deny the CONNECT rather
	// than risk admitting an unverified device.
	ErrAuthStoreUnavailable = errors.New("auth store unavailable")
)

// TieredAuthStoreConfig is the operator-supplied wiring. Each layer
// is optional — an absent layer is skipped on Get; tests and pre-
// production deploys may run with just L3 (HTTP) for example.
type TieredAuthStoreConfig struct {
	// L1Path is the Pebble directory on NVMe. Empty disables L1.
	L1Path string

	// L2Endpoint, L2AccessKey, L2SecretKey, L2Bucket configure the
	// MinIO client. Empty endpoint disables L2.
	L2Endpoint  string
	L2AccessKey string
	L2SecretKey string
	L2Bucket    string
	L2UseSSL    bool

	// L3Client falls back to the assets MS HTTP endpoint when L1
	// and L2 miss. Required — without a fallback, a fresh deploy
	// with empty L1 + missing L2 entry would deny every CONNECT.
	L3Client *AuthClient

	// L1TTL bounds how long an L1 entry stays valid without a fresh
	// invalidation. The FANOUT path keeps L1 coherent with Mongo
	// at write time; this TTL is just a safety net for the case
	// where a FANOUT message is missed (broker disconnected from
	// NATS during the publish window).
	L1TTL time.Duration

	// Log surfaces lifecycle + per-request decisions. nil → nopLogger.
	Log Logger
}

// TieredAuthStore is the default AuthStore. Wraps a Pebble database,
// a MinIO client, and an HTTP fallback client behind a single Get
// method that walks the layers in latency order.
type TieredAuthStore struct {
	cfg TieredAuthStoreConfig
	log Logger

	pebbleDB *pebble.DB
	minio    *minio.Client

	// Counters surface the cache layer effectiveness. atomic.Uint64
	// so concurrent Get from the broker thread doesn't race with
	// stat collectors.
	l1Hits        atomic.Uint64
	l1Misses      atomic.Uint64
	l2Hits        atomic.Uint64
	l2Misses      atomic.Uint64
	l3Hits        atomic.Uint64
	l3Misses      atomic.Uint64
	storeErrors   atomic.Uint64
	invalidations atomic.Uint64
}

// pebbleEntry is what we serialize to Pebble. Wraps the AuthEntry
// with metadata (CachedAt) so the L1TTL safety net works without
// pulling time out of the AuthEntry contract.
type pebbleEntry struct {
	Entry    AuthEntry `json:"entry"`
	CachedAt time.Time `json:"cachedAt"`
}

// NewTieredAuthStore opens the Pebble database (if configured),
// constructs the MinIO client (if configured), and returns the store.
// Returns an error only when L3 is missing — every other layer can
// be absent and the store still works (just slower).
func NewTieredAuthStore(cfg TieredAuthStoreConfig) (*TieredAuthStore, error) {
	if cfg.L3Client == nil {
		return nil, errors.New("L3 HTTP client is required")
	}
	if cfg.Log == nil {
		cfg.Log = nopLogger{}
	}
	if cfg.L1TTL <= 0 {
		cfg.L1TTL = 30 * time.Minute
	}

	store := &TieredAuthStore{cfg: cfg, log: cfg.Log}

	if cfg.L1Path != "" {
		db, err := pebble.Open(cfg.L1Path, &pebble.Options{})
		if err != nil {
			return nil, fmt.Errorf("open pebble L1 at %s: %w", cfg.L1Path, err)
		}
		store.pebbleDB = db
		cfg.Log.Info("auth_store: L1 Pebble ready path=%s", cfg.L1Path)
	}

	if cfg.L2Endpoint != "" {
		mc, err := minio.New(cfg.L2Endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.L2AccessKey, cfg.L2SecretKey, ""),
			Secure: cfg.L2UseSSL,
		})
		if err != nil {
			return nil, fmt.Errorf("init minio L2: %w", err)
		}
		store.minio = mc
		cfg.Log.Info("auth_store: L2 MinIO ready endpoint=%s bucket=%s", cfg.L2Endpoint, cfg.L2Bucket)
	}

	return store, nil
}

// Get walks L1 → L2 → L3 in that order. Each hit warms the upstream
// layers so the next request stays on the fast path. A full miss
// (every layer says "not found") returns ErrAuthEntryNotFound; a
// total outage (every layer errors) returns ErrAuthStoreUnavailable.
func (s *TieredAuthStore) Get(ctx context.Context, assetUUID string) (*AuthEntry, error) {
	if assetUUID == "" {
		return nil, ErrAuthEntryNotFound
	}

	short := truncate(assetUUID, 64)

	if entry, ok := s.readL1(assetUUID); ok {
		s.l1Hits.Add(1)
		s.log.Info("auth_store: L1 hit assetUUID=%s orgId=%s", short, entry.OrgId)
		return entry, nil
	}
	s.l1Misses.Add(1)

	if entry, hit, err := s.readL2(ctx, assetUUID); err == nil {
		if hit {
			s.l2Hits.Add(1)
			s.writeL1(assetUUID, entry)
			s.log.Info("auth_store: L2 hit assetUUID=%s orgId=%s warmed=L1", short, entry.OrgId)
			return entry, nil
		}
		s.l2Misses.Add(1)
		s.log.Info("auth_store: L2 miss assetUUID=%s falling_back=L3", short)
	} else {
		s.storeErrors.Add(1)
		s.log.Warn("auth_store: L2 read failed assetUUID=%s err=%v falling_back=L3", short, err)
	}

	entry, err := s.readL3(ctx, assetUUID)
	if err != nil {
		if errors.Is(err, ErrAuthEntryNotFound) {
			s.l3Misses.Add(1)
			s.log.Info("auth_store: L3 not_found assetUUID=%s decision=deny", short)
			return nil, ErrAuthEntryNotFound
		}
		s.l3Misses.Add(1)
		s.storeErrors.Add(1)
		s.log.Warn("auth_store: L3 unavailable assetUUID=%s err=%v decision=fail_closed", short, err)
		return nil, ErrAuthStoreUnavailable
	}
	s.l3Hits.Add(1)
	s.writeL1(assetUUID, entry)
	s.log.Info("auth_store: L3 hit assetUUID=%s orgId=%s warmed=L1 self_healed=true", short, entry.OrgId)
	return entry, nil
}

// Invalidate drops the L1 entry. Safe to call with no L1 configured
// (returns nil). The L2 stays untouched because the assets MS owns
// it; the FANOUT path that drives this invalidation already knows
// to await the next read for the L2 to be re-projected.
func (s *TieredAuthStore) Invalidate(_ context.Context, assetUUID string) error {
	s.invalidations.Add(1)
	if s.pebbleDB == nil || assetUUID == "" {
		return nil
	}
	if err := s.pebbleDB.Delete([]byte(assetUUID), pebble.Sync); err != nil {
		s.log.Warn("auth_store: invalidate L1 failed assetUUID=%s err=%v", truncate(assetUUID, 64), err)
		return err
	}
	s.log.Info("auth_store: invalidated L1 assetUUID=%s next_read=L2", truncate(assetUUID, 64))
	return nil
}

// Close releases pebble + minio resources. Idempotent.
func (s *TieredAuthStore) Close() error {
	if s.pebbleDB != nil {
		err := s.pebbleDB.Close()
		s.pebbleDB = nil
		return err
	}
	return nil
}

// readL1 fetches from Pebble. On expiry-window pass returns hit
// nonetheless; the FANOUT path keeps L1 coherent so a stale TTL is
// rare. False return on missing key OR on expiration.
func (s *TieredAuthStore) readL1(assetUUID string) (*AuthEntry, bool) {
	if s.pebbleDB == nil {
		return nil, false
	}
	value, closer, err := s.pebbleDB.Get([]byte(assetUUID))
	if err != nil {
		// pebble.ErrNotFound is the expected miss; anything else
		// (corruption, IO error) we log + treat as miss to fall
		// through to L2.
		if !errors.Is(err, pebble.ErrNotFound) {
			s.log.Warn("auth_store: L1 read error assetUUID=%s err=%v", truncate(assetUUID, 64), err)
		}
		return nil, false
	}
	defer closer.Close()

	var pe pebbleEntry
	if err := json.Unmarshal(value, &pe); err != nil {
		s.log.Warn("auth_store: L1 decode failed assetUUID=%s err=%v", truncate(assetUUID, 64), err)
		return nil, false
	}
	if time.Since(pe.CachedAt) > s.cfg.L1TTL {
		// TTL safety net — let the upper layers refresh.
		return nil, false
	}
	return &pe.Entry, true
}

// writeL1 persists the entry to Pebble with the current timestamp.
// Best-effort: a failure here just means the next request will hit
// L2 again (acceptable degradation).
func (s *TieredAuthStore) writeL1(assetUUID string, entry *AuthEntry) {
	if s.pebbleDB == nil || entry == nil {
		return
	}
	pe := pebbleEntry{Entry: *entry, CachedAt: time.Now().UTC()}
	data, err := json.Marshal(pe)
	if err != nil {
		s.log.Warn("auth_store: L1 marshal failed assetUUID=%s err=%v", truncate(assetUUID, 64), err)
		return
	}
	if err := s.pebbleDB.Set([]byte(assetUUID), data, pebble.NoSync); err != nil {
		s.log.Warn("auth_store: L1 write failed assetUUID=%s err=%v", truncate(assetUUID, 64), err)
	}
}

// readL2 fetches the AuthProjection JSON object from MinIO and
// projects it into AuthEntry. Returns (entry, true, nil) on hit;
// (nil, false, nil) on missing key; (nil, false, err) on infra error.
//
// Key format: `{assetUUID}.json` — flat, no tenant prefix. Matches
// what the assets MS writes to the dedicated auth bucket
// (`mapex-asset-auth`) on every CRUD; the AssetUUID is globally
// unique so no collision risk.
func (s *TieredAuthStore) readL2(ctx context.Context, assetUUID string) (*AuthEntry, bool, error) {
	if s.minio == nil {
		return nil, false, nil
	}
	objKey := assetUUID + ".json"
	obj, err := s.minio.GetObject(ctx, s.cfg.L2Bucket, objKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("minio get: %w", err)
	}
	defer obj.Close()

	stat, err := obj.Stat()
	if err != nil {
		// minio-go returns NoSuchKey as a typed error; treat as miss.
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("minio stat: %w", err)
	}

	if stat.Size > 1024*1024 {
		// AssetReadModel payloads are small (sub-kB typical); >1MB
		// is corruption or attack — refuse rather than allocate.
		return nil, false, fmt.Errorf("L2 object suspicious size=%d", stat.Size)
	}

	entry, err := DecodeReadModelToAuthEntry(obj)
	if err != nil {
		return nil, false, fmt.Errorf("decode L2 entry: %w", err)
	}
	return entry, true, nil
}

// readL3 falls back to the HTTP assets MS endpoint. Maps the auth
// callout response to AuthEntry. Used when L2 is missing the entry
// (eg. brand-new asset whose CRUD just landed but L2 hasn't caught
// up) or fully unreachable.
//
// The HTTP endpoint returns 200/401 + body; for the AuthStore path
// we need the entry shape, not just a boolean. The assets MS
// internal endpoint returns the entry on cache-miss reads; if the
// endpoint shape is purely status-based (current /auth/user is just
// 200/401), this path will currently fail until the assets MS adds
// a JSON body return on success. Documented as a known constraint
// for the read-only-via-HTTP fallback case.
func (s *TieredAuthStore) readL3(ctx context.Context, assetUUID string) (*AuthEntry, error) {
	if s.cfg.L3Client == nil {
		return nil, ErrAuthStoreUnavailable
	}
	entry, err := s.cfg.L3Client.LookupEntry(ctx, assetUUID)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, ErrAuthEntryNotFound
	}
	return entry, nil
}

// L1HitCount, L1MissCount, etc. expose layer effectiveness.
func (s *TieredAuthStore) L1HitCount() uint64        { return s.l1Hits.Load() }
func (s *TieredAuthStore) L1MissCount() uint64       { return s.l1Misses.Load() }
func (s *TieredAuthStore) L2HitCount() uint64        { return s.l2Hits.Load() }
func (s *TieredAuthStore) L2MissCount() uint64       { return s.l2Misses.Load() }
func (s *TieredAuthStore) L3HitCount() uint64        { return s.l3Hits.Load() }
func (s *TieredAuthStore) L3MissCount() uint64       { return s.l3Misses.Load() }
func (s *TieredAuthStore) StoreErrorCount() uint64   { return s.storeErrors.Load() }
func (s *TieredAuthStore) InvalidationCount() uint64 { return s.invalidations.Load() }
