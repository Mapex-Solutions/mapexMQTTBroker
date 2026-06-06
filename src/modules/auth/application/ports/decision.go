package ports

// Decision is the auth service's verdict for a CONNECT. On AuthAllow it also
// carries the trusted (orgId, assetUUID) so the entry orchestrator can record
// the session and emit presence without re-reading the entry.
type Decision struct {
	Result    AuthResult
	OrgID     string
	AssetUUID string
}
