package ports

import "context"

// Service is the auth module's driving surface: the CONNECT decision plus the
// per-message topic authorization. Implemented by the application service; the
// compile-time check lives in auth_service.go.
type Service interface {
	Authenticate(ctx context.Context, username, password, certSerial string) Decision
	Authorize(username, topic string, acc int) bool
}
