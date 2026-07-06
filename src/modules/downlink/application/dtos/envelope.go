package dtos

import downlink "github.com/Mapex-Solutions/mapexGoKit/contracts/downlink"

// Envelope is the cross-repo platform->device command wire contract (single
// source of truth in mapexGoKit/contracts/downlink).
type Envelope = downlink.Envelope
