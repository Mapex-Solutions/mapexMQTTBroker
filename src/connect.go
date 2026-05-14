package broker

import (
	"errors"
	"time"

	"github.com/nats-io/nats.go"
)

// NATS resilience defaults applied at every Connect call. The values
// below are deliberately tuned for the broker plugin's "stay alive
// at all costs" stance — the plugin runs in-process inside Mosquitto,
// so a NATS outage MUST never propagate as a process crash.
const (
	// natsMaxReconnects = -1 means infinite. We never give up — the
	// alternative is the plugin going dark forever after a transient
	// NATS hiccup, which is worse than a few seconds of dropped events.
	natsMaxReconnects = -1

	// natsReconnectWait is the back-off between reconnect attempts.
	// 1s is short enough to recover from a NATS rolling restart in a
	// few attempts, long enough to avoid a hot loop.
	natsReconnectWait = 1 * time.Second

	// natsReconnectBufSize bounds the in-memory buffer the nats client
	// holds while disconnected. 16 MiB ≈ 16k presence advisories or
	// ~80 ingress messages at our cap (900 KiB) — large enough for
	// short outages, bounded enough to avoid OOM on the broker host.
	natsReconnectBufSize = 16 * 1024 * 1024

	// natsFlusherTimeout controls how long the client's flusher
	// goroutine holds writes before forcing a TCP write. Aggressive
	// (50ms) so presence advisories don't pile up under low-rate
	// scenarios; the trade-off is more syscalls under high rate, which
	// the kernel coalesces anyway.
	natsFlusherTimeout = 50 * time.Millisecond

	// natsPingInterval is how often the client sends PING to detect
	// half-open connections. 2 minutes balances quick failure
	// detection against PING traffic at scale.
	natsPingInterval = 2 * time.Minute
)

// natsLifecycleLogger wires nats.Conn lifecycle events into our
// plugin Logger so ops sees disconnects, reconnects, and the final
// closed state without having to scrape mosquitto-internal logs.
type natsLifecycleLogger struct {
	log Logger
}

// ConnectNATS opens a *nats.Conn with the platform's broker-plugin
// resilience profile. Returns an error only if the initial dial
// fails — after that, all reconnect logic is internal to the client.
//
// The returned *nats.Conn satisfies our NatsPublisher interface
// directly (its Publish method has the same shape), so callers can
// pass it straight into NewAsyncPublisher.
//
// log may be nil; defaults to nopLogger so the call shape is stable.
func ConnectNATS(url string, log Logger) (*nats.Conn, error) {
	if url == "" {
		return nil, errors.New("nats url is required")
	}
	if log == nil {
		log = nopLogger{}
	}
	hook := &natsLifecycleLogger{log: log}

	opts := []nats.Option{
		nats.Name("mapex-broker-plugin"),
		nats.MaxReconnects(natsMaxReconnects),
		nats.ReconnectWait(natsReconnectWait),
		nats.ReconnectBufSize(natsReconnectBufSize),
		nats.NoEcho(),
		nats.FlusherTimeout(natsFlusherTimeout),
		nats.PingInterval(natsPingInterval),

		nats.DisconnectErrHandler(hook.onDisconnect),
		nats.ReconnectHandler(hook.onReconnect),
		nats.ClosedHandler(hook.onClosed),
		nats.ErrorHandler(hook.onAsyncError),
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		log.Error("NATS initial connect failed url=%s err=%v", url, err)
		return nil, err
	}
	log.Info("NATS connected url=%s server=%s", url, nc.ConnectedUrl())
	return nc, nil
}

// onDisconnect fires when the client loses the TCP connection.
// Reconnect logic is automatic via nats.Connect options; this hook
// only records the event so ops can correlate with broker-side
// drop spikes.
func (h *natsLifecycleLogger) onDisconnect(_ *nats.Conn, err error) {
	if err != nil {
		h.log.Warn("NATS disconnected: err=%v", err)
		return
	}
	h.log.Warn("NATS disconnected (no error reported)")
}

// onReconnect fires after a successful reconnect attempt. Useful for
// dashboards counting outage durations.
func (h *natsLifecycleLogger) onReconnect(nc *nats.Conn) {
	h.log.Info("NATS reconnected url=%s", nc.ConnectedUrl())
}

// onClosed fires when the connection is permanently closed (e.g. via
// nats.Conn.Close() during plugin shutdown OR after MaxReconnects is
// exhausted, which won't happen here since we set it to -1).
func (h *natsLifecycleLogger) onClosed(_ *nats.Conn) {
	h.log.Info("NATS connection closed")
}

// onAsyncError fires for protocol-level errors that don't fit into
// publish-side return codes (e.g. permission violations on a subject
// the plugin doesn't own). Records at Warn so ops can spot
// misconfigured ACLs on the NATS side.
func (h *natsLifecycleLogger) onAsyncError(_ *nats.Conn, _ *nats.Subscription, err error) {
	h.log.Warn("NATS async error: %v", err)
}
