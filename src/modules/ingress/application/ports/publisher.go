package ports

// Publisher is the driven port the ingress service needs: a non-blocking
// enqueue plus a drop accounter for pre-publish validation rejects. The
// broker thread must never block, so Enqueue returns false on overflow.
type Publisher interface {
	Enqueue(subject string, data []byte) bool
	RecordDrop()
}
