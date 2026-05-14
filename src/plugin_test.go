package broker

import (
	"encoding/json"
	"testing"
	"time"
)

func newTestRuntime(t *testing.T, pub NatsPublisher, buf, workers int) *PluginRuntime {
	t.Helper()
	cfg, err := LoadConfig(map[string]string{
		"nats_url":     "nats://nats:4222",
		"auth_url":     "http://assets:5002/auth",
		"auth_api_key": "test",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	async, err := NewAsyncPublisher(pub, buf, workers, nil)
	if err != nil {
		t.Fatalf("async: %v", err)
	}
	async.Start()
	t.Cleanup(func() { _ = async.Drain(time.Second) })
	return &PluginRuntime{Config: cfg, Async: async}
}

func TestPublishConnect_EmitsAdvisoryWithEventConnect(t *testing.T) {
	rp := &recordingPublisher{}
	rt := newTestRuntime(t, rp, 8, 1)

	ts := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	if !rt.PublishConnect("org-1", "asset-aaa", "client-x", "1.2.3.4", ts) {
		t.Fatalf("PublishConnect dropped unexpectedly")
	}
	_ = rt.Async.Drain(time.Second)

	if rp.callCount() != 1 {
		t.Fatalf("expected 1 publish, got %d", rp.callCount())
	}
	rp.mu.Lock()
	subject := rp.calls[0][0]
	body := rp.calls[0][1]
	rp.mu.Unlock()
	if subject != DefaultSubjectPresence {
		t.Fatalf("subject = %q, want %q", subject, DefaultSubjectPresence)
	}

	var got PresenceAdvisory
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Event != EventConnect {
		t.Fatalf("event = %q, want %q", got.Event, EventConnect)
	}
	if got.OrgID != "org-1" || got.AssetUUID != "asset-aaa" {
		t.Fatalf("identity mismatch: %+v", got)
	}
}

func TestPublishDisconnect_TranslatesReasonCode(t *testing.T) {
	rp := &recordingPublisher{}
	rt := newTestRuntime(t, rp, 8, 1)

	if !rt.PublishDisconnect("org-1", "asset-aaa", "client-x", "1.2.3.4", 4, time.Now()) {
		t.Fatalf("PublishDisconnect dropped unexpectedly")
	}
	_ = rt.Async.Drain(time.Second)

	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.calls) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(rp.calls))
	}
	var got PresenceAdvisory
	if err := json.Unmarshal([]byte(rp.calls[0][1]), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Event != EventDisconnect {
		t.Fatalf("event = %q, want %q", got.Event, EventDisconnect)
	}
	if got.ReasonCode != 4 {
		t.Fatalf("reasonCode = %d, want 4", got.ReasonCode)
	}
	if got.ReasonText != "keepalive_timeout" {
		t.Fatalf("reasonText = %q", got.ReasonText)
	}
}

func TestPublishIngress_PerDeviceSubjectAndPayload(t *testing.T) {
	rp := &recordingPublisher{}
	rt := newTestRuntime(t, rp, 8, 1)

	payload := []byte(`{"temperature":23.4}`)
	if !rt.PublishIngress("org-1", "asset-aaa", "client-x", "events/asset-aaa/temperature", payload, 1, false, time.Now()) {
		t.Fatalf("PublishIngress dropped unexpectedly")
	}
	_ = rt.Async.Drain(time.Second)

	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.calls) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(rp.calls))
	}
	want := DefaultSubjectIngressPrefix + ".org-1.asset-aaa"
	if rp.calls[0][0] != want {
		t.Fatalf("subject = %q, want %q", rp.calls[0][0], want)
	}
	var got IngressMessage
	if err := json.Unmarshal([]byte(rp.calls[0][1]), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(got.Payload) != `{"temperature":23.4}` {
		t.Fatalf("payload survived JSON marshal incorrectly: %q", string(got.Payload))
	}
	if got.Topic != "events/asset-aaa/temperature" {
		t.Fatalf("topic = %q", got.Topic)
	}
}

func TestResolveUsername_DelegatesToParseUsername(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantAsset string
		wantOk    bool
	}{
		{"happy path", "asset-aaa", "asset-aaa", true},
		{"empty input", "", "", false},
		{"legacy colon shape rejected", "org-1:asset-aaa", "", false},
		{"trailing colon rejected", "asset-aaa:", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAsset, gotOk := ResolveUsername(tt.input)
			if gotOk != tt.wantOk || gotAsset != tt.wantAsset {
				t.Fatalf("ResolveUsername(%q) = (%q,%t), want (%q,%t)",
					tt.input, gotAsset, gotOk, tt.wantAsset, tt.wantOk)
			}
		})
	}
}
