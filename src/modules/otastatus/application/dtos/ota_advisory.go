package dtos

import ota "github.com/Mapex-Solutions/mapexGoKit/contracts/ota"

// OTAAdvisory is the cross-repo wire contract for a device's normalized OTA
// status transition (single source of truth in mapexGoKit/contracts/ota).
type OTAAdvisory = ota.Advisory

// StatusReport is the raw device payload published on
// events/{assetUUID}/ota_status; identity is never taken from it.
type StatusReport = ota.StatusReport
