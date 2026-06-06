package natsbus

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// NatsPublisher is the minimal contract used to push bytes to NATS. The real
// implementation is *nats.Conn (its Publish method has the same shape); tests
// inject a fake to assert publish semantics without a live NATS.
type NatsPublisher interface {
	Publish(subject string, data []byte) error
}

// NATS resilience defaults applied at every Connect call. The values are
// deliberately tuned for the broker plugin's "stay alive at all costs"
// stance — the plugin runs in-process inside Mosquitto, so a NATS outage
// MUST never propagate as a process crash.
const (
	// natsMaxReconnects = -1 means infinite. We never give up — the
	// alternative is the plugin going dark forever after a transient NATS
	// hiccup, which is worse than a few seconds of dropped events.
	natsMaxReconnects = -1

	// natsReconnectWait is the back-off between reconnect attempts. 1s is
	// short enough to recover from a NATS rolling restart in a few
	// attempts, long enough to avoid a hot loop.
	natsReconnectWait = 1 * time.Second

	// natsReconnectBufSize bounds the in-memory buffer the nats client
	// holds while disconnected. 16 MiB ≈ 16k presence advisories or ~80
	// ingress messages at our cap (900 KiB) — large enough for short
	// outages, bounded enough to avoid OOM on the broker host.
	natsReconnectBufSize = 16 * 1024 * 1024

	// natsFlusherTimeout controls how long the client's flusher goroutine
	// holds writes before forcing a TCP write. Aggressive (50ms) so
	// presence advisories don't pile up under low-rate scenarios; the
	// trade-off is more syscalls under high rate, which the kernel
	// coalesces anyway.
	natsFlusherTimeout = 50 * time.Millisecond

	// natsPingInterval is how often the client sends PING to detect
	// half-open connections. 2 minutes balances quick failure detection
	// against PING traffic at scale.
	natsPingInterval = 2 * time.Minute
)

// publishJob is a single envelope queued for the worker pool. Holding
// subject + bytes only (not the structured payload) keeps the worker hot
// loop allocation-free — marshalling happens on the producer side where
// the broker thread already pays the cost.
type publishJob struct {
	subject string
	data    []byte
}

// AsyncPublisher buffers publish jobs in a bounded channel and drains them
// with a worker pool. Producers (plugin event handlers) NEVER block — when
// the buffer is full, jobs are dropped and counted, so NATS slowness can
// never deadlock the broker.
//
// Lifecycle:
//
//	NewAsyncPublisher() returns the struct with workers stopped
//	Start()             spins up the worker pool (idempotent guard)
//	Enqueue()           pushes a job; rejects pre-Start AND post-Drain
//	Drain(timeout)      closes the channel, waits for workers, returns
//
// Concurrency invariants:
//   - Enqueue and Drain are safe to call concurrently. The closeMu
//     RWMutex serializes the channel-send vs channel-close so a concurrent
//     Drain can never race with a Send and panic with "send on closed
//     channel" — that panic would crash the broker process, since plugin
//     code runs in-process.
//   - Worker goroutines recover from panics in the underlying publisher
//     (nil deref under reconnect, malformed subject string, etc.) so a
//     single bad event never kills the worker pool.
//   - Enqueue called BEFORE Start is rejected with a drop counter
//     increment + warn log — items dropped here would otherwise sit in the
//     channel forever and look like a memory leak under misconfigured init
//     order.
//
// Stats counters are read with the *Count() methods and are safe for
// concurrent access (atomic). They reset only with Drain → Start.
type AsyncPublisher struct {
	pub     NatsPublisher
	queue   chan publishJob
	workers int
	wg      sync.WaitGroup
	log     logging.Logger

	// closeMu serializes access to `closed` + the channel send. Enqueue
	// takes RLock (shared), Drain takes Lock (exclusive). RLock cost
	// (~30ns) is dwarfed by everything else on the hot path; the guarantee
	// against a "send on closed channel" panic is worth it.
	closeMu sync.RWMutex
	closed  bool

	enqueued       atomic.Uint64
	published      atomic.Uint64
	publishErr     atomic.Uint64
	dropped        atomic.Uint64
	droppedNoStart atomic.Uint64
	panics         atomic.Uint64
	started        atomic.Bool
}

// natsLifecycleLogger wires nats.Conn lifecycle events into our plugin
// Logger so ops sees disconnects, reconnects, and the final closed state
// without having to scrape mosquitto-internal logs.
type natsLifecycleLogger struct {
	log logging.Logger
}
