package services

import (
	"errors"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/downlink/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// ErrMalformedEnvelope marks an undeliverable message (bad JSON / missing
// identity or command type). The consumer TERMs it — redelivery cannot fix a
// malformed payload.
var ErrMalformedEnvelope = errors.New("malformed downlink envelope")

// commandQoS is the delivery QoS for device commands: at-least-once to the
// connected subscriber (the device deduplicates by executionId).
const commandQoS = 1

// Service turns a DownlinkEnvelope into a device delivery on the broker's
// device topic contract commands/{assetUUID}/{commandType}.
type Service struct {
	deliverer ports.Deliverer
	log       logging.Logger
}
