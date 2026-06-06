package bootstrap

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/config"
)

type recordingPublisher struct {
	mu    sync.Mutex
	calls int
}

func (r *recordingPublisher) Publish(_ string, _ []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return nil
}

func (r *recordingPublisher) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type captureLogger struct{ n atomic.Uint64 }

func (l *captureLogger) Debug(string, ...any) { l.n.Add(1) }
func (l *captureLogger) Info(string, ...any)  { l.n.Add(1) }
func (l *captureLogger) Warn(string, ...any)  { l.n.Add(1) }
func (l *captureLogger) Error(string, ...any) { l.n.Add(1) }

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(map[string]string{
		"nats_url":      "nats://nats:4222",
		"auth_url":      "http://assets:5002/auth",
		"auth_api_key":  "test",
		"cache_l1_path": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

func TestBuild_Success(t *testing.T) {
	pub := &recordingPublisher{}
	app, err := Build(Options{Config: testConfig(t), Publisher: pub, Log: nil})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Smoke: a presence connect flows through the async pool to the fake.
	app.Presence.PublishConnect("org-1", "asset-aaa", "client", "10.0.0.1", time.Now())
	app.Shutdown() // drains
	if pub.count() != 1 {
		t.Fatalf("expected 1 publish after shutdown drain, got %d", pub.count())
	}
}

func TestBuild_RejectsNilPublisher(t *testing.T) {
	if _, err := Build(Options{Config: testConfig(t)}); err == nil {
		t.Fatalf("expected error on nil publisher")
	}
}

func TestBuild_AnnouncesViaLogger(t *testing.T) {
	pub := &recordingPublisher{}
	cap := &captureLogger{}
	app, err := Build(Options{Config: testConfig(t), Publisher: pub, Log: cap})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer app.Shutdown()
	if cap.n.Load() == 0 {
		t.Fatalf("expected at least one log line announcing init, got zero")
	}
}
