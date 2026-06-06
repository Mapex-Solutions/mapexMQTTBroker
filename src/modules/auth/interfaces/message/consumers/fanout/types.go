package fanout

import (
	"sync"

	"github.com/nats-io/nats.go"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// ConsumerConfig is the wiring for the cache-invalidation subscriber. The
// plugin runs ONE consumer per process; multiple brokers each get their own
// subscription on the same subject and every broker invalidates its own L1
// independently.
type ConsumerConfig struct {
	// Subject is the NATS subject the assets service publishes to. Default
	// "mapexos.fanout.asset.invalidate"; operators can override per
	// environment so multiple deploys can share a NATS cluster.
	Subject string

	// Conn is the NATS connection to subscribe on. Required.
	Conn *nats.Conn

	// Store is the AuthStore whose L1 will be invalidated. Required.
	Store ports.AuthStore

	// Log surfaces lifecycle + per-message decisions. nil → NopLogger.
	Log logging.Logger
}

// Consumer subscribes to the assets-invalidate subject and drops L1 entries
// on every message. Lifecycle:
//
//	NewConsumer  →  ready, not subscribed
//	Start        →  subscription active
//	Stop         →  unsubscribe + drain in-flight handlers
//
// A consumer is safe to instantiate at plugin init time, even before the
// AuthStore is fully ready; Start should be called only after every
// dependency is operational.
type Consumer struct {
	cfg ConsumerConfig

	mu     sync.Mutex
	sub    *nats.Subscription
	closed bool

	processed uint64
	dropped   uint64
}
