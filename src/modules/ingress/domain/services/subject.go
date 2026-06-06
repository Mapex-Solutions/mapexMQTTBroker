package services

// ValidSubjectToken rejects strings that would corrupt a NATS subject when
// concatenated. NATS forbids "." (token separator), "*" / ">" (subscribe-time
// wildcards), and whitespace (protocol-line separator). Empty tokens are also
// rejected — they indicate a platform invariant violation upstream (assets
// service should never persist empty identity tokens).
func ValidSubjectToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch c {
		case '.', '*', '>', ' ', '\t', '\n', '\r':
			return false
		}
	}
	return true
}
