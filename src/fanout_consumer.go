package broker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/nats-io/nats.go"
)

// FanoutInvalidatePayload is the body the assets MS publishes on
// `mapexos.fanout.asset.invalidate`. The plugin only needs the
// AssetUUID to drop its L1 entry; other fields the assets MS may
// include (orgId, etc.) are ignored at this layer.
type FanoutInvalidatePayload struct {
	AssetUUID string `json:"assetUUID"`
}

// FanoutConsumerConfig is the wiring for the cache-invalidation
// subscriber. The plugin runs ONE consumer per process; multiple
// brokers each get their own subscription on the same subject and
// every broker invalidates its own L1 independently.
type FanoutConsumerConfig struct {
	// Subject is the NATS subject the assets MS publishes to. Default
	// "mapexos.fanout.asset.invalidate"; operators can override per
	// environment so multiple deploys can share a NATS cluster.
	Subject string

	// Conn is the NATS connection to subscribe on. Required.
	Conn *nats.Conn

	// Store is the AuthStore whose L1 will be invalidated. Required.
	Store AuthStore

	// Log surfaces lifecycle + per-message decisions. nil → nopLogger.
	Log Logger
}

// FanoutConsumer subscribes to the assets-invalidate subject and
// drops L1 entries on every message. Lifecycle:
//
//	NewFanoutConsumer  →  ready, not subscribed
//	Start              →  subscription active
//	Stop               →  unsubscribe + drain in-flight handlers
//
// A consumer is safe to instantiate at plugin init time, even before
// the AuthStore is fully ready; Start should be called only after
// every dependency is operational.
type FanoutConsumer struct {
	cfg FanoutConsumerConfig

	mu     sync.Mutex
	sub    *nats.Subscription
	closed bool

	processed uint64
	dropped   uint64
}

// NewFanoutConsumer validates the config and constructs the consumer.
// Errors when required deps (Conn, Store) are nil; defaults the
// subject and logger.
func NewFanoutConsumer(cfg FanoutConsumerConfig) (*FanoutConsumer, error) {
	if cfg.Conn == nil {
		return nil, errors.New("nats conn is required")
	}
	if cfg.Store == nil {
		return nil, errors.New("auth store is required")
	}
	if cfg.Subject == "" {
		cfg.Subject = "mapexos.fanout.asset.invalidate"
	}
	if cfg.Log == nil {
		cfg.Log = nopLogger{}
	}
	return &FanoutConsumer{cfg: cfg}, nil
}

// Start subscribes on the configured subject. Each delivery
// triggers a single AuthStore.Invalidate call. NATS Core delivery
// semantics are at-most-once (message lost on broker restart);
// that's acceptable here because the L1 TTL safety net catches any
// missed invalidation within ~30 minutes.
func (c *FanoutConsumer) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sub != nil {
		return errors.New("fanout consumer already started")
	}
	if c.closed {
		return errors.New("fanout consumer is closed")
	}

	sub, err := c.cfg.Conn.Subscribe(c.cfg.Subject, c.handle)
	if err != nil {
		return err
	}
	c.sub = sub
	c.cfg.Log.Info("fanout consumer started subject=%s", c.cfg.Subject)
	return nil
}

// Stop unsubscribes and waits for in-flight handlers to drain (up
// to a reasonable bound). Idempotent.
func (c *FanoutConsumer) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.sub == nil {
		return nil
	}
	if err := c.sub.Drain(); err != nil {
		c.cfg.Log.Warn("fanout consumer drain failed: %v", err)
	}
	c.sub = nil
	c.cfg.Log.Info("fanout consumer stopped processed=%d dropped=%d", c.processed, c.dropped)
	return nil
}

// handle parses the payload and calls Invalidate. Errors at any
// stage (malformed JSON, missing assetUUID, store error) are
// counted and logged but never propagated — losing a single
// invalidation means at most one stale L1 entry until TTL expires
// or the next CRUD touches it.
func (c *FanoutConsumer) handle(msg *nats.Msg) {
	var payload FanoutInvalidatePayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		c.dropped++
		c.cfg.Log.Warn("fanout: malformed payload err=%v", err)
		return
	}
	if payload.AssetUUID == "" {
		c.dropped++
		c.cfg.Log.Warn("fanout: empty assetUUID, dropping")
		return
	}
	if err := c.cfg.Store.Invalidate(context.Background(), payload.AssetUUID); err != nil {
		c.dropped++
		c.cfg.Log.Warn("fanout: invalidate failed assetUUID=%s err=%v",
			truncate(payload.AssetUUID, 64), err)
		return
	}
	c.processed++
	c.cfg.Log.Debug("fanout: invalidated assetUUID=%s", truncate(payload.AssetUUID, 64))
}

// ProcessedCount returns successful invalidations since Start.
func (c *FanoutConsumer) ProcessedCount() uint64 { return c.processed }

// DroppedCount returns parse / store failures since Start. Should
// stay zero in healthy operation; non-zero indicates either the
// assets MS publishing malformed payloads (contract drift) or
// AuthStore unhealthy.
func (c *FanoutConsumer) DroppedCount() uint64 { return c.dropped }
