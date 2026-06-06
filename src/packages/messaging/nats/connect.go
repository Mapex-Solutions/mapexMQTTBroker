package natsbus

import (
	"errors"

	"github.com/nats-io/nats.go"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// ConnectNATS opens a *nats.Conn with the platform's broker-plugin
// resilience profile. Returns an error only if the initial dial fails —
// after that, all reconnect logic is internal to the client.
//
// The returned *nats.Conn satisfies the NatsPublisher port directly (its
// Publish method has the same shape), so callers can pass it straight into
// NewAsyncPublisher.
//
// log may be nil; defaults to a no-op Logger so the call shape is stable.
func ConnectNATS(url string, log logging.Logger) (*nats.Conn, error) {
	if url == "" {
		return nil, errors.New("nats url is required")
	}
	if log == nil {
		log = logging.NopLogger{}
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
		log.Error("[INFRA:NATS] initial connect failed url=%s err=%v", url, err)
		return nil, err
	}
	log.Info("[INFRA:NATS] connected url=%s server=%s", url, nc.ConnectedUrl())
	return nc, nil
}

// onDisconnect fires when the client loses the TCP connection. Reconnect
// logic is automatic via nats.Connect options; this hook only records the
// event so ops can correlate with broker-side drop spikes.
func (h *natsLifecycleLogger) onDisconnect(_ *nats.Conn, err error) {
	if err != nil {
		h.log.Warn("[INFRA:NATS] disconnected: err=%v", err)
		return
	}
	h.log.Warn("[INFRA:NATS] disconnected (no error reported)")
}

// onReconnect fires after a successful reconnect attempt. Useful for
// dashboards counting outage durations.
func (h *natsLifecycleLogger) onReconnect(nc *nats.Conn) {
	h.log.Info("[INFRA:NATS] reconnected url=%s", nc.ConnectedUrl())
}

// onClosed fires when the connection is permanently closed (e.g. via
// nats.Conn.Close() during plugin shutdown OR after MaxReconnects is
// exhausted, which won't happen here since we set it to -1).
func (h *natsLifecycleLogger) onClosed(_ *nats.Conn) {
	h.log.Info("[INFRA:NATS] connection closed")
}

// onAsyncError fires for protocol-level errors that don't fit into
// publish-side return codes (e.g. permission violations on a subject the
// plugin doesn't own). Records at Warn so ops can spot misconfigured ACLs
// on the NATS side.
func (h *natsLifecycleLogger) onAsyncError(_ *nats.Conn, _ *nats.Subscription, err error) {
	h.log.Warn("[INFRA:NATS] async error: %v", err)
}
