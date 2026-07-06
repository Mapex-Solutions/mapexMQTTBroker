package services

import (
	"testing"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/constants"
)

func TestCheckACL_Table(t *testing.T) {
	tests := []struct {
		name     string
		username string
		topic    string
		acc      int
		want     bool
	}{
		{
			name:     "events publish allowed",
			username: "asset-aaa",
			topic:    "events/asset-aaa/temperature",
			acc:      constants.AccessWrite,
			want:     true,
		},
		{
			name:     "events publish nested topic allowed",
			username: "asset-aaa",
			topic:    "events/asset-aaa/sensor/0/value",
			acc:      constants.AccessWrite,
			want:     true,
		},
		{
			name:     "events read denied (device cannot subscribe to events)",
			username: "asset-aaa",
			topic:    "events/asset-aaa/temperature",
			acc:      constants.AccessRead,
			want:     false,
		},
		{
			name:     "events subscribe denied",
			username: "asset-aaa",
			topic:    "events/asset-aaa/temperature",
			acc:      constants.AccessSubscribe,
			want:     false,
		},
		{
			// OTA status report — the device publishes progress on its own
			// events topic; the existing contract already covers it.
			name:     "ota status publish allowed (own asset)",
			username: "asset-aaa",
			topic:    "events/asset-aaa/ota_status",
			acc:      constants.AccessWrite,
			want:     true,
		},
		{
			name:     "ota status publish denied (foreign asset)",
			username: "asset-aaa",
			topic:    "events/asset-bbb/ota_status",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			// OTA command delivery — the device subscribes to its own
			// commands topic; the downlink module publishes there.
			name:     "ota command subscribe allowed (own asset)",
			username: "asset-aaa",
			topic:    "commands/asset-aaa/ota_update",
			acc:      constants.AccessSubscribe,
			want:     true,
		},
		{
			name:     "ota command subscribe denied (foreign asset)",
			username: "asset-aaa",
			topic:    "commands/asset-bbb/ota_update",
			acc:      constants.AccessSubscribe,
			want:     false,
		},
		{
			name:     "ota command publish denied (devices never publish commands)",
			username: "asset-aaa",
			topic:    "commands/asset-aaa/ota_update",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			name:     "commands subscribe allowed",
			username: "asset-aaa",
			topic:    "commands/asset-aaa/relay",
			acc:      constants.AccessSubscribe,
			want:     true,
		},
		{
			name:     "commands read allowed (broker delivering message)",
			username: "asset-aaa",
			topic:    "commands/asset-aaa/relay",
			acc:      constants.AccessRead,
			want:     true,
		},
		{
			name:     "commands write denied (device cannot publish to its own command channel)",
			username: "asset-aaa",
			topic:    "commands/asset-aaa/relay",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			name:     "wrong asset uuid denied",
			username: "asset-aaa",
			topic:    "events/asset-bbb/temperature",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			name:     "unknown prefix denied",
			username: "asset-aaa",
			topic:    "internal/asset-aaa/whatever",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			name:     "legacy colon username denied",
			username: "org-1:asset-aaa",
			topic:    "events/asset-aaa/x",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			name:     "empty username denied",
			username: "",
			topic:    "events/asset-aaa/x",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			name:     "topic with one segment denied (missing assetUUID)",
			username: "asset-aaa",
			topic:    "events",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			name:     "empty topic denied",
			username: "asset-aaa",
			topic:    "",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			name:     "unsubscribe code never matches",
			username: "asset-aaa",
			topic:    "events/asset-aaa/x",
			acc:      constants.AccessUnsubscribe,
			want:     false,
		},
		{
			name:     "subscribe to own asset with wildcard at command-type level",
			username: "asset-aaa",
			topic:    "commands/asset-aaa/+",
			acc:      constants.AccessSubscribe,
			want:     true,
		},
		{
			name:     "subscribe to own asset with multi-level wildcard #",
			username: "asset-aaa",
			topic:    "commands/asset-aaa/#",
			acc:      constants.AccessSubscribe,
			want:     true,
		},
		{
			name:     "subscribe with wildcard at asset slot denied (cross-asset attempt)",
			username: "asset-aaa",
			topic:    "commands/+/relay",
			acc:      constants.AccessSubscribe,
			want:     false,
		},
		{
			name:     "publish with literal + in topic denied (defense in depth)",
			username: "asset-aaa",
			topic:    "events/+/x",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			name:     "topic with leading slash denied",
			username: "asset-aaa",
			topic:    "/events/asset-aaa/x",
			acc:      constants.AccessWrite,
			want:     false,
		},
		{
			name:     "topic with $SYS prefix denied",
			username: "asset-aaa",
			topic:    "$SYS/broker/clients/connected",
			acc:      constants.AccessRead,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckACL(tt.username, tt.topic, tt.acc)
			if got != tt.want {
				t.Fatalf("CheckACL(%q, %q, %d) = %t, want %t",
					tt.username, tt.topic, tt.acc, got, tt.want)
			}
		})
	}
}
