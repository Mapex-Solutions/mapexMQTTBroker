package config

// Default values applied when the matching environment variable is unset.
// Dev-friendly values let a fresh container boot against the local stack; the
// sensitive ones (credentials and the NATS URL, which carries inline
// credentials) are guarded by InitConfig so a non-dev GO_ENV that leaves them
// at the dev default refuses to start. The int defaults feed "int"-typed
// ConfigDefinition entries; Load converts the time-based ones into durations.
const (
	DefaultNatsURL    = "nats://service:service_secret@localhost:4222"
	DefaultAssetsHost = "assets"
	DefaultAssetsPort = "5002"
	DefaultAuthAPIKey = "5230c2e2-e245-468d-89e8-94154cf520d0"

	DefaultAuthTimeoutSeconds = 5
	DefaultWorkerPoolSize     = 4
	DefaultBufferSize         = 10_000
	DefaultCacheL1Path        = "/var/cache/mqtt"
	DefaultCacheL1TTLMinutes  = 30
	DefaultCacheL2Bucket      = "mapex-asset-auth"

	DefaultObjectStoreAccessKey = "svc-broker"
	DefaultObjectStoreSecretKey = "svc-broker-secret-change-me"

	DefaultSubjectPresence         = "dev.mapexos.presence.advisory"
	DefaultSubjectIngressPrefix    = "dev.mapexos.mqtt.data"
	DefaultFanoutInvalidateSubject = "mapexos.fanout.asset.invalidate"
	DefaultSubjectOTAStatus        = "dev.mapexos.ota.status.advisory"
	DefaultSubjectDownlink         = "dev.mapexos.mqtt.downlink"
	DefaultStreamDownlink          = "DEV-MAPEXOS-MQTT-DOWNLINK"
	DefaultDownlinkDurable         = "mqtt-broker-downlink"
	DefaultDownlinkQueue           = "MQTT-BROKER-DOWNLINK-GROUP"
)
