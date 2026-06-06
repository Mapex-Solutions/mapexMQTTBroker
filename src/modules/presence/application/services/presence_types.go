package services

import (
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/presence/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// Service publishes connect/disconnect advisories to the presence subject.
type Service struct {
	pub     ports.Publisher
	subject string
	log     logging.Logger
}
