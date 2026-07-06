package services

import (
	"strings"

	ota "github.com/Mapex-Solutions/mapexGoKit/contracts/ota"
)

// IsStatusTopic reports whether an MQTT publish topic is a device OTA status
// report per the broker's device topic contract: events/{assetUUID}/ota_status
// (exactly three tokens; the ACL already pinned the assetUUID to the session).
func IsStatusTopic(topic string) bool {
	parts := strings.Split(topic, "/")
	return len(parts) == 3 && parts[0] == "events" && parts[1] != "" && parts[2] == ota.EventTypeStatus
}
