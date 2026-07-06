package dtos

import presence "github.com/Mapex-Solutions/mapexGoKit/contracts/presence"

// PresenceAdvisory is the NATS payload published on every device CONNECT and
// DISCONNECT. It aliases the shared edge-presence contract in mapexGoKit so the
// broker, the LNS, and the assets healthmonitor all marshal the exact same
// shape on the same subject — no duplication, no drift.
type PresenceAdvisory = presence.Advisory
