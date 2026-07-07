package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	gokitconfig "github.com/Mapex-Solutions/mapexGoKit/config"
)

// TestLoad_AppliesDefaults verifies a bare dev run resolves every setting to its
// declared default and derives AuthURL from the assets host/port defaults.
func TestLoad_AppliesDefaults(t *testing.T) {
	gokitconfig.ResetSingletonForTest()
	t.Setenv("GO_ENV", "dev")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SubjectPresence != DefaultSubjectPresence {
		t.Errorf("SubjectPresence = %q, want default %q", cfg.SubjectPresence, DefaultSubjectPresence)
	}
	if cfg.SubjectIngressPrefix != DefaultSubjectIngressPrefix {
		t.Errorf("SubjectIngressPrefix = %q, want default", cfg.SubjectIngressPrefix)
	}
	if cfg.WorkerPoolSize != DefaultWorkerPoolSize {
		t.Errorf("WorkerPoolSize = %d, want %d", cfg.WorkerPoolSize, DefaultWorkerPoolSize)
	}
	if cfg.BufferSize != DefaultBufferSize {
		t.Errorf("BufferSize = %d, want %d", cfg.BufferSize, DefaultBufferSize)
	}
	if cfg.CacheL2Bucket != DefaultCacheL2Bucket {
		t.Errorf("CacheL2Bucket = %q, want fixed %q", cfg.CacheL2Bucket, DefaultCacheL2Bucket)
	}
	want := "http://" + DefaultAssetsHost + ":" + DefaultAssetsPort + "/internal/asset_auth"
	if cfg.AuthURL != want {
		t.Errorf("AuthURL = %q, want %q", cfg.AuthURL, want)
	}
	if !cfg.CacheL2AuthIsNeeded {
		t.Errorf("CacheL2AuthIsNeeded = false, want true by default")
	}
}

// TestLoad_ResolvesFromEnv verifies env overrides flow into the Config and that
// AuthURL is composed from ASSETS_HOST + ASSETS_PORT, timeouts from seconds, and
// the object-store booleans parse.
func TestLoad_ResolvesFromEnv(t *testing.T) {
	gokitconfig.ResetSingletonForTest()
	t.Setenv("GO_ENV", "dev")
	t.Setenv("NATS_URL", "nats://custom:4222")
	t.Setenv("ASSETS_HOST", "assets-svc")
	t.Setenv("ASSETS_PORT", "6000")
	t.Setenv("AUTH_TIMEOUT_SECONDS", "12")
	t.Setenv("NATS_SUBJECT_PRESENCE", "prod.mapexos.presence.advisory")
	t.Setenv("PLUGIN_WORKER_POOL_SIZE", "8")
	t.Setenv("OBJECT_STORE_ENDPOINT", "minio:9000")
	t.Setenv("OBJECT_STORE_USE_SSL", "true")
	t.Setenv("OBJECT_STORE_AUTH_IS_NEEDED", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.NatsURL != "nats://custom:4222" {
		t.Errorf("NatsURL = %q", cfg.NatsURL)
	}
	if cfg.AuthURL != "http://assets-svc:6000/internal/asset_auth" {
		t.Errorf("AuthURL = %q, want derived from host+port", cfg.AuthURL)
	}
	if cfg.AuthTimeout.Seconds() != 12 {
		t.Errorf("AuthTimeout = %v, want 12s", cfg.AuthTimeout)
	}
	if cfg.SubjectPresence != "prod.mapexos.presence.advisory" {
		t.Errorf("SubjectPresence = %q", cfg.SubjectPresence)
	}
	if cfg.WorkerPoolSize != 8 {
		t.Errorf("WorkerPoolSize = %d, want 8", cfg.WorkerPoolSize)
	}
	if cfg.CacheL2Endpoint != "minio:9000" {
		t.Errorf("CacheL2Endpoint = %q", cfg.CacheL2Endpoint)
	}
	if !cfg.CacheL2UseSSL {
		t.Errorf("CacheL2UseSSL = false, want true")
	}
	if cfg.CacheL2AuthIsNeeded {
		t.Errorf("CacheL2AuthIsNeeded = true, want false when OBJECT_STORE_AUTH_IS_NEEDED=false")
	}
}

// TestLoad_GuardPassesWhenOverriddenInProd verifies that a non-dev GO_ENV with
// every sensitive credential overridden away from its dev default boots without
// the production guard aborting.
func TestLoad_GuardPassesWhenOverriddenInProd(t *testing.T) {
	gokitconfig.ResetSingletonForTest()
	t.Setenv("GO_ENV", "prod")
	t.Setenv("NATS_URL", "nats://user:realsecret@prod-nats:4222")
	t.Setenv("INTERNAL_API_KEY", "prod-real-api-key")
	t.Setenv("OBJECT_STORE_ACCESS_KEY", "prod-access")
	t.Setenv("OBJECT_STORE_SECRET_KEY", "prod-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthAPIKey != "prod-real-api-key" {
		t.Errorf("AuthAPIKey = %q, want the prod override", cfg.AuthAPIKey)
	}
}

// TestLoad_GuardAbortsOnProdDefault verifies the production guard refuses to
// start when a non-dev GO_ENV leaves sensitive credentials at their dev
// defaults. The guard calls log.Fatalf (os.Exit), so the abort path runs in a
// subprocess and the parent asserts a non-zero exit.
func TestLoad_GuardAbortsOnProdDefault(t *testing.T) {
	if os.Getenv("BROKER_GUARD_CRASH") == "1" {
		// Child: GO_ENV=prod with the sensitive env vars absent means Load
		// resolves them to dev defaults, which the guard must reject.
		_, _ = Load()
		// Unreachable when the guard fires; exit 0 so the parent fails loudly
		// if it did not.
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestLoad_GuardAbortsOnProdDefault$", "-test.v")
	cmd.Env = append(sanitizedEnv(), "BROKER_GUARD_CRASH=1", "GO_ENV=prod")

	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok && !exitErr.Success() {
		return // expected: the guard aborted the child process
	}
	t.Fatalf("expected the guard to abort the process, got err=%v", err)
}

// sanitizedEnv returns the current environment with the broker's sensitive keys
// stripped so the subprocess resolves them to their dev defaults and triggers
// the guard.
func sanitizedEnv() []string {
	stripped := map[string]bool{
		"NATS_URL":                true,
		"INTERNAL_API_KEY":        true,
		"OBJECT_STORE_ACCESS_KEY": true,
		"OBJECT_STORE_SECRET_KEY": true,
	}
	var out []string
	for _, kv := range os.Environ() {
		idx := strings.IndexByte(kv, '=')
		if idx > 0 && stripped[kv[:idx]] {
			continue
		}
		out = append(out, kv)
	}
	return out
}
