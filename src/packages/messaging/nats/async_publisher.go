package natsbus

import (
	"errors"
	"time"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// NewAsyncPublisher constructs an AsyncPublisher. bufferSize and workers
// MUST be positive; the constructor returns an error otherwise so
// misconfigurations are caught at plugin init rather than at first event.
// log may be nil — defaults to a no-op Logger so callers that skip wiring
// still get a usable publisher.
func NewAsyncPublisher(pub NatsPublisher, bufferSize, workers int, log logging.Logger) (*AsyncPublisher, error) {
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
		log = logging.NopLogger{}
	}
	return &AsyncPublisher{
		pub:     pub,
		queue:   make(chan publishJob, bufferSize),
		workers: workers,
		log:     log,
	}, nil
}

// Start spins up the worker goroutines. Calling Start twice is a
// programming error; the second call panics so misuse fails loudly in
// tests rather than silently double-draining.
func (p *AsyncPublisher) Start() {
	if !p.started.CompareAndSwap(false, true) {
		panic("AsyncPublisher: Start called twice")
	}
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	p.log.Info("[INFRA:AsyncPublisher] started: workers=%d buffer=%d", p.workers, cap(p.queue))
}

// Enqueue tries a non-blocking send onto the bounded channel. Returns true
// when the job was accepted, false when the publisher hasn't been Started
// yet, the buffer is full, OR the publisher has been Drained. A false
// return is the platform's overflow signal — the caller MUST NOT retry;
// the dropped counter is the operator-visible health metric.
//
// The closeMu.RLock guards against a concurrent Drain closing the channel
// between our `closed` check and the channel send: without it, the send
// would panic and crash the broker.
func (p *AsyncPublisher) Enqueue(subject string, data []byte) bool {
	if !p.started.Load() {
		p.droppedNoStart.Add(1)
		p.dropped.Add(1)
		p.log.Warn("[INFRA:AsyncPublisher] enqueue before start; dropping subject=%s", subject)
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

// RecordDrop increments the dropped counter for events rejected before
// they reach the queue (subject-token or payload-size validation). Keeps
// the counter the single source of truth for overflow + reject health.
func (p *AsyncPublisher) RecordDrop() {
	p.dropped.Add(1)
}

// Drain closes the input channel and waits up to `timeout` for the worker
// pool to finish. Returns true when all queued jobs drained; false on
// timeout (workers may still be running but cleanup proceeds). After
// Drain, Enqueue rejects new jobs.
//
// closeMu.Lock blocks until every in-flight Enqueue's RLock is released,
// so the channel close races with no concurrent send.
//
// Goroutine-leak note: when Drain hits the timeout, the inner goroutine
// running wg.Wait() stays alive until workers actually finish. That
// goroutine holds only a closure pointer, so the leak is bounded by 1
// goroutine per Drain-on-timeout. The plugin calls Drain at most once per
// process lifecycle (via mosquitto_plugin_cleanup), so this is acceptable;
// documenting it explicitly so future readers don't accidentally turn
// Drain into a per-request operation.
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
		p.log.Info("[INFRA:AsyncPublisher] drained: enqueued=%d published=%d dropped=%d publish_errors=%d panics=%d",
			p.EnqueuedCount(), p.PublishedCount(), p.DroppedCount(),
			p.PublishErrorCount(), p.PanicCount())
		return true
	case <-time.After(timeout):
		p.log.Warn("[INFRA:AsyncPublisher] drain timeout=%s; %d workers may still be running",
			timeout, p.workers)
		return false
	}
}

// EnqueuedCount returns the total jobs accepted since Start.
func (p *AsyncPublisher) EnqueuedCount() uint64 { return p.enqueued.Load() }

// PublishedCount returns the total jobs successfully published.
func (p *AsyncPublisher) PublishedCount() uint64 { return p.published.Load() }

// PublishErrorCount returns the total publish-side errors (NATS returned
// an error from .Publish, including recovered panics).
func (p *AsyncPublisher) PublishErrorCount() uint64 { return p.publishErr.Load() }

// DroppedCount returns the total jobs dropped — sum of buffer-full drops,
// pre-Start drops, AND pre-enqueue validation rejects. A consistently
// growing dropped count is the canonical signal that NATS is slower than
// the event rate, the buffer is undersized, or the plugin was
// misconfigured (Enqueue before Start).
func (p *AsyncPublisher) DroppedCount() uint64 { return p.dropped.Load() }

// DroppedNoStartCount returns drops attributed specifically to
// Enqueue-called-before-Start. Should be zero in any well-configured
// deployment; non-zero indicates a plugin init bug.
func (p *AsyncPublisher) DroppedNoStartCount() uint64 { return p.droppedNoStart.Load() }

// PanicCount returns the total worker panics recovered. Should be zero in
// normal operation; non-zero indicates a bug in the publisher (e.g. nil
// deref under reconnect) and warrants an alert.
func (p *AsyncPublisher) PanicCount() uint64 { return p.panics.Load() }

// worker drains jobs and forwards each to the underlying publisher.
// publishOne wraps each individual call so a panic in pub.Publish (nil
// deref under reconnect, malformed subject) is contained — the worker
// keeps draining the queue and the broker stays alive.
func (p *AsyncPublisher) worker() {
	defer p.wg.Done()
	for job := range p.queue {
		p.publishOne(job)
	}
}

// publishOne handles a single job, recovering from any panic in the
// underlying NATS client. publish errors and panics are counted but never
// re-queued — the plugin trades correctness on individual events for
// liveness of the broker.
func (p *AsyncPublisher) publishOne(job publishJob) {
	defer func() {
		if r := recover(); r != nil {
			p.panics.Add(1)
			p.publishErr.Add(1)
			p.log.Error("[INFRA:AsyncPublisher] publish panic recovered: subject=%s err=%v", job.subject, r)
		}
	}()
	if err := p.pub.Publish(job.subject, job.data); err != nil {
		p.publishErr.Add(1)
		p.log.Warn("[INFRA:AsyncPublisher] publish failed: subject=%s err=%v size=%d", job.subject, err, len(job.data))
		return
	}
	p.published.Add(1)
}
