package services

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/ingress/application/dtos"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/ingress/domain/constants"
	natsbus "github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/messaging/nats"
)

const testIngressPrefix = "test.mapexos.mqtt.data"

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

func (r *recordingPublisher) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newServiceWithFake(t *testing.T, rp *recordingPublisher) (*Service, *natsbus.AsyncPublisher) {
	t.Helper()
	async, err := natsbus.NewAsyncPublisher(rp, 16, 1, nil)
	if err != nil {
		t.Fatalf("async: %v", err)
	}
	async.Start()
	t.Cleanup(func() { _ = async.Drain(time.Second) })
	return New(async, testIngressPrefix, nil), async
}

func TestPublish_PerDeviceSubjectAndPayload(t *testing.T) {
	rp := &recordingPublisher{}
	svc, async := newServiceWithFake(t, rp)

	payload := []byte(`{"temperature":23.4}`)
	if !svc.Publish("org-1", "asset-aaa", "client-x", "events/asset-aaa/temperature", payload, 1, false, time.Now()) {
		t.Fatalf("Publish dropped unexpectedly")
	}
	_ = async.Drain(time.Second)

	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.calls) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(rp.calls))
	}
	want := testIngressPrefix + ".org-1.asset-aaa"
	if rp.calls[0][0] != want {
		t.Fatalf("subject = %q, want %q", rp.calls[0][0], want)
	}
	var got dtos.IngressMessage
	if err := json.Unmarshal([]byte(rp.calls[0][1]), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(got.Payload) != `{"temperature":23.4}` {
		t.Fatalf("payload survived marshal incorrectly: %q", string(got.Payload))
	}
	if got.Topic != "events/asset-aaa/temperature" {
		t.Fatalf("topic = %q", got.Topic)
	}
}

func TestPublish_RejectsInvalidSubjectTokens(t *testing.T) {
	rp := &recordingPublisher{}
	svc, async := newServiceWithFake(t, rp)

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
			before := rp.callCount()
			if svc.Publish(tt.orgID, tt.assetUUID, "client", "topic", []byte("p"), 0, false, time.Now()) {
				t.Fatalf("Publish must reject invalid token")
			}
			_ = async.Drain(time.Second)
			if rp.callCount() != before {
				t.Fatalf("invalid token reached NATS publisher")
			}
		})
	}
}

func TestPublish_DropsOversizedPayload(t *testing.T) {
	rp := &recordingPublisher{}
	svc, async := newServiceWithFake(t, rp)

	huge := bytes.Repeat([]byte("a"), constants.MaxIngressPayloadBytes+1)
	if svc.Publish("org-1", "asset-aaa", "client", "events/x", huge, 0, false, time.Now()) {
		t.Fatalf("Publish must drop payload > MaxIngressPayloadBytes")
	}
	_ = async.Drain(time.Second)
	if rp.callCount() != 0 {
		t.Fatalf("oversized payload reached NATS")
	}
}

func TestPublish_AcceptsPayloadAtCap(t *testing.T) {
	rp := &recordingPublisher{}
	svc, async := newServiceWithFake(t, rp)

	atCap := bytes.Repeat([]byte("a"), constants.MaxIngressPayloadBytes)
	if !svc.Publish("org-1", "asset-aaa", "client", "events/x", atCap, 0, false, time.Now()) {
		t.Fatalf("Publish must accept payload exactly at cap")
	}
	_ = async.Drain(time.Second)
}
