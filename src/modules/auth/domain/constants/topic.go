package constants

// Topic prefix conventions enforced by the platform: devices publish under
// events/ and read/subscribe under commands/.
const (
	TopicPrefixEvents   = "events"   // device -> broker (publish)
	TopicPrefixCommands = "commands" // broker -> device (read + subscribe)
)
