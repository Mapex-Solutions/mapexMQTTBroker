// Package presence is the online/offline-edge bounded context: it publishes
// connect and disconnect advisories to the shared presence subject. Mosquitto
// 2.0.x has no MOSQ_EVT_CONNECT, so a successful auth is the canonical connect
// signal — the entry orchestrates that, this module just emits the advisory.
package presence

import (
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/presence/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/presence/application/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// Service is the presence use-case surface (PublishConnect / PublishDisconnect).
type Service = services.Service

// Publisher is the driven port the presence service requires.
type Publisher = ports.Publisher

// New constructs the presence service bound to the presence subject.
func New(pub Publisher, subject string, log logging.Logger) *Service {
	return services.New(pub, subject, log)
}
