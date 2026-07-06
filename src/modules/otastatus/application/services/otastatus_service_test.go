package services

import (
	"encoding/json"
	"testing"
	"time"

	ota "github.com/Mapex-Solutions/mapexGoKit/contracts/ota"
)

// fakePublisher records enqueues and drops.
type fakePublisher struct {
	subject string
	data    []byte
	enq     int
	drops   int
}

func (f *fakePublisher) Enqueue(subject string, data []byte) bool {
	f.enq++
	f.subject = subject
	f.data = data
	return true
}

func (f *fakePublisher) RecordDrop() { f.drops++ }

func TestHandleStatusReport_Table(t *testing.T) {
	ts := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		payload  []byte
		wantOK   bool
		wantEnq  int
		wantDrop int
	}{
		{
			name:    "valid report becomes an advisory",
			payload: []byte(`{"executionId":"e1","planId":"p1","status":"downloading","progress":42}`),
			wantOK:  true,
			wantEnq: 1,
		},
		{
			name:     "malformed json dropped",
			payload:  []byte("{oops"),
			wantDrop: 1,
		},
		{
			name:     "missing executionId dropped",
			payload:  []byte(`{"status":"updated"}`),
			wantDrop: 1,
		},
		{
			name:     "missing status dropped",
			payload:  []byte(`{"executionId":"e1"}`),
			wantDrop: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := &fakePublisher{}
			svc := New(pub, "dev.mapexos.ota.status.advisory", nil)

			ok := svc.HandleStatusReport("org1", "asset-1", tt.payload, ts)
			if ok != tt.wantOK {
				t.Fatalf("HandleStatusReport = %v, want %v", ok, tt.wantOK)
			}
			if pub.enq != tt.wantEnq {
				t.Fatalf("enqueues = %d, want %d", pub.enq, tt.wantEnq)
			}
			if pub.drops != tt.wantDrop {
				t.Fatalf("drops = %d, want %d", pub.drops, tt.wantDrop)
			}
			if !tt.wantOK {
				return
			}

			var adv ota.Advisory
			if err := json.Unmarshal(pub.data, &adv); err != nil {
				t.Fatalf("published data is not an Advisory: %v", err)
			}
			if adv.OrgID != "org1" || adv.AssetUUID != "asset-1" {
				t.Fatalf("identity not enriched from session: %+v", adv)
			}
			if adv.OTAExecutionID != "e1" || adv.Status != "downloading" || adv.Progress != 42 {
				t.Fatalf("report fields not mapped: %+v", adv)
			}
			if pub.subject != "dev.mapexos.ota.status.advisory" {
				t.Fatalf("subject = %q", pub.subject)
			}
		})
	}
}
