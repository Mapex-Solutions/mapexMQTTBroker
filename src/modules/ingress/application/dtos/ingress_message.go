package dtos

import "time"

// IngressMessage is the NATS payload for every MQTT message the device
// publishes that passes the ACL check. Consumed by js-executor (or any future
// stream-side worker) on a JetStream subject.
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
