package constants

// Auth-mode sentinel values; the asset declares one and the broker enforces
// mutual exclusion. Defined here instead of imported from the MapexOS
// contracts module because the broker plugin lives in its own repo — the JSON
// shape is the contract, not the Go type identity. Must stay in sync with
// `contracts/services/assets/assets/constants.go`.
const (
	AuthTypePassword = "password"
	AuthTypeCert     = "cert"
)
