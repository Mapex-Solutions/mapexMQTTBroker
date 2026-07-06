// Package bootstrap is the composition root: it builds the shared kernel and
// every bounded-context module, wires them with their port dependencies, and
// returns an App the cgo entry holds at process scope. It is the only place
// allowed to depend on every module — the modules never depend on each other.
package bootstrap

import (
	"errors"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/downlink"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/ingress"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/otastatus"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/presence"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/session"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/config"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
	natsbus "github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/messaging/nats"
)

// ShutdownTimeout is the deadline plugin cleanup uses to drain the async pool.
// Past this point the broker is exiting anyway — the plugin gives up
// gracefully and lets the OS reclaim resources.
const ShutdownTimeout = 5 * time.Second

// Options aggregates everything Build needs to wire the App. Splitting the
// publisher from the connection lets tests inject a fake without standing up a
// real nats.Conn.
type Options struct {
	Config    config.Config
	Publisher natsbus.NatsPublisher
	NatsConn  *nats.Conn // optional — required for the fanout + downlink consumers; nil disables them
	Log       logging.Logger

	// Deliverer publishes a payload to a broker-local MQTT topic (the cgo
	// entry implements it with mosquitto_broker_publish). nil disables the
	// downlink consumer — tests and non-plugin builds run without it.
	Deliverer downlink.Deliverer
}

// App is the process-scope aggregate the cgo entry holds: one service per
// bounded context plus the lifecycle handles the composition root owns and
// tears down at cleanup.
type App struct {
	Auth      *auth.Service
	Presence  *presence.Service
	Ingress   *ingress.Service
	Session   *session.Service
	OTAStatus *otastatus.Service

	Log         logging.Logger
	AuthTimeout time.Duration

	async       *natsbus.AsyncPublisher
	authMod     *auth.Module
	downlinkMod *downlink.Module
}

// Build constructs the App: it starts the async publisher, builds the auth
// module (cache + L3 client + fanout), and wires the presence / ingress /
// session services. Returns an error when the publisher is nil or the auth
// module fails to construct (missing L3 client, Pebble open failure).
func Build(opts Options) (*App, error) {
	if opts.Publisher == nil {
		return nil, errors.New("publisher is required")
	}
	if opts.Log == nil {
		opts.Log = logging.NopLogger{}
	}

	async, err := natsbus.NewAsyncPublisher(opts.Publisher, opts.Config.BufferSize, opts.Config.WorkerPoolSize, opts.Log)
	if err != nil {
		return nil, err
	}
	async.Start()

	authMod, err := auth.Build(auth.Config{
		AuthURL:          opts.Config.AuthURL,
		AuthAPIKey:       opts.Config.AuthAPIKey,
		AuthTimeout:      opts.Config.AuthTimeout,
		CacheL1Path:      opts.Config.CacheL1Path,
		CacheL1TTL:       opts.Config.CacheL1TTL,
		CacheL2Endpoint:     opts.Config.CacheL2Endpoint,
		CacheL2AccessKey:    opts.Config.CacheL2AccessKey,
		CacheL2SecretKey:    opts.Config.CacheL2SecretKey,
		CacheL2AuthIsNeeded: opts.Config.CacheL2AuthIsNeeded,
		CacheL2Bucket:    opts.Config.CacheL2Bucket,
		CacheL2UseSSL:    opts.Config.CacheL2UseSSL,
		FanoutSubject:    opts.Config.FanoutInvalidateSubject,
	}, opts.NatsConn, opts.Log)
	if err != nil {
		_ = async.Drain(ShutdownTimeout)
		return nil, err
	}

	app := &App{
		Auth:        authMod.Service,
		Presence:    presence.New(async, opts.Config.SubjectPresence, opts.Log),
		Ingress:     ingress.New(async, opts.Config.SubjectIngressPrefix, opts.Log),
		Session:     session.New(),
		OTAStatus:   otastatus.New(async, opts.Config.SubjectOTAStatus, opts.Log),
		Log:         opts.Log,
		AuthTimeout: opts.Config.AuthTimeout,
		async:       async,
		authMod:     authMod,
	}

	// Downlink consumer — platform->device commands. Requires both the NATS
	// connection (JetStream durable) and the cgo Deliverer; absent either,
	// the plugin runs uplink-only (tests, partial deployments).
	if opts.NatsConn != nil && opts.Deliverer != nil {
		downlinkMod, err := downlink.Build(downlink.Config{
			Conn:      opts.NatsConn,
			Deliverer: opts.Deliverer,
			Subject:   opts.Config.SubjectDownlink,
			Stream:    opts.Config.StreamDownlink,
			Durable:   opts.Config.DownlinkDurable,
			Queue:     opts.Config.DownlinkQueue,
			Log:       opts.Log,
		})
		if err != nil {
			authMod.Stop()
			_ = async.Drain(ShutdownTimeout)
			return nil, err
		}
		app.downlinkMod = downlinkMod
	}

	opts.Log.Info("[MODULE:Broker] initialized: presence=%s ingress_prefix=%s auth_url=%s l1=%s l2=%s",
		opts.Config.SubjectPresence, opts.Config.SubjectIngressPrefix, opts.Config.AuthURL,
		opts.Config.CacheL1Path, opts.Config.CacheL2Endpoint)
	return app, nil
}

// Shutdown tears down the owned lifecycle resources: the auth module (fanout +
// cache store) and the async publisher. Idempotent at the module level.
func (a *App) Shutdown() {
	a.downlinkMod.Shutdown()
	a.authMod.Stop()
	_ = a.async.Drain(ShutdownTimeout)
}
