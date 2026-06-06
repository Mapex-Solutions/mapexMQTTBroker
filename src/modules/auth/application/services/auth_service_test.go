package services

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/constants"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/entities"
)

// stubAuthStore is the in-memory AuthStore used by the auth path tests.
type stubAuthStore struct {
	entries map[string]*entities.AuthEntry
	getErr  error
}

func newStubAuthStore() *stubAuthStore {
	return &stubAuthStore{entries: map[string]*entities.AuthEntry{}}
}

func (s *stubAuthStore) put(assetUUID string, e *entities.AuthEntry) { s.entries[assetUUID] = e }

func (s *stubAuthStore) Get(_ context.Context, assetUUID string) (*entities.AuthEntry, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	e, ok := s.entries[assetUUID]
	if !ok {
		return nil, ports.ErrAuthEntryNotFound
	}
	return e, nil
}

func (s *stubAuthStore) Invalidate(_ context.Context, _ string) error { return nil }
func (s *stubAuthStore) Close() error                                 { return nil }

// bcryptVerifier is a real bcrypt compare, exercising the password path on
// the broker thread without standing up the HTTP client.
type bcryptVerifier struct{}

func (bcryptVerifier) CompareLocal(hash, plaintext string) bool {
	if hash == "" || plaintext == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) == nil
}

func bcryptHash(t *testing.T, plain string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(h)
}

func newService(store ports.AuthStore) *Service {
	return New(store, bcryptVerifier{}, nil)
}

func TestAuthenticate_PasswordAllow(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &entities.AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType: constants.AuthTypePassword, PasswordHash: bcryptHash(t, "secret"),
	})
	dec := newService(store).Authenticate(context.Background(), "asset-aaa", "secret", "")
	if dec.Result != ports.AuthAllow {
		t.Fatalf("expected AuthAllow, got %d", dec.Result)
	}
	// The Decision must carry the trusted identity so the entry can record
	// the session and emit presence without re-reading the projection.
	if dec.OrgID != "org-1" || dec.AssetUUID != "asset-aaa" {
		t.Fatalf("expected identity org-1/asset-aaa, got %+v", dec)
	}
}

func TestAuthenticate_PasswordMismatchDeny(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &entities.AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType: constants.AuthTypePassword, PasswordHash: bcryptHash(t, "secret"),
	})
	dec := newService(store).Authenticate(context.Background(), "asset-aaa", "wrong", "")
	if dec.Result != ports.AuthDeny {
		t.Fatalf("expected AuthDeny on password mismatch, got %d", dec.Result)
	}
	// Deny must not leak identity.
	if dec.OrgID != "" || dec.AssetUUID != "" {
		t.Fatalf("deny must not carry identity, got %+v", dec)
	}
}

func TestAuthenticate_DisabledDeny(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &entities.AuthEntry{
		Enabled: false, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType: constants.AuthTypePassword, PasswordHash: bcryptHash(t, "secret"),
	})
	if dec := newService(store).Authenticate(context.Background(), "asset-aaa", "secret", ""); dec.Result != ports.AuthDeny {
		t.Fatalf("expected AuthDeny on disabled asset, got %d", dec.Result)
	}
}

func TestAuthenticate_CertSerialAllow(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &entities.AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType: constants.AuthTypeCert, CurrentCertSerial: "AB:CD",
	})
	dec := newService(store).Authenticate(context.Background(), "asset-aaa", "", "AB:CD")
	if dec.Result != ports.AuthAllow || dec.OrgID != "org-1" {
		t.Fatalf("expected AuthAllow with identity on active serial, got %+v", dec)
	}
}

func TestAuthenticate_CertSerialCaseInsensitiveAllow(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &entities.AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType: constants.AuthTypeCert, CurrentCertSerial: "ab:cd",
	})
	if dec := newService(store).Authenticate(context.Background(), "asset-aaa", "", "AB:CD"); dec.Result != ports.AuthAllow {
		t.Fatalf("expected case-insensitive AuthAllow, got %d", dec.Result)
	}
}

func TestAuthenticate_CertSerialMismatchDeny(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &entities.AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType: constants.AuthTypeCert, CurrentCertSerial: "AB:CD",
	})
	if dec := newService(store).Authenticate(context.Background(), "asset-aaa", "", "REVOKED"); dec.Result != ports.AuthDeny {
		t.Fatalf("expected AuthDeny on serial mismatch, got %d", dec.Result)
	}
}

func TestAuthenticate_CertSerialEmptyEntryDeny(t *testing.T) {
	store := newStubAuthStore()
	store.put("asset-aaa", &entities.AuthEntry{
		Enabled: true, OrgId: "org-1", AssetUUID: "asset-aaa",
		AuthType: constants.AuthTypeCert, CurrentCertSerial: "",
	})
	if dec := newService(store).Authenticate(context.Background(), "asset-aaa", "", "AB:CD"); dec.Result != ports.AuthDeny {
		t.Fatalf("expected AuthDeny when entry has empty CurrentCertSerial, got %d", dec.Result)
	}
}

func TestAuthenticate_NotFoundDeny(t *testing.T) {
	if dec := newService(newStubAuthStore()).Authenticate(context.Background(), "asset-ghost", "x", ""); dec.Result != ports.AuthDeny {
		t.Fatalf("expected AuthDeny on not-found, got %d", dec.Result)
	}
}

func TestAuthenticate_StoreErrorReturnsAuthError(t *testing.T) {
	store := newStubAuthStore()
	store.getErr = errors.New("transient infra failure")
	if dec := newService(store).Authenticate(context.Background(), "asset-aaa", "x", ""); dec.Result != ports.AuthError {
		t.Fatalf("expected AuthError on store error (fail-closed), got %d", dec.Result)
	}
}

// TestAuthenticate_MalformedUsernameDeny covers the username shapes rejected
// by ParseUsername. Stale firmwares carrying the legacy `{orgId}:{assetUUID}`
// form must surface-fail with a DENY rather than slipping through.
func TestAuthenticate_MalformedUsernameDeny(t *testing.T) {
	svc := newService(newStubAuthStore())
	for _, u := range []string{"", "org-1:asset-aaa", ":asset-aaa", "asset-aaa:", "asset:with:colons"} {
		if dec := svc.Authenticate(context.Background(), u, "x", ""); dec.Result != ports.AuthDeny {
			t.Fatalf("expected AuthDeny on username=%q, got %d", u, dec.Result)
		}
	}
}
