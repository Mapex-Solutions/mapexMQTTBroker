package broker

import "strings"

// ACL access codes from the Mosquitto v5 plugin API
// (mosquitto_plugin.h).
//
//   AccessRead       — broker about to send a message TO the client
//   AccessWrite      — client publishing
//   AccessSubscribe  — client requesting a subscription
//   AccessUnsubscribe (rare) — client unsubscribing
const (
	AccessRead        = 1
	AccessWrite       = 2
	AccessSubscribe   = 4
	AccessUnsubscribe = 8
)

// Topic prefix conventions enforced by the platform.
const (
	TopicPrefixEvents   = "events"   // device → broker (publish)
	TopicPrefixCommands = "commands" // broker → device (read + subscribe)
)

// CheckACL returns true when the device identified by `username` is
// allowed to perform `acc` on `topic`. Pure function — no I/O, no
// state — so it is trivially testable, table-drivable, and runs in
// sub-microseconds inside the broker's plugin event loop.
//
// Username encoding (platform contract): bare `assetUUID`. The
// assetUUID is globally unique (Mongo `idx_asset_uuid_unique`) so the
// broker no longer needs to carry orgId on the wire — tenant scoping
// flows from the auth projection's orgId server-side.
//
// Topic structure (platform contract):
//
//	events/{assetUUID}/{eventType}      acc=2 (publish from device)
//	commands/{assetUUID}/{commandType}  acc=1|4 (read/subscribe to device)
//
// Any topic that does not match one of the two prefixes, or whose
// assetUUID token does not match the username, is denied.
// Service-account routes (datasource bridges, ops) are NOT carried
// here — those are gated by separate broker users with their own
// dedicated ACL handling at the infra layer.
func CheckACL(username, topic string, acc int) bool {
	assetUUID, ok := parseUsername(username)
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
	case TopicPrefixEvents:
		return acc == AccessWrite
	case TopicPrefixCommands:
		return acc == AccessRead || acc == AccessSubscribe
	default:
		return false
	}
}

// parseUsername validates the platform's device-username format. The
// platform contract is strict: the username is the bare assetUUID —
// non-empty, no colon characters (legacy `{orgId}:{assetUUID}` shape
// is rejected so the rollout fails loudly when stale firmwares show
// up). Returns ("",false) on malformed input.
func parseUsername(username string) (assetUUID string, ok bool) {
	if username == "" {
		return "", false
	}
	if strings.ContainsRune(username, ':') {
		return "", false
	}
	return username, true
}

// parseTopic extracts the first two tokens of a topic. Returns false
// on a topic with fewer than two tokens, or if any of the prefix /
// assetUUID tokens is empty. The third token onwards (event type,
// command type) is irrelevant to ACL — the wildcards consumed by
// device subscriptions live there and the broker handles fan-out, not
// the plugin.
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
