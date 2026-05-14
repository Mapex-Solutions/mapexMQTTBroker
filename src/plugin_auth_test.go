package broker

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// stubAuthStore is the in-memory AuthStore used by plugin auth path
// tests. Lets each test seed entries by username and assert the
// invalidation count.
type stubAuthStore struct {
	entries       map[string]*AuthEntry
	getErr        error
	invalidations int
}

func newStubAuthStore() *stubAuthStore {
	return &stubAuthStore{entries: map[string]*AuthEntry{}}
}

func (s *stubAuthStore) put(assetUUID string, entry *AuthEntry) { s.entries[assetUUID] = entry }

func (s *stubAuthStore) Get(_ context.Context, assetUUID string) (*AuthEntry, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	e, ok := s.entries[assetUUID]
	if !ok {
		return nil, ErrAuthEntryNotFound
	}
	return e, nil
}

func (s *stubAuthStore) Invalidate(_ context.Context, assetUUID string) error {
	s.invalidations++
	delete(s.entries, assetUUID)
	return nil
}

func (s *stubAuthStore) Close() error { return nil }

func newRuntimeWithStore(store AuthStore) *PluginRuntime {
	pub := &recordingPublisher{}
	async, _ := NewAsyncPublisher(pub, 16, 1, nil)
	async.Start()
	authClient, _ := NewAuthClient(AuthClientConfig{
		URL: "http://nowhere/auth", APIKey: "k",
	}, nil)
	return &PluginRuntime{
		Config: Config{},
		Async:  async,
		Auth:   authClient,
		Store:  store,
		Log:    nopLogger{},
	}
}

func bcryptHash(t *testing.T, plain string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(h)
}

func TestAuthenticate_PasswordAllow(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType:     AuthTypePassword,
		PasswordHash: bcryptHash(t, "secret"),
	})
	rt := newRuntimeWithStore(store)
	defer rt.Async.Drain(time.Second)

	res := rt.Authenticate(context.Background(), "asset-aaa", "secret", "client-x", "")
	if res != AuthAllow {
		t.Fatalf("expected AuthAllow, got %d", res)
	}

	// Session map must carry the trusted orgId so later callbacks can
	// publish ingress without re-reading the auth projection.
	info, ok := rt.LookupSession("client-x")
	if !ok || info.OrgID != "org-1" || info.AssetUUID != "asset-aaa" {
		t.Fatalf("expected session for client-x with orgId=org-1, got %+v ok=%v", info, ok)
	}
}

func TestAuthenticate_PasswordMismatchDeny(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType:     AuthTypePassword,
		PasswordHash: bcryptHash(t, "secret"),
	})
	rt := newRuntimeWithStore(store)
	defer rt.Async.Drain(time.Second)

	if res := rt.Authenticate(context.Background(), "asset-aaa", "wrong", "c", ""); res != AuthDeny {
		t.Fatalf("expected AuthDeny on password mismatch, got %d", res)
	}
}

func TestAuthenticate_DisabledDeny(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &AuthEntry{
		Enabled: false, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType:     AuthTypePassword,
		PasswordHash: bcryptHash(t, "secret"),
	})
	rt := newRuntimeWithStore(store)
	defer rt.Async.Drain(time.Second)

	if res := rt.Authenticate(context.Background(), "asset-aaa", "secret", "c", ""); res != AuthDeny {
		t.Fatalf("expected AuthDeny on disabled asset, got %d", res)
	}
}

func TestAuthenticate_CertSerialAllow(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType:          AuthTypeCert,
		CurrentCertSerial: "AB:CD",
	})
	rt := newRuntimeWithStore(store)
	defer rt.Async.Drain(time.Second)

	if res := rt.Authenticate(context.Background(), "asset-aaa", "", "c", "AB:CD"); res != AuthAllow {
		t.Fatalf("expected AuthAllow on active serial, got %d", res)
	}
}

func TestAuthenticate_CertSerialCaseInsensitiveAllow(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType:          AuthTypeCert,
		CurrentCertSerial: "ab:cd",
	})
	rt := newRuntimeWithStore(store)
	defer rt.Async.Drain(time.Second)

	if res := rt.Authenticate(context.Background(), "asset-aaa", "", "c", "AB:CD"); res != AuthAllow {
		t.Fatalf("expected case-insensitive AuthAllow, got %d", res)
	}
}

func TestAuthenticate_CertSerialMismatchDeny(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType:          AuthTypeCert,
		CurrentCertSerial: "AB:CD",
	})
	rt := newRuntimeWithStore(store)
	defer rt.Async.Drain(time.Second)

	if res := rt.Authenticate(context.Background(), "asset-aaa", "", "c", "REVOKED"); res != AuthDeny {
		t.Fatalf("expected AuthDeny on serial mismatch, got %d", res)
	}
}

func TestAuthenticate_CertSerialEmptyEntryDeny(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType:          AuthTypeCert,
		CurrentCertSerial: "",
	})
	rt := newRuntimeWithStore(store)
	defer rt.Async.Drain(time.Second)

	if res := rt.Authenticate(context.Background(), "asset-aaa", "", "c", "AB:CD"); res != AuthDeny {
		t.Fatalf("expected AuthDeny when entry has empty CurrentCertSerial, got %d", res)
	}
}

func TestAuthenticate_NotFoundDeny(t *testing.T) {
	rt := newRuntimeWithStore(newStubAuthStore())
	defer rt.Async.Drain(time.Second)

	if res := rt.Authenticate(context.Background(), "asset-ghost", "x", "c", ""); res != AuthDeny {
		t.Fatalf("expected AuthDeny on not-found, got %d", res)
	}
}

func TestAuthenticate_StoreErrorReturnsAuthError(t *testing.T) {
	store := newStubAuthStore()
	store.getErr = errors.New("transient infra failure")
	rt := newRuntimeWithStore(store)
	defer rt.Async.Drain(time.Second)

	if res := rt.Authenticate(context.Background(), "asset-aaa", "x", "c", ""); res != AuthError {
		t.Fatalf("expected AuthError on store error (fail-closed), got %d", res)
	}
}

// TestAuthenticate_MalformedUsernameDeny covers the username shapes
// rejected by parseUsername. Stale firmwares carrying the legacy
// `{orgId}:{assetUUID}` form must surface-fail with a DENY rather
// than slipping through as an unverified device.
func TestAuthenticate_MalformedUsernameDeny(t *testing.T) {
	rt := newRuntimeWithStore(newStubAuthStore())
	defer rt.Async.Drain(time.Second)

	for _, u := range []string{
		"",                  // empty
		"org-1:asset-aaa",   // legacy colon-prefixed shape
		":asset-aaa",        // leading colon
		"asset-aaa:",        // trailing colon
		"asset:with:colons", // multiple colons
	} {
		if res := rt.Authenticate(context.Background(), u, "x", "c", ""); res != AuthDeny {
			t.Fatalf("expected AuthDeny on username=%q, got %d", u, res)
		}
	}
}

// TestAuthenticate_DenyDoesNotPopulateSession guards against a leak
// where a deny path leaves stale data in the session map that the
// next callback could read by chance.
func TestAuthenticate_DenyDoesNotPopulateSession(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType:     AuthTypePassword,
		PasswordHash: bcryptHash(t, "secret"),
	})
	rt := newRuntimeWithStore(store)
	defer rt.Async.Drain(time.Second)

	if res := rt.Authenticate(context.Background(), "asset-aaa", "wrong", "client-y", ""); res != AuthDeny {
		t.Fatalf("expected AuthDeny, got %d", res)
	}
	if _, ok := rt.LookupSession("client-y"); ok {
		t.Fatalf("session must not be populated on deny")
	}
}
