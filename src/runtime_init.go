package broker

import (
	"errors"
	"time"

	"github.com/nats-io/nats.go"
)

// RuntimeOptions aggregates everything BuildRuntime needs to wire a
// fully-running PluginRuntime. Splitting the publisher creation from
// the runtime construction lets unit tests inject a fake without
// having to stand up a real nats.Conn.
type RuntimeOptions struct {
	Config    Config
	Publisher NatsPublisher
	NatsConn  *nats.Conn // optional — required for FANOUT consumer; nil disables it
	Log       Logger
}

// BuildRuntime constructs a PluginRuntime, starts its async publisher,
// builds the TieredAuthStore (L1 Pebble + L2 MinIO + L3 HTTP), starts
// the FANOUT consumer (if NatsConn is set), and returns the runtime
// ready for callbacks.
//
// Returns an error when:
//   - Publisher is nil
//   - Config is missing required values (worker pool / buffer)
//   - L3 HTTP client cannot be constructed (URL or APIKey missing)
//   - TieredAuthStore fails to open Pebble at L1Path
func BuildRuntime(opts RuntimeOptions) (*PluginRuntime, error) {
	if opts.Publisher == nil {
		return nil, errors.New("publisher is required")
	}
	if opts.Log == nil {
		opts.Log = nopLogger{}
	}

	async, err := NewAsyncPublisher(opts.Publisher, opts.Config.BufferSize, opts.Config.WorkerPoolSize, opts.Log)
	if err != nil {
		return nil, err
	}
	async.Start()

	authClient, err := NewAuthClient(AuthClientConfig{
		URL:     opts.Config.AuthURL,
		APIKey:  opts.Config.AuthAPIKey,
		Timeout: opts.Config.AuthTimeout,
	}, opts.Log)
	if err != nil {
		_ = async.Drain(ShutdownTimeout)
		return nil, err
	}

	store, err := NewTieredAuthStore(TieredAuthStoreConfig{
		L1Path:      opts.Config.CacheL1Path,
		L2Endpoint:  opts.Config.CacheL2Endpoint,
		L2AccessKey: opts.Config.CacheL2AccessKey,
		L2SecretKey: opts.Config.CacheL2SecretKey,
		L2Bucket:    opts.Config.CacheL2Bucket,
		L2UseSSL:    opts.Config.CacheL2UseSSL,
		L3Client:    authClient,
		L1TTL:       opts.Config.CacheL1TTL,
		Log:         opts.Log,
	})
	if err != nil {
		_ = async.Drain(ShutdownTimeout)
		return nil, err
	}

	rt := &PluginRuntime{
		Config: opts.Config,
		Async:  async,
		Auth:   authClient,
		Store:  store,
		Log:    opts.Log,
	}

	// FANOUT consumer is best-effort: a nats.Conn failure here doesn't
	// fail the whole plugin init. Without invalidations the L1 TTL
	// safety net keeps things eventually correct, just slower than
	// designed.
	if opts.NatsConn != nil {
		fc, fcErr := NewFanoutConsumer(FanoutConsumerConfig{
			Subject: opts.Config.FanoutInvalidateSubject,
			Conn:    opts.NatsConn,
			Store:   store,
			Log:     opts.Log,
		})
		if fcErr != nil {
			opts.Log.Warn("BuildRuntime: fanout consumer init failed (continuing without): %v", fcErr)
		} else if startErr := fc.Start(); startErr != nil {
			opts.Log.Warn("BuildRuntime: fanout consumer start failed (continuing without): %v", startErr)
		} else {
			rt.Fanout = fc
		}
	}

	opts.Log.Info("PluginRuntime initialized: presence=%s ingress_prefix=%s auth_url=%s l1=%s l2=%s",
		opts.Config.SubjectPresence, opts.Config.SubjectIngressPrefix, opts.Config.AuthURL,
		opts.Config.CacheL1Path, opts.Config.CacheL2Endpoint)
	return rt, nil
}

// ShutdownTimeout is the deadline mosquitto_plugin_cleanup uses to
// drain the async pool. Past this point the broker is exiting anyway
// — the plugin gives up gracefully and lets the OS reclaim resources.
const ShutdownTimeout = 5 * time.Second
