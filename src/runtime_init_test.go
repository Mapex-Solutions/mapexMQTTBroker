package broker

import (
	"testing"
	"time"
)

func TestBuildRuntime_Success(t *testing.T) {
	pub := &recordingPublisher{}
	cfg, err := LoadConfig(map[string]string{
		"nats_url": "nats://nats:4222", "auth_url": "http://assets:5002/auth", "auth_api_key": "test", "cache_l1_path": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	rt, err := BuildRuntime(RuntimeOptions{
		Config:    cfg,
		Publisher: pub,
		Log:       nil, // exercise the nopLogger fallback
	})
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	defer rt.Async.Drain(time.Second)

	if rt.Config.SubjectPresence != cfg.SubjectPresence {
		t.Fatalf("config not propagated to runtime")
	}
	// Smoke: enqueue + drain works without panicking.
	if !rt.PublishConnect("org-1", "asset-aaa", "client", "10.0.0.1", time.Now()) {
		t.Fatalf("PublishConnect failed on freshly built runtime")
	}
	if !rt.Async.Drain(time.Second) {
		t.Fatalf("drain timed out")
	}
	if rt.Async.PublishedCount() != 1 {
		t.Fatalf("expected 1 published, got %d", rt.Async.PublishedCount())
	}
}

func TestBuildRuntime_RejectsNilPublisher(t *testing.T) {
	cfg, _ := LoadConfig(map[string]string{"nats_url": "nats://nats:4222", "auth_url": "http://assets:5002/auth", "auth_api_key": "test", "cache_l1_path": t.TempDir()})
	if _, err := BuildRuntime(RuntimeOptions{Config: cfg}); err == nil {
		t.Fatalf("expected error on nil publisher")
	}
}

func TestBuildRuntime_AnnouncesViaLogger(t *testing.T) {
	pub := &recordingPublisher{}
	cap := &captureLogger{}
	cfg, _ := LoadConfig(map[string]string{"nats_url": "nats://nats:4222", "auth_url": "http://assets:5002/auth", "auth_api_key": "test", "cache_l1_path": t.TempDir()})
	rt, err := BuildRuntime(RuntimeOptions{Config: cfg, Publisher: pub, Log: cap})
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	defer rt.Async.Drain(time.Second)

	if cap.count.Load() == 0 {
		t.Fatalf("expected at least one log line announcing init, got zero")
	}
}
