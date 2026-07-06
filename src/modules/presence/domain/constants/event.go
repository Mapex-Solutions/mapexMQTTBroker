package constants

import presence "github.com/Mapex-Solutions/mapexGoKit/contracts/presence"

// Event and protocol values for a presence advisory, aliased from the shared
// edge-presence contract so the broker and every consumer reference the exact
// same tokens.
const (
	EventConnect    = presence.EventConnect
	EventDisconnect = presence.EventDisconnect

	ProtocolMQTT = presence.ProtocolMQTT
)
