package broker

import (
	"bytes"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestEnqueueBeforeStart_RejectedAndCounted exercises the misuse path
// where Enqueue is called before Start. Without the started.Load()
// gate the items would sit in the channel forever (no worker draining)
// and look like a memory leak under misconfigured init order.
func TestEnqueueBeforeStart_RejectedAndCounted(t *testing.T) {
	pub := &recordingPublisher{}
	p, err := NewAsyncPublisher(pub, 16, 1, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	// NOTE: no Start() call here.
	for i := 0; i < 5; i++ {
		if p.Enqueue("subj", []byte("x")) {
			t.Fatalf("Enqueue must reject before Start (iteration %d)", i)
		}
	}
	if p.DroppedCount() != 5 {
		t.Fatalf("expected 5 drops, got %d", p.DroppedCount())
	}
	if p.DroppedNoStartCount() != 5 {
		t.Fatalf("expected DroppedNoStartCount=5 to attribute drops to misuse, got %d",
			p.DroppedNoStartCount())
	}
	if p.EnqueuedCount() != 0 {
		t.Fatalf("expected zero successful enqueues, got %d", p.EnqueuedCount())
	}
}

// TestPublishIngress_RejectsInvalidSubjectTokens ensures an orgId or
// assetUUID containing NATS-illegal characters never produces a
// corrupted subject. Without this guard, "." in the assetUUID would
// silently corrupt the routing.
func TestPublishIngress_RejectsInvalidSubjectTokens(t *testing.T) {
	pub := &recordingPublisher{}
	rt := newTestRuntime(t, pub, 16, 1)

	tests := []struct {
		name      string
		orgID     string
		assetUUID string
	}{
		{"dot in orgID", "org.with.dot", "asset-aaa"},
		{"dot in assetUUID", "org-1", "asset.with.dot"},
		{"star in assetUUID", "org-1", "asset*"},
		{"gt in orgID", "org>x", "asset-aaa"},
		{"space in assetUUID", "org-1", "asset with space"},
		{"tab in orgID", "org\twith\ttab", "asset-aaa"},
		{"newline in assetUUID", "org-1", "asset\nwith\nnewline"},
		{"empty orgID", "", "asset-aaa"},
		{"empty assetUUID", "org-1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := pub.callCount()
			ok := rt.PublishIngress(tt.orgID, tt.assetUUID, "client", "topic", []byte("p"), 0, false, time.Now())
			if ok {
				t.Fatalf("PublishIngress must reject invalid token")
			}
			_ = rt.Async.Drain(time.Second)
			if pub.callCount() != before {
				t.Fatalf("invalid token reached NATS publisher")
			}
		})
	}
}

// TestPublishIngress_DropsOversizedPayload guards against silently
// hitting the NATS server's 1 MiB max_payload limit. Devices with
// buggy firmware (or adversaries) sending huge payloads must not
// pollute counters as cryptic publish errors — the warn log
// identifies them clearly.
func TestPublishIngress_DropsOversizedPayload(t *testing.T) {
	pub := &recordingPublisher{}
	rt := newTestRuntime(t, pub, 16, 1)

	huge := bytes.Repeat([]byte("a"), MaxIngressPayloadBytes+1)
	if rt.PublishIngress("org-1", "asset-aaa", "client", "events/x", huge, 0, false, time.Now()) {
		t.Fatalf("PublishIngress must drop payload > MaxIngressPayloadBytes")
	}
	_ = rt.Async.Drain(time.Second)

	if pub.callCount() != 0 {
		t.Fatalf("oversized payload reached NATS")
	}
}

// TestPublishIngress_AcceptsPayloadAtCap verifies the boundary case:
// a payload exactly at MaxIngressPayloadBytes is accepted (off-by-one
// regression guard).
func TestPublishIngress_AcceptsPayloadAtCap(t *testing.T) {
	pub := &recordingPublisher{}
	rt := newTestRuntime(t, pub, 16, 1)

	atCap := bytes.Repeat([]byte("a"), MaxIngressPayloadBytes)
	if !rt.PublishIngress("org-1", "asset-aaa", "client", "events/x", atCap, 0, false, time.Now()) {
		t.Fatalf("PublishIngress must accept payload exactly at cap")
	}
	_ = rt.Async.Drain(time.Second)
}

// TestNoGoroutineLeak_StressfulEnqueueDrainCycles cycles 50 publishers
// through Start + Enqueue burst + Drain and confirms the goroutine
// count returns to baseline. Catches the wg.Wait() goroutine leak
// edge in Drain timeout, plus any worker-side leak from the panic
// recovery path.
func TestNoGoroutineLeak_StressfulEnqueueDrainCycles(t *testing.T) {
	// Warm up the runtime so background goroutines (e.g. Go's GC
	// helpers) settle before we baseline.
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	baseline := runtime.NumGoroutine()

	const cycles = 50
	const workers = 4
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
	final := runtime.NumGoroutine()

	// Allow a small tolerance for goroutines the testing runtime spins
	// up (parallelism, log flusher, etc.). The leak bug would manifest
	// as `final - baseline >= cycles * workers` (workers per cycle
	// staying alive).
	if delta := final - baseline; delta > workers {
		t.Fatalf("possible goroutine leak: baseline=%d final=%d delta=%d (cycles=%d workers/cycle=%d)",
			baseline, final, delta, cycles, workers)
	}
}

// captureLogger records every emitted line into a buffer so tests can
// assert that critical events are surfaced (drops, panics, etc.).
type captureLogger struct {
	mu    bytes.Buffer
	count atomic.Uint64
}

func (l *captureLogger) Debug(format string, args ...any) { l.append("DEBUG", format, args...) }
func (l *captureLogger) Info(format string, args ...any)  { l.append("INFO", format, args...) }
func (l *captureLogger) Warn(format string, args ...any)  { l.append("WARN", format, args...) }
func (l *captureLogger) Error(format string, args ...any) { l.append("ERROR", format, args...) }
func (l *captureLogger) append(level, format string, args ...any) {
	l.count.Add(1)
	l.mu.WriteString(level + " ")
	l.mu.WriteString(formatLine(format, args...))
	l.mu.WriteByte('\n')
}
func (l *captureLogger) String() string { return l.mu.String() }

func formatLine(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	// Lightweight fmt.Sprintf substitute that handles %s + %d + %v
	// without pulling fmt; sufficient for assertion logs.
	return strings.NewReplacer().Replace(format) + " " + sprintfFallback(args...)
}

func sprintfFallback(args ...any) string {
	var b bytes.Buffer
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		_, _ = b.WriteString(toString(a))
	}
	return b.String()
}

func toString(a any) string {
	switch v := a.(type) {
	case string:
		return v
	case error:
		return v.Error()
	default:
		return ""
	}
}

// TestLogger_EmitsOnPublishError confirms the warn path fires when
// the underlying publisher returns an error. Lets ops grep for
// "publish failed" in production logs to spot NATS instability.
func TestLogger_EmitsOnPublishError(t *testing.T) {
	pub := &alwaysFailPublisher{}
	cap := &captureLogger{}
	p, err := NewAsyncPublisher(pub, 8, 1, cap)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	p.Start()
	for i := 0; i < 3; i++ {
		_ = p.Enqueue("dev.mapexos.mqtt.data.org-1.asset-aaa", []byte("x"))
	}
	_ = p.Drain(2 * time.Second)

	if cap.count.Load() == 0 {
		t.Fatalf("expected logs to be emitted on publish failures, got zero")
	}
	if !strings.Contains(cap.String(), "WARN") {
		t.Fatalf("expected at least one WARN line, got: %s", cap.String())
	}
}

// TestLogger_StartAndDrainAnnouncements ensures the lifecycle
// boundaries are visible in logs — without these, an operator
// debugging "where is the plugin in its lifecycle" has no trace.
func TestLogger_StartAndDrainAnnouncements(t *testing.T) {
	pub := &recordingPublisher{}
	cap := &captureLogger{}
	p, _ := NewAsyncPublisher(pub, 8, 2, cap)
	p.Start()
	_ = p.Enqueue("subj", []byte("x"))
	_ = p.Drain(time.Second)

	out := cap.String()
	if !strings.Contains(out, "started") {
		t.Fatalf("expected start announcement in logs, got: %s", out)
	}
	if !strings.Contains(out, "drained") {
		t.Fatalf("expected drain summary in logs, got: %s", out)
	}
}

// TestLogger_MinLevelSuppressesDebug confirms LogInfo level filters
// out the debug noise so production logs stay clean. Critical: a
// chatty plugin at debug-level under 16k events/s would flood log
// aggregators.
func TestLogger_MinLevelSuppressesDebug(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, LogInfo)
	log.Debug("this should not appear")
	log.Info("this should appear")
	if strings.Contains(buf.String(), "should not appear") {
		t.Fatalf("LogInfo level leaked DEBUG line: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "should appear") {
		t.Fatalf("LogInfo line was suppressed: %s", buf.String())
	}
}
