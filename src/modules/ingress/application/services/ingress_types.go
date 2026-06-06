package services

import (
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/ingress/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// Service forwards device messages to per-device ingress subjects built from
// the configured prefix and the trusted (orgId, assetUUID).
type Service struct {
	pub    ports.Publisher
	prefix string
	log    logging.Logger
}
