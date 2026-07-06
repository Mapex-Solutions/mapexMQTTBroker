// Package otastatus is the OTA status-forwarding bounded context: publishes
// a device's events/{assetUUID}/ota_status report as the normalized OTA
// advisory on the STATIC platform subject. The entry orchestrates the topic
// routing; this module just parses, enriches (session identity), and emits.
package otastatus

import (
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/otastatus/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/otastatus/application/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// Service is the otastatus use-case surface (HandleStatusReport).
type Service = services.Service

// Publisher is the driven port the otastatus service requires.
type Publisher = ports.Publisher

// New constructs the otastatus service bound to the advisory subject.
func New(pub Publisher, subject string, log logging.Logger) *Service {
	return services.New(pub, subject, log)
}
