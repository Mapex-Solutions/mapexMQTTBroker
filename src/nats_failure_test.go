package broker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// alwaysFailPublisher simulates a NATS connection that is permanently
// unreachable — every Publish returns an error. The plugin's
// AsyncPublisher must continue draining the queue and counting
// errors without ever blocking the broker thread.
type alwaysFailPublisher struct {
	calls atomic.Uint64
}

func (p *alwaysFailPublisher) Publish(_ string, _ []byte) error {
	p.calls.Add(1)
	return errors.New("nats unreachable")
}

func TestAsyncPublisher_NatsCompletelyDown_BrokerStaysAlive(t *testing.T) {
	pub := &alwaysFailPublisher{}
	p, err := NewAsyncPublisher(pub, 100, 4, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	p.Start()

	// Simulate a burst of 1000 events while NATS is fully down. None
	// of these calls may block the producer (broker thread).
	deadline := time.Now().Add(500 * time.Millisecond)
	enqueued := 0
	for i := 0; i < 1000 && time.Now().Before(deadline); i++ {
		if p.Enqueue("subj", []byte("x")) {
			enqueued++
		}
	}
	_ = p.Drain(2 * time.Second)

	if pub.calls.Load() == 0 {
		t.Fatalf("expected workers to attempt publishes against the failing backend, got zero")
	}
	if p.PublishErrorCount() == 0 {
		t.Fatalf("expected PublishErrorCount > 0 with always-failing backend, got zero")
	}
	if p.PublishedCount() != 0 {
		t.Fatalf("expected zero successful publishes with always-failing backend, got %d", p.PublishedCount())
	}
	// The total work the workers handled = errors observed.
	// All enqueued items must have been processed (drained).
	if p.PublishErrorCount() != uint64(enqueued) {
		t.Fatalf("expected every enqueued job to surface as a publish error: enqueued=%d errors=%d",
			enqueued, p.PublishErrorCount())
	}
}

// panickingPublisher simulates a buggy NATS client lib (e.g. nil deref
// during reconnect). The plugin MUST recover from these panics — a
// panic in the worker goroutine inside the broker process would crash
// the broker.
type panickingPublisher struct {
	panicNext atomic.Int32
	calls     atomic.Uint64
}

func (p *panickingPublisher) Publish(_ string, _ []byte) error {
	p.calls.Add(1)
	if p.panicNext.Add(-1) >= 0 {
		panic("simulated nats client nil deref")
	}
	return nil
}

func TestAsyncPublisher_PublishPanic_WorkerSurvives(t *testing.T) {
	pub := &panickingPublisher{}
	pub.panicNext.Store(5)

	p, err := NewAsyncPublisher(pub, 16, 1, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	p.Start()

	for i := 0; i < 10; i++ {
		if !p.Enqueue("subj", []byte{byte(i)}) {
			t.Fatalf("enqueue dropped job %d unexpectedly", i)
		}
	}
	if !p.Drain(2 * time.Second) {
		t.Fatalf("drain timed out — worker may have died on panic")
	}

	if p.PanicCount() != 5 {
		t.Fatalf("expected 5 recovered panics, got %d", p.PanicCount())
	}
	if p.PublishedCount() != 5 {
		t.Fatalf("expected 5 successful publishes after the panic burst, got %d", p.PublishedCount())
	}
	if p.PublishErrorCount() != 5 {
		t.Fatalf("expected panics to count as publish errors too, got %d", p.PublishErrorCount())
	}
}

// TestAsyncPublisher_ConcurrentEnqueueAndDrain_NoPanic exercises the
// TOCTOU window between the `closed` check and the channel send. Run
// many producer goroutines and call Drain mid-flight. Without the
// closeMu RWMutex this would panic with "send on closed channel" and
// crash the broker process.
func TestAsyncPublisher_ConcurrentEnqueueAndDrain_NoPanic(t *testing.T) {
	pub := &recordingPublisher{}
	p, err := NewAsyncPublisher(pub, 1024, 4, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	p.Start()

	var producers sync.WaitGroup
	for i := 0; i < 32; i++ {
		producers.Add(1)
		go func() {
			defer producers.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Enqueue panicked under concurrent Drain: %v", r)
				}
			}()
			for j := 0; j < 200; j++ {
				_ = p.Enqueue("subj", []byte{byte(j)})
			}
		}()
	}
	// Drain after producers have started so they race against the close.
	time.Sleep(2 * time.Millisecond)
	if !p.Drain(2 * time.Second) {
		t.Fatalf("drain timed out")
	}
	producers.Wait()
}

// TestAsyncPublisher_DrainCalledTwice_Idempotent confirms the second
// Drain returns immediately and does not double-close the channel.
func TestAsyncPublisher_DrainCalledTwice_Idempotent(t *testing.T) {
	pub := &recordingPublisher{}
	p, _ := NewAsyncPublisher(pub, 16, 1, nil)
	p.Start()

	if !p.Drain(time.Second) {
		t.Fatalf("first drain timed out")
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("second Drain panicked (double close): %v", r)
		}
	}()
	if !p.Drain(time.Second) {
		t.Fatalf("second drain returned false (should be idempotent)")
	}
}

// slowPublisher simulates a NATS connection that's UP but slower than
// the event rate, exercising the drop-on-overflow contract under
// sustained pressure.
type slowPublisher struct {
	delay time.Duration
	calls atomic.Uint64
}

func (p *slowPublisher) Publish(_ string, _ []byte) error {
	p.calls.Add(1)
	time.Sleep(p.delay)
	return nil
}

func TestAsyncPublisher_NatsSlow_ProducerNeverBlocks(t *testing.T) {
	pub := &slowPublisher{delay: 100 * time.Millisecond}
	p, err := NewAsyncPublisher(pub, 4, 1, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	p.Start()

	// Producer must complete a burst of 200 enqueues in well under
	// the time it would take the slow worker to drain even one job.
	// If Enqueue ever blocks, this fails on duration alone.
	burstStart := time.Now()
	for i := 0; i < 200; i++ {
		_ = p.Enqueue("subj", []byte{byte(i)})
	}
	burstDuration := time.Since(burstStart)
	if burstDuration > 50*time.Millisecond {
		t.Fatalf("producer took %v for 200 enqueues — Enqueue is blocking", burstDuration)
	}
	if p.DroppedCount() == 0 {
		t.Fatalf("expected drops with slow worker + small buffer + 200-job burst, got zero")
	}
	_ = p.Drain(2 * time.Second)
}

// TestAsyncPublisher_EnqueueAfterDrainReturnsFalse confirms the
// closed-state read path under the new RWMutex implementation works
// without deadlocking.
func TestAsyncPublisher_EnqueueAfterDrainReturnsFalse(t *testing.T) {
	pub := &recordingPublisher{}
	p, _ := NewAsyncPublisher(pub, 4, 1, nil)
	p.Start()
	_ = p.Drain(time.Second)

	for i := 0; i < 5; i++ {
		if p.Enqueue("subj", []byte{byte(i)}) {
			t.Fatalf("Enqueue accepted job %d after Drain", i)
		}
	}
}
