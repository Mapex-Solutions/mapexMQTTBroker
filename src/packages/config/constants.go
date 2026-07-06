package config

import "time"

// Default values applied when the operator does not supply a `plugin_opt_*`
// directive. The subjects default to the dev-environment values so a fresh
// container boots with the dev stack working out of the box; operators in
// other environments override via the conf file.
const (
	DefaultSubjectPresence         = "dev.mapexos.presence.advisory"
	DefaultSubjectIngressPrefix    = "dev.mapexos.mqtt.data"
	DefaultWorkerPoolSize          = 4
	DefaultBufferSize              = 10_000
	DefaultAuthTimeout             = 5 * time.Second
	DefaultCacheL1Path             = "/var/cache/mqtt"
	DefaultCacheL1TTL              = 30 * time.Minute
	DefaultCacheL2Bucket           = "mapex-asset-auth"
	DefaultFanoutInvalidateSubject = "mapexos.fanout.asset.invalidate"
	DefaultSubjectOTAStatus        = "dev.mapexos.ota.status.advisory"
	DefaultSubjectDownlink         = "dev.mapexos.mqtt.downlink"
	DefaultStreamDownlink          = "DEV-MAPEXOS-MQTT-DOWNLINK"
	DefaultDownlinkDurable         = "mqtt-broker-downlink"
	DefaultDownlinkQueue           = "MQTT-BROKER-DOWNLINK-GROUP"
)
