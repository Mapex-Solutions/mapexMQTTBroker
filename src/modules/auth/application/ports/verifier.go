package ports

// PasswordVerifier compares a plaintext password against a stored bcrypt hash
// on the broker thread. Pulled behind a port so the auth service does not
// depend on the concrete HTTP client for a pure CPU compare.
type PasswordVerifier interface {
	CompareLocal(hash, plaintext string) bool
}
