package services

import (
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/otastatus/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// Service forwards a device's OTA status report into the platform advisory:
// it parses the raw events/{assetUUID}/ota_status payload, enriches it with
// the session's trusted identity, and enqueues the Advisory on the STATIC
// advisory subject.
type Service struct {
	pub     ports.Publisher
	subject string
	log     logging.Logger
}
