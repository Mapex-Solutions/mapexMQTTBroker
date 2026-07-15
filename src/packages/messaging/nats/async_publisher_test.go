package natsbus

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingPublisher captures every publish call so tests can assert the
// exact count of (subject, payload) pairs the worker pool drained.
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

// captureLogger records emitted lines so tests can assert critical events
// surface (drops, panics, lifecycle).
type captureLogger struct {
	mu    sync.Mutex
	buf   strings.Builder
	count atomic.Uint64
}

func (l *captureLogger) Debug(f string, a ...any) { l.add("DEBUG", f, a...) }
func (l *captureLogger) Info(f string, a ...any)  { l.add("INFO", f, a...) }
func (l *captureLogger) Warn(f string, a ...any)  { l.add("WARN", f, a...) }
func (l *captureLogger) Error(f string, a ...any) { l.add("ERROR", f, a...) }
func (l *captureLogger) add(level, f string, a ...any) {
	l.count.Add(1)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.WriteString(level + " " + fmt.Sprintf(f, a...) + "\n")
}
func (l *captureLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
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

	accepted, dropped := 0, 0
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

func TestAsyncPublisher_RecordDropCountsValidationRejects(t *testing.T) {
	rp := &recordingPublisher{}
	p, _ := NewAsyncPublisher(rp, 8, 1, nil)
	p.Start()
	p.RecordDrop()
	p.RecordDrop()
	if p.DroppedCount() != 2 {
		t.Fatalf("expected DroppedCount=2 from RecordDrop, got %d", p.DroppedCount())
	}
	_ = p.Drain(time.Second)
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
		t.Fatalf("expected 2 successful publishes, got %d", p.PublishedCount())
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

func TestEnqueueBeforeStart_RejectedAndCounted(t *testing.T) {
	pub := &recordingPublisher{}
	p, err := NewAsyncPublisher(pub, 16, 1, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	for i := 0; i < 5; i++ {
		if p.Enqueue("subj", []byte("x")) {
			t.Fatalf("Enqueue must reject before Start (iteration %d)", i)
		}
	}
	if p.DroppedCount() != 5 {
		t.Fatalf("expected 5 drops, got %d", p.DroppedCount())
	}
	if p.DroppedNoStartCount() != 5 {
		t.Fatalf("expected DroppedNoStartCount=5, got %d", p.DroppedNoStartCount())
	}
	if p.EnqueuedCount() != 0 {
		t.Fatalf("expected zero successful enqueues, got %d", p.EnqueuedCount())
	}
}

// alwaysFailPublisher simulates a permanently-unreachable NATS connection.
type alwaysFailPublisher struct{ calls atomic.Uint64 }

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

	deadline := time.Now().Add(500 * time.Millisecond)
	enqueued := 0
	for i := 0; i < 1000 && time.Now().Before(deadline); i++ {
		if p.Enqueue("subj", []byte("x")) {
			enqueued++
		}
	}
	_ = p.Drain(2 * time.Second)

	if p.PublishErrorCount() == 0 {
		t.Fatalf("expected PublishErrorCount > 0 with always-failing backend")
	}
	if p.PublishedCount() != 0 {
		t.Fatalf("expected zero successful publishes, got %d", p.PublishedCount())
	}
	if p.PublishErrorCount() != uint64(enqueued) {
		t.Fatalf("every enqueued job must surface as an error: enqueued=%d errors=%d", enqueued, p.PublishErrorCount())
	}
}

// panickingPublisher simulates a buggy NATS client (nil deref on reconnect).
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
		t.Fatalf("expected 5 successful publishes after panic burst, got %d", p.PublishedCount())
	}
	if p.PublishErrorCount() != 5 {
		t.Fatalf("expected panics to count as publish errors, got %d", p.PublishErrorCount())
	}
}

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
	time.Sleep(2 * time.Millisecond)
	if !p.Drain(2 * time.Second) {
		t.Fatalf("drain timed out")
	}
	producers.Wait()
}

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

// slowPublisher simulates a NATS connection that's UP but slower than the
// event rate, exercising the drop-on-overflow contract under pressure.
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

	burstStart := time.Now()
	for i := 0; i < 200; i++ {
		_ = p.Enqueue("subj", []byte{byte(i)})
	}
	if burstDuration := time.Since(burstStart); burstDuration > 50*time.Millisecond {
		t.Fatalf("producer took %v for 200 enqueues — Enqueue is blocking", burstDuration)
	}
	if p.DroppedCount() == 0 {
		t.Fatalf("expected drops with slow worker + small buffer + 200-job burst, got zero")
	}
	_ = p.Drain(2 * time.Second)
}

func TestNoGoroutineLeak_StressfulEnqueueDrainCycles(t *testing.T) {
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	baseline := runtime.NumGoroutine()

	const cycles, workers = 50, 4
	for c := 0; c < cycles; c++ {
		pub := &recordingPublisher{}
		p, err := NewAsyncPublisher(pub, 64, workers, nil)
		if err != nil {
			t.Fatalf("constructor cycle=%d: %v", c, err)
		}
		p.Start()
		for i := 0; i < 100; i++ {
			_ = p.Enqueue("subj", []byte{byte(i)})
		}
		if !p.Drain(2 * time.Second) {
			t.Fatalf("drain timed out cycle=%d", c)
		}
	}

	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	if delta := runtime.NumGoroutine() - baseline; delta > workers {
		t.Fatalf("possible goroutine leak: delta=%d (cycles=%d workers/cycle=%d)", delta, cycles, workers)
	}
}

func TestLogger_EmitsOnPublishError(t *testing.T) {
	pub := &alwaysFailPublisher{}
	cap := &captureLogger{}
	p, err := NewAsyncPublisher(pub, 8, 1, cap)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	p.Start()
	for i := 0; i < 3; i++ {
		_ = p.Enqueue("dev.mapexos.mqtt.data", []byte("x"))
	}
	_ = p.Drain(2 * time.Second)

	if cap.count.Load() == 0 {
		t.Fatalf("expected logs on publish failures, got zero")
	}
	if !strings.Contains(cap.String(), "WARN") {
		t.Fatalf("expected at least one WARN line, got: %s", cap.String())
	}
}

func TestLogger_StartAndDrainAnnouncements(t *testing.T) {
	pub := &recordingPublisher{}
	cap := &captureLogger{}
	p, _ := NewAsyncPublisher(pub, 8, 2, cap)
	p.Start()
	_ = p.Enqueue("subj", []byte("x"))
	_ = p.Drain(time.Second)

	out := cap.String()
	if !strings.Contains(out, "started") {
		t.Fatalf("expected start announcement, got: %s", out)
	}
	if !strings.Contains(out, "drained") {
		t.Fatalf("expected drain summary, got: %s", out)
	}
}
