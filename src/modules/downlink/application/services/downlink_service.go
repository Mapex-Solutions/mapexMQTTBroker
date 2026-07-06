package services

import (
	"encoding/json"

	downlink "github.com/Mapex-Solutions/mapexGoKit/contracts/downlink"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/downlink/application/dtos"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/downlink/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/textutil"
)

// New constructs the downlink service over the delivery port.
func New(deliverer ports.Deliverer, log logging.Logger) *Service {
	if log == nil {
		log = logging.NopLogger{}
	}
	return &Service{deliverer: deliverer, log: log}
}

// HandleMessage processes one downlink envelope: unmarshal, validate the
// identity + command type, and deliver the inner command payload to the
// device's topic (commands/{assetUUID}/{commandType} — the ACL lets exactly
// that device subscribe). Returns ErrMalformedEnvelope for undeliverable
// messages (the consumer TERMs); a delivery failure is logged and swallowed —
// the broker is a dumb transport, the Asset MS reconciler re-dispatches.
func (s *Service) HandleMessage(data []byte) error {
	var env dtos.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		s.log.Warn("[SERVICE:Downlink] malformed envelope: err=%v", err)
		return ErrMalformedEnvelope
	}
	if env.AssetUUID == "" || env.CommandType == "" {
		s.log.Warn("[SERVICE:Downlink] malformed envelope: asset=%q command=%q",
			textutil.Truncate(env.AssetUUID, 64), textutil.Truncate(env.CommandType, 64))
		return ErrMalformedEnvelope
	}

	topic := downlink.DeviceCommandTopic(env.AssetUUID, env.CommandType)
	if err := s.deliverer.Deliver(topic, env.Payload, commandQoS); err != nil {
		s.log.Error("[SERVICE:Downlink] delivery failed: org=%s asset=%s command=%s err=%v",
			textutil.Truncate(env.OrgID, 64), textutil.Truncate(env.AssetUUID, 64), env.CommandType, err)
		return nil
	}
	s.log.Debug("[SERVICE:Downlink] delivered: org=%s asset=%s command=%s topic=%s",
		textutil.Truncate(env.OrgID, 64), textutil.Truncate(env.AssetUUID, 64), env.CommandType, topic)
	return nil
}
