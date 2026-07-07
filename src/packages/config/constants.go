package config

// Default values applied when the matching environment variable is unset.
// Dev-friendly values let a fresh container boot against the local stack; the
// sensitive ones (credentials and the inline-credential NATS URL) are guarded
// by InitConfig so a non-dev GO_ENV that leaves them at the default refuses to
// start. The int defaults feed "int"-typed ConfigDefinition entries; Load
// converts the time-based ones into durations. Grouped by context to mirror
// the ConfigDefinition list in config.go.

// Auth callout — the assets MS internal endpoint and its shared secret.
const (
	DefaultAssetsHost         = "assets"
	DefaultAssetsPort         = "5002"
	DefaultAuthAPIKey         = "5230c2e2-e245-468d-89e8-94154cf520d0"
	DefaultAuthTimeoutSeconds = 5
)

// NATS connection — the URL carries inline user:password credentials.
const (
	DefaultNatsURL = "nats://service:service_secret@localhost:4222"
)

// Async publisher — worker pool draining the bounded NATS publish buffer.
const (
	DefaultWorkerPoolSize = 4
	DefaultBufferSize     = 10_000
)

// TieredCache — L1 Pebble path/TTL and the platform-fixed L2 bucket.
const (
	DefaultCacheL1Path       = "/var/cache/mqtt"
	DefaultCacheL1TTLMinutes = 30
	DefaultCacheL2Bucket     = "mapex-asset-auth"
)

// Object store (L2) credentials — scoped svc-broker user.
const (
	DefaultObjectStoreAccessKey = "svc-broker"
	DefaultObjectStoreSecretKey = "svc-broker-secret-change-me"
)

// NATS subjects, streams, and downlink consumer names.
const (
	DefaultSubjectPresence         = "dev.mapexos.presence.advisory"
	DefaultSubjectIngressPrefix    = "dev.mapexos.mqtt.data"
	DefaultFanoutInvalidateSubject = "mapexos.fanout.asset.invalidate"
	DefaultSubjectOTAStatus        = "dev.mapexos.ota.status.advisory"
	DefaultSubjectDownlink         = "dev.mapexos.mqtt.downlink"
	DefaultStreamDownlink          = "DEV-MAPEXOS-MQTT-DOWNLINK"
	DefaultDownlinkDurable         = "mqtt-broker-downlink"
	DefaultDownlinkQueue           = "MQTT-BROKER-DOWNLINK-GROUP"
)
