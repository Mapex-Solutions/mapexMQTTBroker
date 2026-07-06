package services

import (
	"encoding/json"
	"errors"
	"testing"

	downlink "github.com/Mapex-Solutions/mapexGoKit/contracts/downlink"
)

// fakeDeliverer records deliveries and optionally fails.
type fakeDeliverer struct {
	topic   string
	payload []byte
	qos     int
	calls   int
	err     error
}

func (f *fakeDeliverer) Deliver(topic string, payload []byte, qos int) error {
	f.calls++
	f.topic = topic
	f.payload = payload
	f.qos = qos
	return f.err
}

func envelopeBytes(t *testing.T, orgID, assetUUID, commandType string, payload any) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	data, err := json.Marshal(downlink.Envelope{
		OrgID: orgID, AssetUUID: assetUUID, CommandType: commandType, Payload: body,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return data
}

func TestHandleMessage_Table(t *testing.T) {
	cmd := downlink.OTAUpdateCommand{PlanID: "p1", ExecutionID: "e1", TargetVersion: "2.0.0"}

	tests := []struct {
		name         string
		data         []byte
		deliverErr   error
		wantErr      error
		wantCalls    int
		wantTopic    string
		wantQoS      int
		checkPayload bool
	}{
		{
			name:         "valid envelope delivered to the device command topic",
			data:         envelopeBytes(t, "org1", "asset-1", downlink.CommandTypeOTAUpdate, cmd),
			wantCalls:    1,
			wantTopic:    "commands/asset-1/ota_update",
			wantQoS:      1,
			checkPayload: true,
		},
		{
			name:    "malformed json is TERM-able",
			data:    []byte("{not-json"),
			wantErr: ErrMalformedEnvelope,
		},
		{
			name:    "missing assetUUID is TERM-able",
			data:    envelopeBytes(t, "org1", "", downlink.CommandTypeOTAUpdate, cmd),
			wantErr: ErrMalformedEnvelope,
		},
		{
			name:    "missing commandType is TERM-able",
			data:    envelopeBytes(t, "org1", "asset-1", "", cmd),
			wantErr: ErrMalformedEnvelope,
		},
		{
			name:       "delivery failure is swallowed (reconciler retries)",
			data:       envelopeBytes(t, "org1", "asset-1", downlink.CommandTypeOTAUpdate, cmd),
			deliverErr: errors.New("broker down"),
			wantCalls:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deliverer := &fakeDeliverer{err: tt.deliverErr}
			svc := New(deliverer, nil)

			err := svc.HandleMessage(tt.data)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("HandleMessage err = %v, want %v", err, tt.wantErr)
			}
			if deliverer.calls != tt.wantCalls {
				t.Fatalf("deliver calls = %d, want %d", deliverer.calls, tt.wantCalls)
			}
			if tt.wantTopic != "" && deliverer.topic != tt.wantTopic {
				t.Fatalf("topic = %q, want %q", deliverer.topic, tt.wantTopic)
			}
			if tt.wantQoS != 0 && deliverer.qos != tt.wantQoS {
				t.Fatalf("qos = %d, want %d", deliverer.qos, tt.wantQoS)
			}
			if tt.checkPayload {
				var got downlink.OTAUpdateCommand
				if err := json.Unmarshal(deliverer.payload, &got); err != nil {
					t.Fatalf("delivered payload is not the command JSON: %v", err)
				}
				if got != cmd {
					t.Fatalf("delivered command = %+v, want %+v", got, cmd)
				}
			}
		})
	}
}
