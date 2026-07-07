package bootstrap

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gokitconfig "github.com/Mapex-Solutions/mapexGoKit/config"
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
	gokitconfig.ResetSingletonForTest()
	t.Setenv("GO_ENV", "dev")
	t.Setenv("NATS_URL", "nats://nats:4222")
	t.Setenv("ASSETS_HOST", "assets")
	t.Setenv("ASSETS_PORT", "5002")
	t.Setenv("INTERNAL_API_KEY", "test")
	t.Setenv("CACHE_L1_PATH", t.TempDir())
	cfg, err := config.Load()
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
