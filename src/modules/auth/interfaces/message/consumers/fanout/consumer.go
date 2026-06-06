package fanout

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nats-io/nats.go"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/dtos"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/textutil"
)

// NewConsumer validates the config and constructs the consumer. Errors when
// required deps (Conn, Store) are nil; defaults the subject and logger.
func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	if cfg.Conn == nil {
		return nil, errors.New("nats conn is required")
	}
	if cfg.Store == nil {
		return nil, errors.New("auth store is required")
	}
	if cfg.Subject == "" {
		cfg.Subject = defaultSubject
	}
	if cfg.Log == nil {
		cfg.Log = logging.NopLogger{}
	}
	return &Consumer{cfg: cfg}, nil
}

// Start subscribes on the configured subject. Each delivery triggers a
// single AuthStore.Invalidate call. NATS Core delivery semantics are
// at-most-once (message lost on broker restart); that's acceptable here
// because the L1 TTL safety net catches any missed invalidation.
func (c *Consumer) Start() error {
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
	c.cfg.Log.Info("[CONSUMER:Fanout] started subject=%s", c.cfg.Subject)
	return nil
}

// Stop unsubscribes and waits for in-flight handlers to drain (up to a
// reasonable bound). Idempotent.
func (c *Consumer) Stop() error {
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
		c.cfg.Log.Warn("[CONSUMER:Fanout] drain failed: %v", err)
	}
	c.sub = nil
	c.cfg.Log.Info("[CONSUMER:Fanout] stopped processed=%d dropped=%d", c.processed, c.dropped)
	return nil
}

// ProcessedCount returns successful invalidations since Start.
func (c *Consumer) ProcessedCount() uint64 { return c.processed }

// DroppedCount returns parse / store failures since Start. Should stay zero
// in healthy operation; non-zero indicates either the assets service
// publishing malformed payloads (contract drift) or AuthStore unhealthy.
func (c *Consumer) DroppedCount() uint64 { return c.dropped }

// handle parses the payload and calls Invalidate. Errors at any stage
// (malformed JSON, missing assetUUID, store error) are counted and logged
// but never propagated — losing a single invalidation means at most one
// stale L1 entry until TTL expires or the next CRUD touches it.
func (c *Consumer) handle(msg *nats.Msg) {
	var payload dtos.FanoutInvalidatePayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		c.dropped++
		c.cfg.Log.Warn("[CONSUMER:Fanout] malformed payload err=%v", err)
		return
	}
	if payload.AssetUUID == "" {
		c.dropped++
		c.cfg.Log.Warn("[CONSUMER:Fanout] empty assetUUID, dropping")
		return
	}
	if err := c.cfg.Store.Invalidate(context.Background(), payload.AssetUUID); err != nil {
		c.dropped++
		c.cfg.Log.Warn("[CONSUMER:Fanout] invalidate failed assetUUID=%s err=%v",
			textutil.Truncate(payload.AssetUUID, 64), err)
		return
	}
	c.processed++
	c.cfg.Log.Debug("[CONSUMER:Fanout] invalidated assetUUID=%s", textutil.Truncate(payload.AssetUUID, 64))
}
