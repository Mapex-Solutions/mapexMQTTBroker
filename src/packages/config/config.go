package config

import (
	"fmt"
	"time"

	gokitconfig "github.com/Mapex-Solutions/mapexGoKit/config"
)

// brokerDefinitions declares every runtime setting the plugin reads, its
// environment variable, type, and dev default. Sensitive keys — the auth API
// key, the object-store credentials, and the NATS URL (which carries inline
// user:password) — are guarded by InitConfig: a non-dev GO_ENV that leaves any
// of them at the dev default aborts startup. The broker shares this definition
// list shape with every mapexOS Go service so services and edge brokers run one
// config flow.
var brokerDefinitions = []gokitconfig.ConfigDefinition{
	{Key: "go_env", Env: "GO_ENV", Type: "string", Default: "dev"},

	{Key: "nats_url", Env: "NATS_URL", Type: "string", Default: DefaultNatsURL, Sensitive: true},
	{Key: "auth_api_key", Env: "INTERNAL_API_KEY", Type: "string", Default: DefaultAuthAPIKey, Sensitive: true},

	{Key: "assets_host", Env: "ASSETS_HOST", Type: "string", Default: DefaultAssetsHost},
	{Key: "assets_port", Env: "ASSETS_PORT", Type: "string", Default: DefaultAssetsPort},
	{Key: "auth_timeout_seconds", Env: "AUTH_TIMEOUT_SECONDS", Type: "int", Default: DefaultAuthTimeoutSeconds},

	{Key: "nats_subject_presence", Env: "NATS_SUBJECT_PRESENCE", Type: "string", Default: DefaultSubjectPresence},
	{Key: "nats_subject_ingress_prefix", Env: "NATS_SUBJECT_INGRESS_PREFIX", Type: "string", Default: DefaultSubjectIngressPrefix},

	{Key: "worker_pool_size", Env: "PLUGIN_WORKER_POOL_SIZE", Type: "int", Default: DefaultWorkerPoolSize},
	{Key: "buffer_size", Env: "PLUGIN_BUFFER_SIZE", Type: "int", Default: DefaultBufferSize},

	{Key: "cache_l1_path", Env: "CACHE_L1_PATH", Type: "string", Default: DefaultCacheL1Path},
	{Key: "cache_l1_ttl_minutes", Env: "CACHE_L1_TTL_MINUTES", Type: "int", Default: DefaultCacheL1TTLMinutes},
	{Key: "fanout_invalidate_subject", Env: "FANOUT_INVALIDATE_SUBJECT", Type: "string", Default: DefaultFanoutInvalidateSubject},

	{Key: "nats_subject_ota_status", Env: "NATS_SUBJECT_OTA_STATUS", Type: "string", Default: DefaultSubjectOTAStatus},
	{Key: "nats_subject_downlink", Env: "NATS_SUBJECT_DOWNLINK", Type: "string", Default: DefaultSubjectDownlink},
	{Key: "nats_stream_downlink", Env: "NATS_STREAM_DOWNLINK", Type: "string", Default: DefaultStreamDownlink},
	{Key: "nats_downlink_durable", Env: "NATS_DOWNLINK_DURABLE", Type: "string", Default: DefaultDownlinkDurable},
	{Key: "nats_downlink_queue", Env: "NATS_DOWNLINK_QUEUE", Type: "string", Default: DefaultDownlinkQueue},

	{Key: "object_store_endpoint", Env: "OBJECT_STORE_ENDPOINT", Type: "string", Default: ""},
	{Key: "object_store_access_key", Env: "OBJECT_STORE_ACCESS_KEY", Type: "string", Default: DefaultObjectStoreAccessKey, Sensitive: true},
	{Key: "object_store_secret_key", Env: "OBJECT_STORE_SECRET_KEY", Type: "string", Default: DefaultObjectStoreSecretKey, Sensitive: true},
	{Key: "object_store_use_ssl", Env: "OBJECT_STORE_USE_SSL", Type: "bool", Default: false},
	{Key: "object_store_auth_is_needed", Env: "OBJECT_STORE_AUTH_IS_NEEDED", Type: "bool", Default: true},
}

// Load reads the plugin configuration from the environment through the shared
// mapexGoKit config flow and returns an assembled Config. InitConfig applies
// the dev defaults, runs the sensitive-default production guard (aborting the
// process when a non-dev GO_ENV still uses a dev credential), and exposes the
// typed getters used below. The AuthURL is derived from the assets host and
// port so operators configure the target host, not a full URL.
//
// The error return is reserved for future validation; today Load never fails
// because InitConfig either succeeds or aborts on a guard violation.
func Load() (Config, error) {
	gokitconfig.InitConfig(brokerDefinitions)

	assetsHost, _ := gokitconfig.GetStringValue("assets_host")
	assetsPort, _ := gokitconfig.GetStringValue("assets_port")
	authTimeoutSecs, _ := gokitconfig.GetIntValue("auth_timeout_seconds")
	cacheL1TTLMinutes, _ := gokitconfig.GetIntValue("cache_l1_ttl_minutes")

	natsURL, _ := gokitconfig.GetStringValue("nats_url")
	authKey, _ := gokitconfig.GetStringValue("auth_api_key")
	subjectPresence, _ := gokitconfig.GetStringValue("nats_subject_presence")
	subjectIngressPrefix, _ := gokitconfig.GetStringValue("nats_subject_ingress_prefix")
	workerPoolSize, _ := gokitconfig.GetIntValue("worker_pool_size")
	bufferSize, _ := gokitconfig.GetIntValue("buffer_size")
	cacheL1Path, _ := gokitconfig.GetStringValue("cache_l1_path")
	fanoutInvalidateSubject, _ := gokitconfig.GetStringValue("fanout_invalidate_subject")
	subjectOTAStatus, _ := gokitconfig.GetStringValue("nats_subject_ota_status")
	subjectDownlink, _ := gokitconfig.GetStringValue("nats_subject_downlink")
	streamDownlink, _ := gokitconfig.GetStringValue("nats_stream_downlink")
	downlinkDurable, _ := gokitconfig.GetStringValue("nats_downlink_durable")
	downlinkQueue, _ := gokitconfig.GetStringValue("nats_downlink_queue")

	l2Endpoint, _ := gokitconfig.GetStringValue("object_store_endpoint")
	l2AccessKey, _ := gokitconfig.GetStringValue("object_store_access_key")
	l2SecretKey, _ := gokitconfig.GetStringValue("object_store_secret_key")
	l2UseSSL, _ := gokitconfig.GetBoolValue("object_store_use_ssl")
	l2AuthIsNeeded, _ := gokitconfig.GetBoolValue("object_store_auth_is_needed")

	cfg := Config{
		NatsURL:              natsURL,
		SubjectPresence:      subjectPresence,
		SubjectIngressPrefix: subjectIngressPrefix,
		AuthURL:              fmt.Sprintf("http://%s:%s/internal/asset_auth", assetsHost, assetsPort),
		AuthAPIKey:           authKey,
		AuthTimeout:          time.Duration(authTimeoutSecs) * time.Second,
		WorkerPoolSize:       workerPoolSize,
		BufferSize:           bufferSize,
		CacheL1Path:          cacheL1Path,
		CacheL1TTL:           time.Duration(cacheL1TTLMinutes) * time.Minute,
		CacheL2Endpoint:      l2Endpoint,
		CacheL2AccessKey:     l2AccessKey,
		CacheL2SecretKey:     l2SecretKey,
		// Bucket is fixed by the platform contract with the assets service.
		CacheL2Bucket:           DefaultCacheL2Bucket,
		CacheL2UseSSL:           l2UseSSL,
		CacheL2AuthIsNeeded:     l2AuthIsNeeded,
		FanoutInvalidateSubject: fanoutInvalidateSubject,
		SubjectOTAStatus:        subjectOTAStatus,
		SubjectDownlink:         subjectDownlink,
		StreamDownlink:          streamDownlink,
		DownlinkDurable:         downlinkDurable,
		DownlinkQueue:           downlinkQueue,
	}
	return cfg, nil
}
