// Package auth is the access-control bounded context: it decides every CONNECT
// (authentication) and authorizes every publish/subscribe (ACL), backed by a
// tiered auth cache (L1 Pebble + L2 MinIO + L3 HTTP) that a fanout consumer
// keeps coherent. Build encapsulates the module's own infrastructure so the
// composition root only supplies config + the shared NATS connection.
package auth

import (
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/infrastructure/cache/tiered"
	httpauth "github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/infrastructure/lookup/http"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/interfaces/message/consumers/fanout"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// Service is the auth use-case surface (Authenticate / Authorize).
type Service = services.Service

// Decision is the auth verdict returned by Authenticate.
type Decision = ports.Decision

// AuthResult enumerates the verdict carried by a Decision.
type AuthResult = ports.AuthResult

const (
	AuthAllow = ports.AuthAllow
	AuthDeny  = ports.AuthDeny
	AuthError = ports.AuthError
)

// Config is the operator-supplied wiring for the auth module.
type Config struct {
	AuthURL     string
	AuthAPIKey  string
	AuthTimeout time.Duration

	CacheL1Path      string
	CacheL1TTL       time.Duration
	CacheL2Endpoint  string
	CacheL2AccessKey string
	CacheL2SecretKey string
	CacheL2Bucket    string
	CacheL2UseSSL    bool

	FanoutSubject string
}

// Module bundles the auth service with the lifecycle handles the composition
// root tears down at cleanup (the cache store and the fanout consumer).
type Module struct {
	Service *Service

	fanout *fanout.Consumer
	store  ports.AuthStore
}

// Build constructs the auth module: the L3 HTTP client, the tiered cache, the
// service, and (when nc is set) the fanout invalidation consumer. The fanout
// consumer is best-effort — its failure doesn't fail the module, because the
// L1 TTL safety net keeps things eventually correct.
func Build(cfg Config, nc *nats.Conn, log logging.Logger) (*Module, error) {
	if log == nil {
		log = logging.NopLogger{}
	}

	client, err := httpauth.NewAuthClient(httpauth.AuthClientConfig{
		URL:     cfg.AuthURL,
		APIKey:  cfg.AuthAPIKey,
		Timeout: cfg.AuthTimeout,
	}, log)
	if err != nil {
		return nil, err
	}

	store, err := tiered.NewTieredAuthStore(tiered.TieredAuthStoreConfig{
		L1Path:      cfg.CacheL1Path,
		L2Endpoint:  cfg.CacheL2Endpoint,
		L2AccessKey: cfg.CacheL2AccessKey,
		L2SecretKey: cfg.CacheL2SecretKey,
		L2Bucket:    cfg.CacheL2Bucket,
		L2UseSSL:    cfg.CacheL2UseSSL,
		L3Client:    client,
		L1TTL:       cfg.CacheL1TTL,
		Log:         log,
	})
	if err != nil {
		return nil, err
	}

	m := &Module{
		Service: services.New(store, client, log),
		store:   store,
	}

	if nc != nil {
		fc, fcErr := fanout.NewConsumer(fanout.ConsumerConfig{
			Subject: cfg.FanoutSubject,
			Conn:    nc,
			Store:   store,
			Log:     log,
		})
		if fcErr != nil {
			log.Warn("[MODULE:Auth] fanout consumer init failed (continuing without): %v", fcErr)
		} else if startErr := fc.Start(); startErr != nil {
			log.Warn("[MODULE:Auth] fanout consumer start failed (continuing without): %v", startErr)
		} else {
			m.fanout = fc
		}
	}

	return m, nil
}

// Stop releases the module's lifecycle resources. Idempotent.
func (m *Module) Stop() {
	if m.fanout != nil {
		_ = m.fanout.Stop()
	}
	if m.store != nil {
		_ = m.store.Close()
	}
}
