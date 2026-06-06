package dtos

import "time"

// PresenceAdvisory is the NATS payload published on every device CONNECT and
// DISCONNECT. Mirrors the healthmonitor.PresenceAdvisory contract on the
// consumer side; field names + JSON tags MUST stay in sync. The plugin lives
// in goKit (no access to MapexOS contracts to avoid circular imports), so
// this duplication is intentional and tracked.
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
