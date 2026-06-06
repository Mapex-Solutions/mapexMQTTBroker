package constants

// Event values used in a presence advisory's Event field. Single source of
// truth so the broker and its consumers reference the same tokens.
const (
	EventConnect    = "connect"
	EventDisconnect = "disconnect"
)
