package httpauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/dtos"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/entities"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/textutil"
)

var (
	_ ports.PasswordVerifier = (*AuthClient)(nil)
	_ ports.AuthLookup       = (*AuthClient)(nil)
)

// NewAuthClient validates the supplied config and constructs a reusable
// AuthClient. Returns an error when URL or APIKey is empty — failing init
// is preferable to silently degrading auth security.
func NewAuthClient(cfg AuthClientConfig, log logging.Logger) (*AuthClient, error) {
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
		log = logging.NopLogger{}
	}

	transport := &http.Transport{
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConns,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		ForceAttemptHTTP2:   false, // assets service is HTTP/1.1; avoid h2 dial probing
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
	}
	return &AuthClient{cfg: cfg, http: client, log: log}, nil
}

// CompareLocal runs bcrypt against an in-memory hash without making an HTTP
// call. Used by the broker plugin's CONNECT path: the entry (with the
// password hash) was already loaded from L1/L2/L3, so the remaining check
// is a pure CPU compare on the broker thread.
//
// Returns true on match, false on mismatch OR on a malformed hash. The
// bcrypt cost embedded in the hash drives the latency (~50ms at cost 10),
// which dominates the entire auth path — cache layer choice doesn't matter
// once we're here.
func (a *AuthClient) CompareLocal(hash, plaintext string) bool {
	if hash == "" || plaintext == "" {
		return false
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)); err != nil {
		return false
	}
	return true
}

// LookupEntry fetches the slim AuthProjection for the given asset UUID via
// a read-only GET on the assets service internal endpoint
// (/internal/asset-auth/:assetUUID). Used by the tiered store as the L3
// fallback when L1 (Pebble) and L2 (MinIO) both miss.
//
// The response body is the assets service AuthProjection wrapped in the
// platform's standard response envelope (`{status, errors, data}`).
// Returns nil + ErrAuthEntryNotFound when the asset does not exist; non-nil
// error on transport / 5xx.
func (a *AuthClient) LookupEntry(ctx context.Context, assetUUID string) (*entities.AuthEntry, error) {
	if assetUUID == "" {
		return nil, ports.ErrAuthEntryNotFound
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
		a.log.Warn("[INFRA:AuthClient] lookup failed assetUUID=%s err=%v", textutil.Truncate(assetUUID, 64), err)
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		a.lookupHits.Add(1)
		return dtos.DecodeReadModelEnvelopeToAuthEntry(resp.Body)
	case http.StatusNotFound:
		a.lookupMiss.Add(1)
		return nil, ports.ErrAuthEntryNotFound
	default:
		a.lookupError.Add(1)
		a.log.Warn("[INFRA:AuthClient] lookup unexpected status assetUUID=%s status=%d", textutil.Truncate(assetUUID, 64), resp.StatusCode)
		return nil, fmt.Errorf("lookup status %d", resp.StatusCode)
	}
}

// LookupCount returns the total lookups attempted since plugin init.
func (a *AuthClient) LookupCount() uint64 { return a.lookups.Load() }

// LookupHitCount returns successful lookups (HTTP 200).
func (a *AuthClient) LookupHitCount() uint64 { return a.lookupHits.Load() }

// LookupMissCount returns 404 responses (asset unknown).
func (a *AuthClient) LookupMissCount() uint64 { return a.lookupMiss.Load() }

// LookupErrorCount returns infra errors (timeout, 5xx, network). A growing
// error rate indicates either assets service instability or a misconfigured
// callout URL / API key.
func (a *AuthClient) LookupErrorCount() uint64 { return a.lookupError.Load() }
