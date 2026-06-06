package services

import (
	"strings"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/constants"
)

// CheckACL returns true when the device identified by `username` is allowed to
// perform `acc` on `topic`. Pure function — no I/O, no state — so it runs in
// sub-microseconds inside the broker's plugin event loop.
//
// Username encoding (platform contract): bare `assetUUID`, globally unique
// (Mongo `idx_asset_uuid_unique`), so tenant scoping flows from the auth
// projection's orgId server-side.
//
// Topic structure (platform contract):
//
//	events/{assetUUID}/{eventType}      acc=2 (publish from device)
//	commands/{assetUUID}/{commandType}  acc=1|4 (read/subscribe to device)
//
// Any topic that does not match one of the two prefixes, or whose assetUUID
// token does not match the username, is denied.
func CheckACL(username, topic string, acc int) bool {
	assetUUID, ok := ParseUsername(username)
	if !ok {
		return false
	}

	prefix, tAsset, ok := parseTopic(topic)
	if !ok {
		return false
	}

	if tAsset != assetUUID {
		return false
	}

	switch prefix {
	case constants.TopicPrefixEvents:
		return acc == constants.AccessWrite
	case constants.TopicPrefixCommands:
		return acc == constants.AccessRead || acc == constants.AccessSubscribe
	default:
		return false
	}
}

// ParseUsername validates the platform's device-username format. The contract
// is strict: the username is the bare assetUUID — non-empty, no colon (legacy
// `{orgId}:{assetUUID}` is rejected so the rollout fails loudly when stale
// firmwares show up). Returns ("",false) on malformed input.
func ParseUsername(username string) (assetUUID string, ok bool) {
	if username == "" {
		return "", false
	}
	if strings.ContainsRune(username, ':') {
		return "", false
	}
	return username, true
}

// parseTopic extracts the first two tokens of a topic. Returns false on a
// topic with fewer than two tokens, or if any of the prefix / assetUUID tokens
// is empty. The third token onwards is irrelevant to ACL.
func parseTopic(topic string) (prefix, assetUUID string, ok bool) {
	if topic == "" {
		return "", "", false
	}
	parts := strings.SplitN(topic, "/", 3)
	if len(parts) < 2 {
		return "", "", false
	}
	prefix, assetUUID = parts[0], parts[1]
	if prefix == "" || assetUUID == "" {
		return "", "", false
	}
	return prefix, assetUUID, true
}
