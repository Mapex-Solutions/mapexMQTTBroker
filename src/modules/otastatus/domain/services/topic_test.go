package services

import "testing"

func TestIsStatusTopic(t *testing.T) {
	tests := []struct {
		name  string
		topic string
		want  bool
	}{
		{"ota status", "events/asset-1/ota_status", true},
		{"regular telemetry", "events/asset-1/temperature", false},
		{"commands prefix", "commands/asset-1/ota_status", false},
		{"missing event type", "events/asset-1", false},
		{"extra tokens", "events/asset-1/ota_status/extra", false},
		{"empty asset", "events//ota_status", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStatusTopic(tt.topic); got != tt.want {
				t.Fatalf("IsStatusTopic(%q) = %v, want %v", tt.topic, got, tt.want)
			}
		})
	}
}
