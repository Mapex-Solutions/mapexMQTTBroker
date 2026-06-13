package httpauth

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// AuthClientConfig holds the runtime parameters the broker plugin reads
// from `plugin_opt_*` directives and uses to fetch AuthEntry records from
// the assets service as the L3 fallback of the tiered auth store.
type AuthClientConfig struct {
	// URL is the base of the assets service auth-projection fallback.
	// Example: "http://assets:5002/internal/asset_auth". The plugin appends
	// "/:assetUUID" per request.
	URL string

	// APIKey is the shared secret the assets service internal-auth
	// middleware expects on the X-API-Key header. The plugin never falls
	// back to a "no auth" path — empty APIKey is rejected at load time.
	APIKey string

	// Timeout caps the round-trip per lookup. 5s is generous; a real cold
	// path completes well under 100ms.
	Timeout time.Duration

	// MaxIdleConns + IdleConnTimeout shape the connection pool so a
	// reconnect storm doesn't open a fresh TCP per cache miss. Defaults are
	// applied when zero.
	MaxIdleConns    int
	IdleConnTimeout time.Duration
}

// HTTPDoer is the minimal HTTP client surface AuthClient depends on. Pulled
// into an interface so unit tests can inject a fake without standing up an
// HTTP server.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// AuthClient is the L3 read-only fallback for the tiered auth store. It
// fetches an asset's AuthEntry projection from the assets service via the
// internal read-model endpoint when L1 (Pebble) and L2 (MinIO) both miss.
// The plugin never POSTs credentials — auth decisions (bcrypt for password
// mode, cert serial match for cert mode) happen LOCALLY against the entry
// returned here.
type AuthClient struct {
	cfg  AuthClientConfig
	http HTTPDoer
	log  logging.Logger

	// Counters surfaced via the *Count() methods. atomic.Uint64 so reads
	// from the broker thread don't tear when a worker updates concurrently.
	lookups     atomic.Uint64
	lookupHits  atomic.Uint64
	lookupMiss  atomic.Uint64
	lookupError atomic.Uint64
}
