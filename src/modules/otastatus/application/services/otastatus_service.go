package services

import (
	"encoding/json"
	"time"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/otastatus/application/dtos"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/otastatus/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/textutil"
)

// New constructs the otastatus service bound to the advisory subject.
func New(pub ports.Publisher, subject string, log logging.Logger) *Service {
	if log == nil {
		log = logging.NopLogger{}
	}
	return &Service{pub: pub, subject: subject, log: log}
}

// HandleStatusReport parses one device OTA status publish and emits the
// Advisory. org/assetUUID come from the AUTHENTICATED session (the caller),
// never from the payload. Malformed or incomplete reports are logged and
// dropped — liveness-over-correctness on the broker thread.
func (s *Service) HandleStatusReport(orgID, assetUUID string, payload []byte, ts time.Time) bool {
	var report dtos.StatusReport
	if err := json.Unmarshal(payload, &report); err != nil {
		s.log.Warn("[SERVICE:OTAStatus] dropped: malformed report org=%s asset=%s err=%v",
			textutil.Truncate(orgID, 64), textutil.Truncate(assetUUID, 64), err)
		s.pub.RecordDrop()
		return false
	}
	if report.ExecutionID == "" || report.Status == "" {
		s.log.Warn("[SERVICE:OTAStatus] dropped: incomplete report org=%s asset=%s executionId=%q status=%q",
			textutil.Truncate(orgID, 64), textutil.Truncate(assetUUID, 64),
			textutil.Truncate(report.ExecutionID, 64), textutil.Truncate(report.Status, 32))
		s.pub.RecordDrop()
		return false
	}

	adv := dtos.OTAAdvisory{
		OrgID:          orgID,
		AssetUUID:      assetUUID,
		PlanID:         report.PlanID,
		OTAExecutionID: report.ExecutionID,
		Status:         report.Status,
		Progress:       report.Progress,
		Error:          report.Error,
		Message:        report.Message,
		Timestamp:      ts,
	}
	return s.publish(adv)
}

// publish marshals the advisory and hands it to the async publisher.
func (s *Service) publish(adv dtos.OTAAdvisory) bool {
	data, err := json.Marshal(adv)
	if err != nil {
		s.log.Error("[SERVICE:OTAStatus] marshal failure: executionId=%s err=%v", adv.OTAExecutionID, err)
		s.pub.RecordDrop()
		return false
	}
	return s.pub.Enqueue(s.subject, data)
}
