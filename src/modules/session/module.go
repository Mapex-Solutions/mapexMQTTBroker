// Package session is the connection-identity bounded context: it tracks the
// trusted (orgId, assetUUID) per Mosquitto clientID across the auth, presence
// and ingress callbacks. It owns no I/O — purely in-memory state.
package session

import (
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/session/application/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/session/domain/entities"
)

// Service is the session use-case surface (Remember / Lookup / Forget).
type Service = services.Service

// Info is the trusted per-connection identity returned by Lookup.
type Info = entities.SessionInfo

// New constructs the session service.
func New() *Service {
	return services.New()
}
