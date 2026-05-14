package broker

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// NatsPublisher is the minimal contract the plugin uses to push events
// to NATS. The real implementation wraps *nats.Conn; tests inject a
// fake to assert publish semantics without needing a live NATS.
type NatsPublisher interface {
	Publish(subject string, data []byte) error
}

// publishJob is a single envelope queued for the worker pool. Holding
// subject + bytes only (not the structured payload) keeps the worker
// hot loop allocation-free — marshalling happens on the producer side
// where the broker thread already pays the cost.
type publishJob struct {
	subject string
	data    []byte
}

// AsyncPublisher buffers publish jobs in a bounded channel and drains
// them with a worker pool. Producers (plugin event handlers) NEVER
// block — when the buffer is full, jobs are dropped and counted, so
// NATS slowness can never deadlock the broker.
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
//     RWMutex serializes the channel-send vs channel-close so a
//     concurrent Drain can never race with a Send and panic with
//     "send on closed channel" — that panic would crash the broker
//     process, since plugin code runs in-process.
//   - Worker goroutines recover from panics in the underlying
//     publisher (nil deref under reconnect, malformed subject string,
//     etc.) so a single bad event never kills the worker pool.
//   - Enqueue called BEFORE Start is rejected with a drop counter
//     increment + warn log — items dropped here would otherwise sit
//     in the channel forever and look like a memory leak under
//     misconfigured init order.
//
// Stats counters are read with the *Count() methods and are safe for
// concurrent access (atomic). They reset only with Drain → Start.
type AsyncPublisher struct {
	pub     NatsPublisher
	queue   chan publishJob
	workers int
	wg      sync.WaitGroup
	log     Logger

	// closeMu serializes access to `closed` + the channel send. Enqueue
	// takes RLock (shared), Drain takes Lock (exclusive). RLock cost
	// (~30ns) is dwarfed by everything else on the hot path; the
	// guarantee against a "send on closed channel" panic is worth it.
	closeMu sync.RWMutex
	closed  bool

	enqueued      atomic.Uint64
	published     atomic.Uint64
	publishErr    atomic.Uint64
	dropped       atomic.Uint64
	droppedNoStart atomic.Uint64
	panics        atomic.Uint64
	started       atomic.Bool
}

// NewAsyncPublisher constructs an AsyncPublisher. bufferSize and
// workers MUST be positive; the constructor returns an error
// otherwise so misconfigurations are caught at plugin init rather
// than at first event. log may be nil — defaults to a no-op Logger
// so callers that skip wiring still get a usable publisher.
func NewAsyncPublisher(pub NatsPublisher, bufferSize, workers int, log Logger) (*AsyncPublisher, error) {
	if pub == nil {
		return nil, errors.New("publisher is nil")
	}
	if bufferSize <= 0 {
		return nil, errors.New("bufferSize must be > 0")
	}
	if workers <= 0 {
		return nil, errors.New("workers must be > 0")
	}
	if log == nil {
		log = nopLogger{}
	}
	return &AsyncPublisher{
		pub:     pub,
		queue:   make(chan publishJob, bufferSize),
		workers: workers,
		log:     log,
	}, nil
}

// Start spins up the worker goroutines. Calling Start twice is a
// programming error; the second call panics so misuse fails loudly
// in tests rather than silently double-draining.
func (p *AsyncPublisher) Start() {
	if !p.started.CompareAndSwap(false, true) {
		panic("AsyncPublisher: Start called twice")
	}
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	p.log.Info("AsyncPublisher started: workers=%d buffer=%d", p.workers, cap(p.queue))
}

// Enqueue tries a non-blocking send onto the bounded channel. Returns
// true when the job was accepted, false when the publisher hasn't
// been Started yet, the buffer is full, OR the publisher has been
// Drained. A false return is the platform's overflow signal — the
// caller MUST NOT retry; the dropped counter is the operator-visible
// health metric.
//
// The closeMu.RLock guards against a concurrent Drain closing the
// channel between our `closed` check and the channel send: without
// it, the send would panic and crash the broker.
func (p *AsyncPublisher) Enqueue(subject string, data []byte) bool {
	if !p.started.Load() {
		p.droppedNoStart.Add(1)
		p.dropped.Add(1)
		p.log.Warn("Enqueue called before Start; dropping subject=%s", subject)
		return false
	}

	p.closeMu.RLock()
	defer p.closeMu.RUnlock()

	if p.closed {
		return false
	}
	select {
	case p.queue <- publishJob{subject: subject, data: data}:
		p.enqueued.Add(1)
		return true
	default:
		p.dropped.Add(1)
		return false
	}
}

// Drain closes the input channel and waits up to `timeout` for the
// worker pool to finish. Returns true when all queued jobs drained;
// false on timeout (workers may still be running but cleanup
// proceeds). After Drain, Enqueue rejects new jobs.
//
// closeMu.Lock blocks until every in-flight Enqueue's RLock is
// released, so the channel close races with no concurrent send.
//
// Goroutine-leak note: when Drain hits the timeout, the inner
// goroutine running wg.Wait() stays alive until workers actually
// finish. That goroutine holds only a closure pointer, so the leak
// is bounded by 1 goroutine per Drain-on-timeout. The plugin calls
// Drain at most once per process lifecycle (via mosquitto_plugin_cleanup),
// so this is acceptable; documenting it explicitly so future readers
// don't accidentally turn Drain into a per-request operation.
func (p *AsyncPublisher) Drain(timeout time.Duration) bool {
	p.closeMu.Lock()
	if p.closed {
		p.closeMu.Unlock()
		return true
	}
	p.closed = true
	close(p.queue)
	p.closeMu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		p.log.Info("AsyncPublisher drained: enqueued=%d published=%d dropped=%d publish_errors=%d panics=%d",
			p.EnqueuedCount(), p.PublishedCount(), p.DroppedCount(),
			p.PublishErrorCount(), p.PanicCount())
		return true
	case <-time.After(timeout):
		p.log.Warn("AsyncPublisher drain timeout=%s; %d workers may still be running",
			timeout, p.workers)
		return false
	}
}

// EnqueuedCount returns the total jobs accepted since Start.
func (p *AsyncPublisher) EnqueuedCount() uint64 { return p.enqueued.Load() }

// PublishedCount returns the total jobs successfully published.
func (p *AsyncPublisher) PublishedCount() uint64 { return p.published.Load() }

// PublishErrorCount returns the total publish-side errors (NATS
// returned an error from .Publish, including recovered panics).
func (p *AsyncPublisher) PublishErrorCount() uint64 { return p.publishErr.Load() }

// DroppedCount returns the total jobs dropped — sum of buffer-full
// drops AND pre-Start drops. A consistently growing dropped count is
// the canonical signal that NATS is slower than the event rate, the
// buffer is undersized, or the plugin was misconfigured (Enqueue
// before Start).
func (p *AsyncPublisher) DroppedCount() uint64 { return p.dropped.Load() }

// DroppedNoStartCount returns drops attributed specifically to
// Enqueue-called-before-Start. Should be zero in any well-configured
// deployment; non-zero indicates a plugin init bug.
func (p *AsyncPublisher) DroppedNoStartCount() uint64 { return p.droppedNoStart.Load() }

// PanicCount returns the total worker panics recovered. Should be
// zero in normal operation; non-zero indicates a bug in the publisher
// (e.g. nil deref under reconnect) and warrants an alert.
func (p *AsyncPublisher) PanicCount() uint64 { return p.panics.Load() }

// worker drains jobs and forwards each to the underlying publisher.
// publishOne wraps each individual call so a panic in pub.Publish
// (nil deref under reconnect, malformed subject) is contained — the
// worker keeps draining the queue and the broker stays alive.
func (p *AsyncPublisher) worker() {
	defer p.wg.Done()
	for job := range p.queue {
		p.publishOne(job)
	}
}

// publishOne handles a single job, recovering from any panic in the
// underlying NATS client. publish errors and panics are counted but
// never re-queued — the plugin trades correctness on individual
// events for liveness of the broker.
func (p *AsyncPublisher) publishOne(job publishJob) {
	defer func() {
		if r := recover(); r != nil {
			p.panics.Add(1)
			p.publishErr.Add(1)
			p.log.Error("publish panic recovered: subject=%s err=%v", job.subject, r)
		}
	}()
	if err := p.pub.Publish(job.subject, job.data); err != nil {
		p.publishErr.Add(1)
		p.log.Warn("publish failed: subject=%s err=%v size=%d", job.subject, err, len(job.data))
		return
	}
	p.published.Add(1)
}
