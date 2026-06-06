package services

import (
	"encoding/json"
	"time"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/ingress/application/dtos"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/ingress/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/ingress/domain/constants"
	domainsvc "github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/ingress/domain/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/textutil"
)

// New constructs the ingress service bound to the ingress subject prefix.
func New(pub ports.Publisher, prefix string, log logging.Logger) *Service {
	if log == nil {
		log = logging.NopLogger{}
	}
	return &Service{pub: pub, prefix: prefix, log: log}
}

// Publish emits an MQTT message envelope to a per-device ingress subject
// ("{prefix}.{orgId}.{assetUUID}") so the js-executor's wildcard consumer
// routes each device's messages without a fan-in step. Drops the message when
// the subject tokens are NATS-illegal or the payload exceeds the size cap.
func (s *Service) Publish(orgID, assetUUID, clientID, topic string, payload []byte, qos int, retain bool, ts time.Time) bool {
	if !s.subjectSafe(orgID, assetUUID, topic) {
		return false
	}
	if !s.payloadSafe(orgID, assetUUID, topic, payload) {
		return false
	}
	msg := dtos.IngressMessage{
		OrgID:     orgID,
		AssetUUID: assetUUID,
		ClientID:  clientID,
		Topic:     topic,
		Payload:   payload,
		QoS:       qos,
		Retain:    retain,
		Timestamp: ts,
	}
	subject := s.prefix + "." + orgID + "." + assetUUID
	return s.publish(subject, msg)
}

// subjectSafe rejects an ingress whose orgId / assetUUID would corrupt the
// composed NATS subject. Drops with a warn log and records the drop so the
// offender is visible without enabling debug.
func (s *Service) subjectSafe(orgID, assetUUID, topic string) bool {
	if domainsvc.ValidSubjectToken(orgID) && domainsvc.ValidSubjectToken(assetUUID) {
		return true
	}
	s.log.Warn("[SERVICE:Ingress] dropped: invalid subject token org=%q asset=%q topic=%s",
		textutil.Truncate(orgID, 64), textutil.Truncate(assetUUID, 64), textutil.Truncate(topic, 128))
	s.pub.RecordDrop()
	return false
}

// payloadSafe rejects an ingress payload above the size cap. The underlying
// NATS server would reject it anyway; this surfaces it earlier with the
// offending asset + topic + size.
func (s *Service) payloadSafe(orgID, assetUUID, topic string, payload []byte) bool {
	if len(payload) <= constants.MaxIngressPayloadBytes {
		return true
	}
	s.log.Warn("[SERVICE:Ingress] dropped: payload too large org=%s asset=%s topic=%s size=%d max=%d",
		textutil.Truncate(orgID, 64), textutil.Truncate(assetUUID, 64), textutil.Truncate(topic, 128),
		len(payload), constants.MaxIngressPayloadBytes)
	s.pub.RecordDrop()
	return false
}

// publish marshals the envelope and hands it to the async publisher.
// Marshalling failures are translated to drops; the broader strategy is
// liveness-over-correctness on the broker thread.
func (s *Service) publish(subject string, msg dtos.IngressMessage) bool {
	data, err := json.Marshal(msg)
	if err != nil {
		s.log.Error("[SERVICE:Ingress] marshal failure: subject=%s err=%v", subject, err)
		s.pub.RecordDrop()
		return false
	}
	return s.pub.Enqueue(subject, data)
}
