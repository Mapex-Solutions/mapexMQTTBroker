package services

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/presence/application/dtos"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/presence/domain/constants"
	natsbus "github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/messaging/nats"
)

const testPresenceSubject = "test.mapexos.mqtt.presence.advisory"

// recordingPublisher captures the (subject, payload) pairs the async pool
// drains so a presence test can assert what reached NATS.
type recordingPublisher struct {
	mu    sync.Mutex
	calls [][2]string
}

func (r *recordingPublisher) Publish(subject string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [2]string{subject, string(data)})
	return nil
}

func newServiceWithFake(t *testing.T, rp *recordingPublisher) (*Service, *natsbus.AsyncPublisher) {
	t.Helper()
	async, err := natsbus.NewAsyncPublisher(rp, 8, 1, nil)
	if err != nil {
		t.Fatalf("async: %v", err)
	}
	async.Start()
	t.Cleanup(func() { _ = async.Drain(time.Second) })
	return New(async, testPresenceSubject, nil), async
}

func TestPublishConnect_EmitsAdvisoryWithEventConnect(t *testing.T) {
	rp := &recordingPublisher{}
	svc, async := newServiceWithFake(t, rp)

	ts := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	if !svc.PublishConnect("org-1", "asset-aaa", "client-x", "1.2.3.4", ts) {
		t.Fatalf("PublishConnect dropped unexpectedly")
	}
	_ = async.Drain(time.Second)

	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.calls) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(rp.calls))
	}
	if rp.calls[0][0] != testPresenceSubject {
		t.Fatalf("subject = %q, want %q", rp.calls[0][0], testPresenceSubject)
	}
	var got dtos.PresenceAdvisory
	if err := json.Unmarshal([]byte(rp.calls[0][1]), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Event != constants.EventConnect {
		t.Fatalf("event = %q, want %q", got.Event, constants.EventConnect)
	}
	if got.OrgID != "org-1" || got.AssetUUID != "asset-aaa" {
		t.Fatalf("identity mismatch: %+v", got)
	}
}

func TestPublishDisconnect_TranslatesReasonCode(t *testing.T) {
	rp := &recordingPublisher{}
	svc, async := newServiceWithFake(t, rp)

	if !svc.PublishDisconnect("org-1", "asset-aaa", "client-x", "1.2.3.4", 4, time.Now()) {
		t.Fatalf("PublishDisconnect dropped unexpectedly")
	}
	_ = async.Drain(time.Second)

	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.calls) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(rp.calls))
	}
	var got dtos.PresenceAdvisory
	if err := json.Unmarshal([]byte(rp.calls[0][1]), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Event != constants.EventDisconnect {
		t.Fatalf("event = %q, want %q", got.Event, constants.EventDisconnect)
	}
	if got.ReasonCode != 4 || got.ReasonText != "keepalive_timeout" {
		t.Fatalf("reason mismatch: code=%d text=%q", got.ReasonCode, got.ReasonText)
	}
}
