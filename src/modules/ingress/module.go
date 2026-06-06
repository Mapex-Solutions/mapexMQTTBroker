// Package ingress is the device-data-forwarding bounded context: it validates
// and projects each MQTT publish onto a per-device NATS subject. It owns the
// payload-size cap and the subject-token safety invariants.
package ingress

import (
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/ingress/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/ingress/application/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// Service is the ingress use-case surface (Publish).
type Service = services.Service

// Publisher is the driven port the ingress service requires.
type Publisher = ports.Publisher

// New constructs the ingress service bound to the ingress subject prefix.
func New(pub Publisher, prefix string, log logging.Logger) *Service {
	return services.New(pub, prefix, log)
}
