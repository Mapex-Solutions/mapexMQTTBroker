package services

import (
	"encoding/json"
	"time"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/presence/application/dtos"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/presence/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/presence/domain/constants"
	domainsvc "github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/presence/domain/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/textutil"
)

// New constructs the presence service bound to the presence subject.
func New(pub ports.Publisher, subject string, log logging.Logger) *Service {
	if log == nil {
		log = logging.NopLogger{}
	}
	return &Service{pub: pub, subject: subject, log: log}
}

// PublishConnect emits a CONNECT advisory to the presence subject. Returns
// true on accept, false on overflow drop.
func (s *Service) PublishConnect(orgID, assetUUID, clientID, sourceIP string, ts time.Time) bool {
	s.log.Debug("[SERVICE:Presence] connect: org=%s asset=%s client=%s ip=%s",
		textutil.Truncate(orgID, 64), textutil.Truncate(assetUUID, 64), textutil.Truncate(clientID, 64), sourceIP)
	adv := dtos.PresenceAdvisory{
		Event:     constants.EventConnect,
		OrgID:     orgID,
		AssetUUID: assetUUID,
		ClientID:  clientID,
		SourceIP:  sourceIP,
		Timestamp: ts,
	}
	return s.publish(adv)
}

// PublishDisconnect emits a DISCONNECT advisory with the broker's reason code
// translated to a stable text bucket. ts is the broker's wall-clock time at
// the disconnect callback.
func (s *Service) PublishDisconnect(orgID, assetUUID, clientID, sourceIP string, reasonCode int, ts time.Time) bool {
	reason := domainsvc.MapDisconnectReason(reasonCode)
	s.log.Debug("[SERVICE:Presence] disconnect: org=%s asset=%s client=%s reason=%s code=%d",
		textutil.Truncate(orgID, 64), textutil.Truncate(assetUUID, 64), textutil.Truncate(clientID, 64), reason, reasonCode)
	adv := dtos.PresenceAdvisory{
		Event:      constants.EventDisconnect,
		OrgID:      orgID,
		AssetUUID:  assetUUID,
		ClientID:   clientID,
		SourceIP:   sourceIP,
		Timestamp:  ts,
		ReasonCode: reasonCode,
		ReasonText: reason,
	}
	return s.publish(adv)
}

// publish marshals the advisory and hands it to the async publisher.
// Marshalling failures are translated to drops; the broader strategy is
// liveness-over-correctness on the broker thread.
func (s *Service) publish(adv dtos.PresenceAdvisory) bool {
	data, err := json.Marshal(adv)
	if err != nil {
		s.log.Error("[SERVICE:Presence] marshal failure: event=%s err=%v", adv.Event, err)
		s.pub.RecordDrop()
		return false
	}
	return s.pub.Enqueue(s.subject, data)
}
