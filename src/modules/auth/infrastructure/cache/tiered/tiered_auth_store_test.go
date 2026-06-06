package tiered

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/entities"
	httpauth "github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/infrastructure/lookup/http"
)

// realFallbackClient builds an AuthClient pointed at an httptest server so
// tests of the full tiered path don't need to mock the HTTP layer.
func realFallbackClient(t *testing.T, srvURL string) *httpauth.AuthClient {
	t.Helper()
	a, err := httpauth.NewAuthClient(httpauth.AuthClientConfig{
		URL:    srvURL + "/internal/asset-auth",
		APIKey: "test",
	}, nil)
	if err != nil {
		t.Fatalf("ctor auth client: %v", err)
	}
	return a
}

func newL1OnlyStore(t *testing.T) *TieredAuthStore {
	t.Helper()
	dir := t.TempDir()
	cli, _ := httpauth.NewAuthClient(httpauth.AuthClientConfig{URL: "http://nowhere/auth", APIKey: "k"}, nil)
	store, err := NewTieredAuthStore(TieredAuthStoreConfig{
		L1Path:   filepath.Join(dir, "pebble"),
		L3Client: cli,
		L1TTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("ctor store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestNewTieredAuthStore_RequiresL3 verifies init fails fast when no
// fallback is configured — without L3, a fresh deploy with empty L1 +
// missing L2 entry would deny every CONNECT silently.
func TestNewTieredAuthStore_RequiresL3(t *testing.T) {
	if _, err := NewTieredAuthStore(TieredAuthStoreConfig{}); err == nil {
		t.Fatalf("expected error when L3Client is nil")
	}
}

// TestTieredAuthStore_L1Hit confirms a previously-cached entry comes back
// from Pebble without touching downstream layers.
func TestTieredAuthStore_L1Hit(t *testing.T) {
	store := newL1OnlyStore(t)
	want := &entities.AuthEntry{
		Enabled: true, AssetUUID: "asset-aaa", OrgId: "org-1",
		PasswordHash: "$2a$10$...",
	}
	store.writeL1("asset-aaa", want)

	got, err := store.Get(context.Background(), "asset-aaa")
	if err != nil {
		t.Fatalf("L1 hit returned error: %v", err)
	}
	if got.PasswordHash != want.PasswordHash || got.OrgId != want.OrgId {
		t.Fatalf("L1 hit returned wrong entry: %+v", got)
	}
	if store.L1HitCount() != 1 {
		t.Fatalf("L1 hit counter not advanced")
	}
}

// TestTieredAuthStore_L3Fallback confirms a miss in L1 + no L2 configured
// falls through to L3 and warms L1 on the way back.
func TestTieredAuthStore_L3Fallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/asset-aaa") {
			w.WriteHeader(200)
			// L3 lookup returns the assets service standard response
			// envelope wrapping AssetReadModel. The broker plugin projects
			// out the four fields it needs.
			_, _ = io.WriteString(w, `{"status":200,"errors":null,"data":{"assetUUID":"asset-aaa","orgId":"org-1","enabled":true,"type":"mqtt","mqtt":{"authType":"password","passwordHash":"hash"}}}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := NewTieredAuthStore(TieredAuthStoreConfig{
		L1Path:   filepath.Join(dir, "pebble"),
		L3Client: realFallbackClient(t, srv.URL),
		L1TTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	defer store.Close()

	entry, err := store.Get(context.Background(), "asset-aaa")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if entry.OrgId != "org-1" {
		t.Fatalf("L3 entry decoded wrong: %+v", entry)
	}
	if store.L3HitCount() != 1 {
		t.Fatalf("L3 hit counter not advanced")
	}

	// Second Get must hit L1 (no extra HTTP call).
	if _, err := store.Get(context.Background(), "asset-aaa"); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if store.L1HitCount() != 1 {
		t.Fatalf("expected L1 hit after warmup, got count=%d", store.L1HitCount())
	}
}

// TestTieredAuthStore_L3NotFound returns ErrAuthEntryNotFound when the L3
// endpoint responds 404 — caller maps to deny.
func TestTieredAuthStore_L3NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, _ := NewTieredAuthStore(TieredAuthStoreConfig{
		L1Path:   filepath.Join(dir, "pebble"),
		L3Client: realFallbackClient(t, srv.URL),
	})
	defer store.Close()

	_, err := store.Get(context.Background(), "ghost")
	if !errors.Is(err, ports.ErrAuthEntryNotFound) {
		t.Fatalf("expected ErrAuthEntryNotFound, got %v", err)
	}
}

// TestTieredAuthStore_Invalidate drops the L1 entry; next Get re-fetches via
// L3.
func TestTieredAuthStore_Invalidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		// L3 returns the standard envelope; broker projects out.
		_, _ = io.WriteString(w, `{"status":200,"errors":null,"data":{"assetUUID":"asset-aaa","orgId":"org-1","enabled":true,"type":"mqtt","mqtt":{"authType":"password","passwordHash":"x"}}}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, _ := NewTieredAuthStore(TieredAuthStoreConfig{
		L1Path:   filepath.Join(dir, "pebble"),
		L3Client: realFallbackClient(t, srv.URL),
	})
	defer store.Close()

	// Warmup → L1 populated
	if _, err := store.Get(context.Background(), "asset-aaa"); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	if store.L3HitCount() != 1 {
		t.Fatalf("expected 1 L3 hit on warmup")
	}

	// Invalidate → L1 dropped
	if err := store.Invalidate(context.Background(), "asset-aaa"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	// Next Get must miss L1 and refetch L3
	if _, err := store.Get(context.Background(), "asset-aaa"); err != nil {
		t.Fatalf("post-invalidate Get: %v", err)
	}
	if store.L3HitCount() != 2 {
		t.Fatalf("expected 2 L3 hits after invalidation, got %d", store.L3HitCount())
	}
}
