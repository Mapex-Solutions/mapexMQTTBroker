package dtos

// FanoutInvalidatePayload is the body the assets service publishes on
// `mapexos.fanout.asset.invalidate`. The plugin only needs the AssetUUID to
// drop its L1 entry; other fields are ignored at this layer.
type FanoutInvalidatePayload struct {
	AssetUUID string `json:"assetUUID"`
}
