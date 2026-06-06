package ports

// Publisher is the driven port the presence service needs: a non-blocking
// enqueue plus a drop accounter. The broker thread must never block, so
// Enqueue returns false on overflow rather than waiting.
type Publisher interface {
	Enqueue(subject string, data []byte) bool
	RecordDrop()
}
