package tiered

import "time"

// Technology-specific constants for the tiered store.
const (
	// defaultL1TTL is the safety-net expiry applied when the operator does
	// not configure one. The fanout path keeps L1 coherent at write time;
	// this TTL only catches a missed invalidation.
	defaultL1TTL = 30 * time.Minute

	// l2ObjectKeySuffix is appended to the assetUUID to form the MinIO key.
	// Flat, no tenant prefix — the AssetUUID is globally unique.
	l2ObjectKeySuffix = ".json"

	// l2MaxObjectSize caps the L2 object we will read. AssetReadModel
	// payloads are sub-kB typical; anything larger is corruption or attack
	// and is refused rather than allocated.
	l2MaxObjectSize = 1024 * 1024
)
