package httpauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
)

// recordingDoer captures every HTTP request the AuthClient sends so tests
// can assert headers, URL, and call count without standing up a real server.
type recordingDoer struct {
	calls   atomic.Int32
	lastReq atomic.Pointer[http.Request]
	respFn  func(req *http.Request) (*http.Response, error)
}

func (d *recordingDoer) Do(req *http.Request) (*http.Response, error) {
	d.calls.Add(1)
	d.lastReq.Store(req)
	if d.respFn == nil {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	return d.respFn(req)
}

func newAuthClientWithDoer(t *testing.T, doer HTTPDoer) *AuthClient {
	t.Helper()
	a, err := NewAuthClient(AuthClientConfig{
		URL:    "http://assets:5002/internal/asset_auth",
		APIKey: "secret-test-key",
	}, nil)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	a.http = doer
	return a
}

func TestNewAuthClient_ValidatesArgs(t *testing.T) {
	tests := []struct {
		name string
		cfg  AuthClientConfig
	}{
		{"missing url", AuthClientConfig{APIKey: "k"}},
		{"missing api key", AuthClientConfig{URL: "http://x/internal/asset_auth"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewAuthClient(tt.cfg, nil); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}

// validReadModelBody is the platform's standard response envelope
// (`{status, errors, data}`) carrying the slim AuthProjection the
// /internal/asset_auth/:assetUUID endpoint returns.
const validReadModelBody = `{
    "status": 200,
    "errors": null,
    "data": {
        "assetUUID": "asset-aaa",
        "orgId": "507f1f77bcf86cd799439011",
        "enabled": true,
        "type": "mqtt",
        "mqtt": {
            "authType": "password",
            "passwordHash": "$2a$10$abcdefghij",
            "currentCertSerial": "DEADBEEF42"
        }
    }
}`

func TestLookupEntry_ReturnsEntryOn200(t *testing.T) {
	doer := &recordingDoer{
		respFn: func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(validReadModelBody)),
			}, nil
		},
	}
	a := newAuthClientWithDoer(t, doer)

	entry, err := a.LookupEntry(context.Background(), "asset-aaa")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if entry == nil {
		t.Fatalf("expected non-nil entry")
	}
	if entry.AssetUUID != "asset-aaa" {
		t.Errorf("AssetUUID = %q", entry.AssetUUID)
	}
	if entry.OrgId != "507f1f77bcf86cd799439011" {
		t.Errorf("OrgId = %q", entry.OrgId)
	}
	if !entry.Enabled {
		t.Errorf("Enabled = false")
	}
	if entry.PasswordHash != "$2a$10$abcdefghij" {
		t.Errorf("PasswordHash = %q", entry.PasswordHash)
	}
	if entry.CurrentCertSerial != "DEADBEEF42" {
		t.Errorf("CurrentCertSerial = %q", entry.CurrentCertSerial)
	}
	if a.LookupHitCount() != 1 || a.LookupCount() != 1 {
		t.Fatalf("counter mismatch: hits=%d lookups=%d", a.LookupHitCount(), a.LookupCount())
	}
}

func TestLookupEntry_NotFoundOn404(t *testing.T) {
	doer := &recordingDoer{
		respFn: func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	}
	a := newAuthClientWithDoer(t, doer)

	entry, err := a.LookupEntry(context.Background(), "asset-aaa")
	if !errors.Is(err, ports.ErrAuthEntryNotFound) {
		t.Fatalf("expected ErrAuthEntryNotFound, got %v", err)
	}
	if entry != nil {
		t.Fatalf("expected nil entry on 404, got %+v", entry)
	}
	if a.LookupMissCount() != 1 {
		t.Fatalf("miss count = %d", a.LookupMissCount())
	}
}

func TestLookupEntry_ErrorsOn5xx(t *testing.T) {
	doer := &recordingDoer{
		respFn: func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	}
	a := newAuthClientWithDoer(t, doer)

	_, err := a.LookupEntry(context.Background(), "asset-aaa")
	if err == nil {
		t.Fatalf("expected error on 503")
	}
	if a.LookupErrorCount() != 1 {
		t.Fatalf("error count = %d", a.LookupErrorCount())
	}
}

func TestLookupEntry_ErrorsOnNetworkFailure(t *testing.T) {
	doer := &recordingDoer{
		respFn: func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		},
	}
	a := newAuthClientWithDoer(t, doer)
	if _, err := a.LookupEntry(context.Background(), "asset-aaa"); err == nil {
		t.Fatalf("expected error on network failure")
	}
}

func TestLookupEntry_EmptyUUIDReturnsNotFound(t *testing.T) {
	a := newAuthClientWithDoer(t, &recordingDoer{})
	if _, err := a.LookupEntry(context.Background(), ""); !errors.Is(err, ports.ErrAuthEntryNotFound) {
		t.Fatalf("expected ErrAuthEntryNotFound for empty uuid, got %v", err)
	}
}

func TestLookupEntry_SendsApiKeyHeaderAndUUIDPath(t *testing.T) {
	doer := &recordingDoer{
		respFn: func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("X-API-Key"); got != "secret-test-key" {
				t.Errorf("X-API-Key header = %q, want secret-test-key", got)
			}
			if !strings.HasSuffix(req.URL.Path, "/internal/asset_auth/asset-aaa") {
				t.Errorf("URL path = %q, want suffix /internal/asset_auth/asset-aaa", req.URL.Path)
			}
			if req.Method != http.MethodGet {
				t.Errorf("method = %s, want GET", req.Method)
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(validReadModelBody)),
			}, nil
		},
	}
	a := newAuthClientWithDoer(t, doer)
	_, _ = a.LookupEntry(context.Background(), "asset-aaa")
}

// TestLookupEntry_AgainstRealServer exercises the actual http.Client (not
// the recordingDoer) against an in-process httptest.Server. This validates
// timeout + transport tuning + header propagation end-to-end.
func TestLookupEntry_AgainstRealServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-API-Key") != "real-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/internal/asset_auth/asset-aaa") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(validReadModelBody))
	}))
	defer srv.Close()

	a, err := NewAuthClient(AuthClientConfig{
		URL:     srv.URL + "/internal/asset_auth",
		APIKey:  "real-key",
		Timeout: 2 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	entry, err := a.LookupEntry(context.Background(), "asset-aaa")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if entry.PasswordHash != "$2a$10$abcdefghij" {
		t.Fatalf("PasswordHash = %q", entry.PasswordHash)
	}
}

func TestLookupEntry_TimeoutTriggersError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a, err := NewAuthClient(AuthClientConfig{
		URL:     srv.URL + "/internal/asset_auth",
		APIKey:  "k",
		Timeout: 50 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	if _, err := a.LookupEntry(context.Background(), "u"); err == nil {
		t.Fatalf("expected error on timeout")
	}
}

func TestCompareLocal_MatchesHash(t *testing.T) {
	// Generate a fresh hash at cost 4 (cheap for tests; bcrypt salts are
	// random per invocation, so a hardcoded fixture would be non-portable
	// across rebuilds).
	hash, err := bcrypt.GenerateFromPassword([]byte("good-password"), 4)
	if err != nil {
		t.Fatalf("generate hash: %v", err)
	}
	a := newAuthClientWithDoer(t, &recordingDoer{})
	if !a.CompareLocal(string(hash), "good-password") {
		t.Fatalf("expected CompareLocal to match")
	}
	if a.CompareLocal(string(hash), "WRONG") {
		t.Fatalf("expected CompareLocal to reject wrong password")
	}
	if a.CompareLocal("", "any") {
		t.Fatalf("empty hash must not match")
	}
	if a.CompareLocal(string(hash), "") {
		t.Fatalf("empty plaintext must not match")
	}
}
