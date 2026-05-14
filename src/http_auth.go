package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCompare wraps bcrypt.CompareHashAndPassword so callers can
// invoke it without importing golang.org/x/crypto/bcrypt directly.
// Returns nil on match, a typed bcrypt error otherwise.
func bcryptCompare(hash, plaintext string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
}

// AuthResult is the binary outcome the plugin's CONNECT callback
// returns to Mosquitto via cgo. The CONNECT decision happens LOCALLY
// in PluginRuntime.Authenticate using the AuthEntry returned by the
// TieredAuthStore — there is no HTTP auth callout at the broker
// thread. This enum is the wire between the Go side and the cgo
// trampoline that calls back into Mosquitto.
type AuthResult int

const (
	// AuthAllow — entry found and credentials match (bcrypt for
	// password mode, serial-equality for cert mode).
	AuthAllow AuthResult = iota
	// AuthDeny — entry not found, asset disabled, cross-tenant
	// attempt, or credential mismatch. All deny reasons collapse to
	// this so the wire never enumerates valid usernames.
	AuthDeny
	// AuthError — TieredAuthStore reported every layer unreachable
	// (L1 + L2 + L3 all failed) or the runtime is misconfigured.
	// Caller MUST fail closed: a transient outage admitting an
	// unverified device is worse than refusing the CONNECT.
	AuthError
)

// AuthClientConfig holds the runtime parameters the broker plugin
// reads from `plugin_opt_*` directives in mosquitto.conf and uses to
// fetch AuthEntry records from the assets MS as the L3 fallback of
// the TieredAuthStore.
type AuthClientConfig struct {
	// URL is the base of the assets MS auth-projection fallback.
	// Example: "http://assets:5002/internal/asset-auth". The plugin
	// appends "/:assetUUID" per request.
	URL string

	// APIKey is the shared secret the assets MS internal-auth
	// middleware expects on the X-API-Key header. The plugin never
	// falls back to a "no auth" path — empty APIKey is rejected at
	// load time.
	APIKey string

	// Timeout caps the round-trip per lookup. 5s is generous; a real
	// cold path completes well under 100ms.
	Timeout time.Duration

	// MaxIdleConns + IdleConnTimeout shape the connection pool so a
	// reconnect storm doesn't open a fresh TCP per cache miss. Defaults
	// are applied when zero.
	MaxIdleConns    int
	IdleConnTimeout time.Duration
}

// HTTPDoer is the minimal HTTP client surface AuthClient depends on.
// Pulled into an interface so unit tests can inject a fake without
// standing up an HTTP server.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// AuthClient is the L3 read-only fallback for the TieredAuthStore. It
// fetches an asset's AuthEntry projection from the assets MS via the
// internal read-model endpoint when L1 (Pebble) and L2 (MinIO) both
// miss. The plugin never POSTs credentials — auth decisions (bcrypt
// for password mode, cert serial match for cert mode) happen LOCALLY
// against the entry returned here.
type AuthClient struct {
	cfg  AuthClientConfig
	http HTTPDoer
	log  Logger

	// Counters surfaced via the *Count() methods. atomic.Uint64 so
	// reads from the broker thread don't tear when a worker
	// updates concurrently.
	lookups     atomic.Uint64
	lookupHits  atomic.Uint64
	lookupMiss  atomic.Uint64
	lookupError atomic.Uint64
}

// NewAuthClient validates the supplied config and constructs a
// reusable AuthClient. Returns an error when URL or APIKey is empty
// — failing init is preferable to silently degrading auth security.
func NewAuthClient(cfg AuthClientConfig, log Logger) (*AuthClient, error) {
	if cfg.URL == "" {
		return nil, errors.New("auth url is required")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("auth api key is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MaxIdleConns <= 0 {
		cfg.MaxIdleConns = 32
	}
	if cfg.IdleConnTimeout <= 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}
	if log == nil {
		log = nopLogger{}
	}

	transport := &http.Transport{
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConns,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		ForceAttemptHTTP2:   false, // assets MS is HTTP/1.1; avoid h2 dial probing
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
	}
	return &AuthClient{cfg: cfg, http: client, log: log}, nil
}

// CompareLocal runs bcrypt against an in-memory hash without making
// an HTTP call. Used by the broker plugin's CONNECT path: the entry
// (with the password hash) was already loaded from L1/L2/L3, so the
// remaining check is a pure CPU compare on the broker thread.
//
// Returns true on match, false on mismatch OR on a malformed hash.
// The bcrypt cost embedded in the hash drives the latency (~50ms at
// cost 10), which is the dominant cost in the entire auth path —
// cache layer choice doesn't matter once we're here.
func (a *AuthClient) CompareLocal(hash, plaintext string) bool {
	if hash == "" || plaintext == "" {
		return false
	}
	if err := bcryptCompare(hash, plaintext); err != nil {
		return false
	}
	return true
}

// LookupEntry fetches the slim AuthProjection for the given asset
// UUID via a read-only GET on the assets MS internal endpoint
// (/internal/asset-auth/:assetUUID). Used by the TieredAuthStore as
// the L3 fallback when L1 (Pebble) and L2 (MinIO) both miss.
//
// The response body is the assets MS AuthProjection wrapped in the
// platform's standard response envelope (`{status, errors, data}`).
// Returns nil + ErrAuthEntryNotFound when the asset does not exist;
// non-nil error on transport / 5xx.
func (a *AuthClient) LookupEntry(ctx context.Context, assetUUID string) (*AuthEntry, error) {
	if assetUUID == "" {
		return nil, ErrAuthEntryNotFound
	}
	a.lookups.Add(1)

	url := a.cfg.URL + "/" + assetUUID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		a.lookupError.Add(1)
		return nil, fmt.Errorf("build lookup request: %w", err)
	}
	req.Header.Set("X-API-Key", a.cfg.APIKey)
	req.Header.Set("User-Agent", "mapex-broker-plugin/1.0")

	resp, err := a.http.Do(req)
	if err != nil {
		a.lookupError.Add(1)
		a.log.Warn("auth lookup failed assetUUID=%s err=%v", truncate(assetUUID, 64), err)
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		a.lookupHits.Add(1)
		return decodeReadModelToAuthEntry(resp.Body)
	case http.StatusNotFound:
		a.lookupMiss.Add(1)
		return nil, ErrAuthEntryNotFound
	default:
		a.lookupError.Add(1)
		a.log.Warn("auth lookup unexpected status assetUUID=%s status=%d", truncate(assetUUID, 64), resp.StatusCode)
		return nil, fmt.Errorf("lookup status %d", resp.StatusCode)
	}
}

// authProjectionEnvelope is the platform's standard HTTP response
// envelope (response.Success wraps payloads in `{status, errors,
// data}`). The L3 HTTP path returns this; the L2 MinIO object is the
// raw AuthProjection without an envelope (the assets MS writes the
// projection directly via WriteAssetAuth).
type authProjectionEnvelope struct {
	Data AuthProjection `json:"data"`
}

// AuthProjection mirrors the slim contract written by the assets MS
// to `mapex-asset-auth` and returned by `/internal/asset-auth/:uuid`.
// Forward-compatible because json.Unmarshal ignores unknown keys —
// new fields can land on the producer side without a plugin redeploy.
type AuthProjection struct {
	AssetUUID         string `json:"assetUUID"`
	OrgId             string `json:"orgId"`
	Enabled           bool   `json:"enabled"`
	Type              string `json:"type"`
	AuthType          string `json:"authType"`
	PasswordHash      string `json:"passwordHash"`
	CurrentCertSerial string `json:"currentCertSerial"`
}

// ToAuthEntry projects the slim AuthProjection into the AuthEntry the
// broker plugin caches and decides CONNECTs against. Both shapes are
// auth-specific — the field copy is one-to-one with a rename of the
// active cert serial token.
func (p *AuthProjection) ToAuthEntry() *AuthEntry {
	return &AuthEntry{
		Enabled:           p.Enabled,
		AssetUUID:         p.AssetUUID,
		OrgId:             p.OrgId,
		AuthType:          p.AuthType,
		PasswordHash:      p.PasswordHash,
		CurrentCertSerial: p.CurrentCertSerial,
	}
}

// DecodeReadModelEnvelopeToAuthEntry parses the standard HTTP response
// envelope (used by the L3 fallback) and projects to AuthEntry.
func DecodeReadModelEnvelopeToAuthEntry(r io.Reader) (*AuthEntry, error) {
	var env authProjectionEnvelope
	if err := json.NewDecoder(r).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode auth projection envelope: %w", err)
	}
	return env.Data.ToAuthEntry(), nil
}

// DecodeReadModelToAuthEntry parses the raw AuthProjection JSON (no
// envelope) and projects to AuthEntry. Used by the L2 MinIO path
// where the assets MS writes the projection directly.
func DecodeReadModelToAuthEntry(r io.Reader) (*AuthEntry, error) {
	var proj AuthProjection
	if err := json.NewDecoder(r).Decode(&proj); err != nil {
		return nil, fmt.Errorf("decode auth projection: %w", err)
	}
	return proj.ToAuthEntry(), nil
}

// decodeReadModelToAuthEntry is the package-private alias retained
// for the existing L3 call site below.
func decodeReadModelToAuthEntry(r io.Reader) (*AuthEntry, error) {
	return DecodeReadModelEnvelopeToAuthEntry(r)
}

// LookupCount returns the total lookups attempted since plugin init.
func (a *AuthClient) LookupCount() uint64 { return a.lookups.Load() }

// LookupHitCount returns successful lookups (HTTP 200).
func (a *AuthClient) LookupHitCount() uint64 { return a.lookupHits.Load() }

// LookupMissCount returns 404 responses (asset unknown).
func (a *AuthClient) LookupMissCount() uint64 { return a.lookupMiss.Load() }

// LookupErrorCount returns infra errors (timeout, 5xx, network).
// A growing error rate indicates either assets MS instability or a
// misconfigured callout URL / API key.
func (a *AuthClient) LookupErrorCount() uint64 { return a.lookupError.Load() }

// authCachePolicy is reserved for a future in-plugin LRU cache that
// would short-circuit repeat CONNECTs from the same client_id within
// a short window. Not implemented in MVP — assets MS Ristretto L0
// absorbs the warm-path cost. Keeping the type so adding it later is
// a non-breaking change to the AuthClient API.
type authCachePolicy struct {
	mu sync.Mutex
}
