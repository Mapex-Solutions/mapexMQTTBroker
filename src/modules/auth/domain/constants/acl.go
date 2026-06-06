package constants

// ACL access codes from the Mosquitto v5 plugin API (mosquitto_plugin.h).
//
//	AccessRead        — broker about to send a message TO the client
//	AccessWrite       — client publishing
//	AccessSubscribe   — client requesting a subscription
//	AccessUnsubscribe — (rare) client unsubscribing
const (
	AccessRead        = 1
	AccessWrite       = 2
	AccessSubscribe   = 4
	AccessUnsubscribe = 8
)
