package services

import "testing"

func TestMapDisconnectReason(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{0, "clean_disconnect"},
		{4, "keepalive_timeout"},
		{142, "session_taken_over"},
		{152, "admin_action"},
		{999, "unknown_999"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := MapDisconnectReason(tt.code); got != tt.want {
				t.Fatalf("MapDisconnectReason(%d) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}
