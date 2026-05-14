package broker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingPublisher captures every publish call so tests can assert
// the exact sequence + count of (subject, payload) pairs the worker
// pool drained.
type recordingPublisher struct {
	mu       sync.Mutex
	calls    [][2]string
	delay    time.Duration
	failNext atomic.Int32
}

func (r *recordingPublisher) Publish(subject string, data []byte) error {
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	if r.failNext.Add(-1) >= 0 {
		return errors.New("simulated nats failure")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [2]string{subject, string(data)})
	return nil
}

func (r *recordingPublisher) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func TestNewAsyncPublisher_ValidatesArgs(t *testing.T) {
	rp := &recordingPublisher{}
	tests := []struct {
		name    string
		pub     NatsPublisher
		buffer  int
		workers int
	}{
		{"nil publisher", nil, 10, 1},
		{"zero buffer", rp, 0, 1},
		{"negative buffer", rp, -1, 1},
		{"zero workers", rp, 10, 0},
		{"negative workers", rp, 10, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewAsyncPublisher(tt.pub, tt.buffer, tt.workers, nil); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestAsyncPublisher_PublishesEnqueuedJobs(t *testing.T) {
	rp := &recordingPublisher{}
	p, err := NewAsyncPublisher(rp, 32, 2, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	p.Start()

	for i := 0; i < 10; i++ {
		if !p.Enqueue("subj", []byte{byte(i)}) {
			t.Fatalf("enqueue rejected job %d unexpectedly", i)
		}
	}
	if !p.Drain(time.Second) {
		t.Fatalf("drain timed out")
	}
	if got := rp.callCount(); got != 10 {
		t.Fatalf("expected 10 publishes, got %d", got)
	}
	if p.PublishedCount() != 10 {
		t.Fatalf("expected PublishedCount=10, got %d", p.PublishedCount())
	}
	if p.DroppedCount() != 0 {
		t.Fatalf("expected DroppedCount=0, got %d", p.DroppedCount())
	}
}

func TestAsyncPublisher_DropsOnFullBuffer(t *testing.T) {
	rp := &recordingPublisher{delay: 50 * time.Millisecond}
	p, err := NewAsyncPublisher(rp, 1, 1, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	p.Start()

	accepted := 0
	dropped := 0
	for i := 0; i < 30; i++ {
		if p.Enqueue("subj", []byte{byte(i)}) {
			accepted++
		} else {
			dropped++
		}
	}
	if dropped == 0 {
		t.Fatalf("expected at least one drop with bufferSize=1 + slow worker, got zero")
	}
	if accepted == 0 {
		t.Fatalf("expected at least one acceptance, got zero")
	}
	if p.DroppedCount() != uint64(dropped) {
		t.Fatalf("DroppedCount %d != tracked drops %d", p.DroppedCount(), dropped)
	}
	_ = p.Drain(2 * time.Second)
}

func TestAsyncPublisher_CountsPublishErrors(t *testing.T) {
	rp := &recordingPublisher{}
	rp.failNext.Store(3)

	p, err := NewAsyncPublisher(rp, 16, 1, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	p.Start()

	for i := 0; i < 5; i++ {
		p.Enqueue("subj", []byte{byte(i)})
	}
	if !p.Drain(time.Second) {
		t.Fatalf("drain timed out")
	}

	if p.PublishErrorCount() != 3 {
		t.Fatalf("expected 3 publish errors, got %d", p.PublishErrorCount())
	}
	if p.PublishedCount() != 2 {
		t.Fatalf("expected 2 successful publishes (5 enqueued - 3 failed), got %d", p.PublishedCount())
	}
}

func TestAsyncPublisher_DrainRejectsSubsequentEnqueues(t *testing.T) {
	rp := &recordingPublisher{}
	p, err := NewAsyncPublisher(rp, 4, 1, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	p.Start()

	if !p.Drain(time.Second) {
		t.Fatalf("drain timed out")
	}
	if p.Enqueue("subj", []byte("late")) {
		t.Fatalf("Enqueue must reject after Drain returns")
	}
}

func TestAsyncPublisher_StartTwicePanics(t *testing.T) {
	rp := &recordingPublisher{}
	p, _ := NewAsyncPublisher(rp, 4, 1, nil)
	p.Start()
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic on second Start")
		}
		_ = p.Drain(time.Second)
	}()
	p.Start()
}
