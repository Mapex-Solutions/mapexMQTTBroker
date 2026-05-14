package broker

import "time"

// PresenceAdvisory is the NATS payload published by the plugin on
// every device CONNECT and DISCONNECT. Mirrors the
// healthmonitor.PresenceAdvisory contract on the consumer side; field
// names + JSON tags MUST stay in sync. The plugin lives in goKit (no
// access to MapexOS contracts to avoid circular imports), so this
// duplication is intentional and tracked.
type PresenceAdvisory struct {
	Event      string    `json:"event"`
	OrgID      string    `json:"orgId"`
	AssetUUID  string    `json:"assetUUID"`
	ClientID   string    `json:"clientId,omitempty"`
	SourceIP   string    `json:"sourceIp,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	ReasonCode int       `json:"reasonCode,omitempty"`
	ReasonText string    `json:"reasonText,omitempty"`
}

// IngressMessage is the NATS payload for every MQTT message the
// device publishes that passes the ACL check. Consumed by JS-Executor
// (or any future stream-side worker) on a JetStream subject.
type IngressMessage struct {
	OrgID     string    `json:"orgId"`
	AssetUUID string    `json:"assetUUID"`
	ClientID  string    `json:"clientId,omitempty"`
	Topic     string    `json:"topic"`
	Payload   []byte    `json:"payload"`
	QoS       int       `json:"qos"`
	Retain    bool      `json:"retain"`
	Timestamp time.Time `json:"timestamp"`
}

// Event values used in PresenceAdvisory.Event. Single source of truth
// so callers and consumers reference the same constants.
const (
	EventConnect    = "connect"
	EventDisconnect = "disconnect"
)

// MapDisconnectReason translates a Mosquitto disconnect reason code
// (MOSQ_DISCONNECT_*) into a human-readable text label. Unmapped
// codes return "unknown_<code>" so dashboards have a stable bucket
// for new broker firmware values without code changes.
func MapDisconnectReason(code int) string {
	switch code {
	case 0:
		return "clean_disconnect"
	case 4:
		return "keepalive_timeout"
	case 142:
		return "session_taken_over"
	case 152:
		return "admin_action"
	default:
		return formatUnknown(code)
	}
}
