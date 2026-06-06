package services

import "github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/session/domain/entities"

// New constructs an empty session service.
func New() *Service {
	return &Service{}
}

// Remember stores the trusted (orgId, assetUUID) for the supplied clientID.
// Idempotent — a re-CONNECT under the same clientID overwrites the prior
// entry.
func (s *Service) Remember(clientID, orgID, assetUUID string) {
	if clientID == "" {
		return
	}
	s.sessions.Store(clientID, entities.SessionInfo{OrgID: orgID, AssetUUID: assetUUID})
}

// Lookup returns the SessionInfo captured at CONNECT for the supplied
// clientID. Returns zero-value + false when no auth has taken place — caller
// MUST treat that as a non-actionable event (service-account user, race
// during disconnect, malformed callback).
func (s *Service) Lookup(clientID string) (entities.SessionInfo, bool) {
	if clientID == "" {
		return entities.SessionInfo{}, false
	}
	v, ok := s.sessions.Load(clientID)
	if !ok {
		return entities.SessionInfo{}, false
	}
	info, ok := v.(entities.SessionInfo)
	if !ok {
		return entities.SessionInfo{}, false
	}
	return info, true
}

// Forget removes the per-connection entry. Called on disconnect so stale
// clientIDs do not accumulate over a long process lifetime.
func (s *Service) Forget(clientID string) {
	if clientID == "" {
		return
	}
	s.sessions.Delete(clientID)
}
