package tiered

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/dtos"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/entities"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/textutil"
)

var _ ports.AuthStore = (*TieredAuthStore)(nil)

// NewTieredAuthStore opens the Pebble database (if configured), constructs
// the MinIO client (if configured), and returns the store. Returns an error
// only when L3 is missing — every other layer can be absent and the store
// still works (just slower).
func NewTieredAuthStore(cfg TieredAuthStoreConfig) (*TieredAuthStore, error) {
	if cfg.L3Client == nil {
		return nil, errors.New("L3 HTTP client is required")
	}
	if cfg.Log == nil {
		cfg.Log = logging.NopLogger{}
	}
	if cfg.L1TTL <= 0 {
		cfg.L1TTL = defaultL1TTL
	}

	store := &TieredAuthStore{cfg: cfg, log: cfg.Log}

	if cfg.L1Path != "" {
		db, err := pebble.Open(cfg.L1Path, &pebble.Options{})
		if err != nil {
			return nil, fmt.Errorf("open pebble L1 at %s: %w", cfg.L1Path, err)
		}
		store.pebbleDB = db
		cfg.Log.Info("[INFRA:AuthStore] L1 Pebble ready path=%s", cfg.L1Path)
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
		cfg.Log.Info("[INFRA:AuthStore] L2 MinIO ready endpoint=%s bucket=%s", cfg.L2Endpoint, cfg.L2Bucket)
	}

	return store, nil
}

// Get walks L1 → L2 → L3 in that order. Each hit warms the upstream layers
// so the next request stays on the fast path. A full miss (every layer says
// "not found") returns ErrAuthEntryNotFound; a total outage (every layer
// errors) returns ErrAuthStoreUnavailable.
func (s *TieredAuthStore) Get(ctx context.Context, assetUUID string) (*entities.AuthEntry, error) {
	if assetUUID == "" {
		return nil, ports.ErrAuthEntryNotFound
	}

	short := textutil.Truncate(assetUUID, 64)

	if entry, ok := s.readL1(assetUUID); ok {
		s.l1Hits.Add(1)
		s.log.Info("[INFRA:AuthStore] L1 hit assetUUID=%s orgId=%s", short, entry.OrgId)
		return entry, nil
	}
	s.l1Misses.Add(1)

	if entry, hit, err := s.readL2(ctx, assetUUID); err == nil {
		if hit {
			s.l2Hits.Add(1)
			s.writeL1(assetUUID, entry)
			s.log.Info("[INFRA:AuthStore] L2 hit assetUUID=%s orgId=%s warmed=L1", short, entry.OrgId)
			return entry, nil
		}
		s.l2Misses.Add(1)
		s.log.Info("[INFRA:AuthStore] L2 miss assetUUID=%s falling_back=L3", short)
	} else {
		s.storeErrors.Add(1)
		s.log.Warn("[INFRA:AuthStore] L2 read failed assetUUID=%s err=%v falling_back=L3", short, err)
	}

	entry, err := s.readL3(ctx, assetUUID)
	if err != nil {
		if errors.Is(err, ports.ErrAuthEntryNotFound) {
			s.l3Misses.Add(1)
			s.log.Info("[INFRA:AuthStore] L3 not_found assetUUID=%s decision=deny", short)
			return nil, ports.ErrAuthEntryNotFound
		}
		s.l3Misses.Add(1)
		s.storeErrors.Add(1)
		s.log.Warn("[INFRA:AuthStore] L3 unavailable assetUUID=%s err=%v decision=fail_closed", short, err)
		return nil, ports.ErrAuthStoreUnavailable
	}
	s.l3Hits.Add(1)
	s.writeL1(assetUUID, entry)
	s.log.Info("[INFRA:AuthStore] L3 hit assetUUID=%s orgId=%s warmed=L1 self_healed=true", short, entry.OrgId)
	return entry, nil
}

// Invalidate drops the L1 entry. Safe to call with no L1 configured
// (returns nil). The L2 stays untouched because the assets service owns it;
// the fanout path that drives this invalidation already knows to await the
// next read for the L2 to be re-projected.
func (s *TieredAuthStore) Invalidate(_ context.Context, assetUUID string) error {
	s.invalidations.Add(1)
	if s.pebbleDB == nil || assetUUID == "" {
		return nil
	}
	if err := s.pebbleDB.Delete([]byte(assetUUID), pebble.Sync); err != nil {
		s.log.Warn("[INFRA:AuthStore] invalidate L1 failed assetUUID=%s err=%v", textutil.Truncate(assetUUID, 64), err)
		return err
	}
	s.log.Info("[INFRA:AuthStore] invalidated L1 assetUUID=%s next_read=L2", textutil.Truncate(assetUUID, 64))
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

// readL1 fetches from Pebble. False return on missing key OR on expiration —
// the fanout path keeps L1 coherent so a stale TTL is rare.
func (s *TieredAuthStore) readL1(assetUUID string) (*entities.AuthEntry, bool) {
	if s.pebbleDB == nil {
		return nil, false
	}
	value, closer, err := s.pebbleDB.Get([]byte(assetUUID))
	if err != nil {
		// pebble.ErrNotFound is the expected miss; anything else
		// (corruption, IO error) we log + treat as miss to fall through to
		// L2.
		if !errors.Is(err, pebble.ErrNotFound) {
			s.log.Warn("[INFRA:AuthStore] L1 read error assetUUID=%s err=%v", textutil.Truncate(assetUUID, 64), err)
		}
		return nil, false
	}
	defer closer.Close()

	var pe pebbleEntry
	if err := json.Unmarshal(value, &pe); err != nil {
		s.log.Warn("[INFRA:AuthStore] L1 decode failed assetUUID=%s err=%v", textutil.Truncate(assetUUID, 64), err)
		return nil, false
	}
	if time.Since(pe.CachedAt) > s.cfg.L1TTL {
		// TTL safety net — let the upper layers refresh.
		return nil, false
	}
	return &pe.Entry, true
}

// writeL1 persists the entry to Pebble with the current timestamp.
// Best-effort: a failure here just means the next request will hit L2 again
// (acceptable degradation).
func (s *TieredAuthStore) writeL1(assetUUID string, entry *entities.AuthEntry) {
	if s.pebbleDB == nil || entry == nil {
		return
	}
	pe := pebbleEntry{Entry: *entry, CachedAt: time.Now().UTC()}
	data, err := json.Marshal(pe)
	if err != nil {
		s.log.Warn("[INFRA:AuthStore] L1 marshal failed assetUUID=%s err=%v", textutil.Truncate(assetUUID, 64), err)
		return
	}
	if err := s.pebbleDB.Set([]byte(assetUUID), data, pebble.NoSync); err != nil {
		s.log.Warn("[INFRA:AuthStore] L1 write failed assetUUID=%s err=%v", textutil.Truncate(assetUUID, 64), err)
	}
}

// readL2 fetches the AuthProjection JSON object from MinIO and projects it
// into AuthEntry. Returns (entry, true, nil) on hit; (nil, false, nil) on
// missing key; (nil, false, err) on infra error.
//
// Key format: `{assetUUID}.json` — flat, no tenant prefix. Matches what the
// assets service writes to the dedicated auth bucket; the AssetUUID is
// globally unique so no collision risk.
func (s *TieredAuthStore) readL2(ctx context.Context, assetUUID string) (*entities.AuthEntry, bool, error) {
	if s.minio == nil {
		return nil, false, nil
	}
	objKey := assetUUID + l2ObjectKeySuffix
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

	if stat.Size > l2MaxObjectSize {
		return nil, false, fmt.Errorf("L2 object suspicious size=%d", stat.Size)
	}

	entry, err := dtos.DecodeReadModelToAuthEntry(obj)
	if err != nil {
		return nil, false, fmt.Errorf("decode L2 entry: %w", err)
	}
	return entry, true, nil
}

// readL3 falls back to the HTTP assets service endpoint. Used when L2 is
// missing the entry (e.g. brand-new asset whose CRUD just landed but L2
// hasn't caught up) or fully unreachable.
func (s *TieredAuthStore) readL3(ctx context.Context, assetUUID string) (*entities.AuthEntry, error) {
	if s.cfg.L3Client == nil {
		return nil, ports.ErrAuthStoreUnavailable
	}
	entry, err := s.cfg.L3Client.LookupEntry(ctx, assetUUID)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, ports.ErrAuthEntryNotFound
	}
	return entry, nil
}

// L1HitCount and the sibling counters expose layer effectiveness.
func (s *TieredAuthStore) L1HitCount() uint64        { return s.l1Hits.Load() }
func (s *TieredAuthStore) L1MissCount() uint64       { return s.l1Misses.Load() }
func (s *TieredAuthStore) L2HitCount() uint64        { return s.l2Hits.Load() }
func (s *TieredAuthStore) L2MissCount() uint64       { return s.l2Misses.Load() }
func (s *TieredAuthStore) L3HitCount() uint64        { return s.l3Hits.Load() }
func (s *TieredAuthStore) L3MissCount() uint64       { return s.l3Misses.Load() }
func (s *TieredAuthStore) StoreErrorCount() uint64   { return s.storeErrors.Load() }
func (s *TieredAuthStore) InvalidationCount() uint64 { return s.invalidations.Load() }
