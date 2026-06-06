package ports

// AuthResult is the binary outcome the auth service returns for a CONNECT. The
// decision happens LOCALLY using the AuthEntry from the AuthStore — there is
// no HTTP auth callout on the broker thread.
type AuthResult int

const (
	// AuthAllow — entry found and credentials match (bcrypt for password mode,
	// serial-equality for cert mode).
	AuthAllow AuthResult = iota

	// AuthDeny — entry not found, asset disabled, cross-tenant attempt, or
	// credential mismatch. All deny reasons collapse to this so the wire never
	// enumerates valid usernames.
	AuthDeny

	// AuthError — the AuthStore reported every layer unreachable or the service
	// is misconfigured. Caller MUST fail closed.
	AuthError
)
