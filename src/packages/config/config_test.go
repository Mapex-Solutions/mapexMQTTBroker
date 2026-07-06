package config

import "testing"

func TestLoadConfig_MissingNatsURLFails(t *testing.T) {
	if _, err := Load(map[string]string{}); err == nil {
		t.Fatalf("expected error when nats_url is missing")
	}
}

func TestLoadConfig_AppliesDefaults(t *testing.T) {
	cfg, err := Load(map[string]string{
		"nats_url":     "nats://nats:4222",
		"auth_url":     "http://assets:5002/internal/assets/auth/mqtt",
		"auth_api_key": "test-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SubjectPresence != DefaultSubjectPresence {
		t.Fatalf("default presence subject not applied: got %q", cfg.SubjectPresence)
	}
	if cfg.SubjectIngressPrefix != DefaultSubjectIngressPrefix {
		t.Fatalf("default ingress prefix not applied: got %q", cfg.SubjectIngressPrefix)
	}
	if cfg.WorkerPoolSize != DefaultWorkerPoolSize {
		t.Fatalf("default worker pool size not applied: got %d", cfg.WorkerPoolSize)
	}
	if cfg.BufferSize != DefaultBufferSize {
		t.Fatalf("default buffer size not applied: got %d", cfg.BufferSize)
	}
}

func TestLoadConfig_AcceptsOverrides(t *testing.T) {
	cfg, err := Load(map[string]string{
		"nats_url":                    "nats://prod:4222",
		"auth_url":                    "http://assets:5002/auth",
		"auth_api_key":                "prod-key",
		"nats_subject_presence":       "prod.mapexos.presence.advisory",
		"nats_subject_ingress_prefix": "prod.mapexos.mqtt.data",
		"worker_pool_size":            "8",
		"buffer_size":                 "20000",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.NatsURL != "nats://prod:4222" {
		t.Fatalf("nats_url = %q", cfg.NatsURL)
	}
	if cfg.SubjectPresence != "prod.mapexos.presence.advisory" {
		t.Fatalf("presence subject = %q", cfg.SubjectPresence)
	}
	if cfg.SubjectIngressPrefix != "prod.mapexos.mqtt.data" {
		t.Fatalf("ingress prefix = %q", cfg.SubjectIngressPrefix)
	}
	if cfg.WorkerPoolSize != 8 {
		t.Fatalf("worker pool = %d", cfg.WorkerPoolSize)
	}
	if cfg.BufferSize != 20000 {
		t.Fatalf("buffer size = %d", cfg.BufferSize)
	}
}

func TestLoadConfig_TrimsWhitespace(t *testing.T) {
	cfg, err := Load(map[string]string{
		"nats_url":              "  nats://nats:4222  ",
		"auth_url":              "  http://assets:5002/auth  ",
		"auth_api_key":          "  trim-me  ",
		"nats_subject_presence": "  ",
		"worker_pool_size":      "  16  ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.NatsURL != "nats://nats:4222" {
		t.Fatalf("expected trimmed url, got %q", cfg.NatsURL)
	}
	if cfg.SubjectPresence != DefaultSubjectPresence {
		t.Fatalf("whitespace-only override should fall back to default, got %q", cfg.SubjectPresence)
	}
	if cfg.WorkerPoolSize != 16 {
		t.Fatalf("trimmed int not parsed: got %d", cfg.WorkerPoolSize)
	}
}

func TestLoadConfig_FallsBackOnInvalidIntegers(t *testing.T) {
	tests := []struct {
		name string
		val  string
	}{
		{"non-numeric", "abc"},
		{"zero", "0"},
		{"negative", "-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(map[string]string{
				"nats_url":         "nats://nats:4222",
				"auth_url":         "http://assets:5002/auth",
				"auth_api_key":     "test",
				"worker_pool_size": tt.val,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.WorkerPoolSize != DefaultWorkerPoolSize {
				t.Fatalf("expected fallback to default, got %d", cfg.WorkerPoolSize)
			}
		})
	}
}
