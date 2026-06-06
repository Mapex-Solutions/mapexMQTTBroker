package entities

// SessionInfo is the per-connection state captured at CONNECT time. Keyed by
// Mosquitto clientID so the later Disconnect / Message callbacks can compose
// NATS subjects with the trusted orgId from the auth projection — the bare
// assetUUID on the wire no longer carries it.
type SessionInfo struct {
	OrgID     string
	AssetUUID string
}
