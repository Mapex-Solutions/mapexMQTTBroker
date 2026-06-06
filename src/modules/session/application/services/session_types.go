package services

import "sync"

// Service tracks the trusted (orgId, assetUUID) per connected client so the
// Disconnect / Message callbacks can publish without re-reading the
// AuthEntry. sync.Map gives lock-free reads on the hot path.
type Service struct {
	sessions sync.Map
}
