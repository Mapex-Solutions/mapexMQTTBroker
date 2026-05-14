package broker

import "strings"

// Auth-mode sentinel values; the asset declares one and the broker
// enforces mutual exclusion. Defined here instead of imported from
// the MapexOS contracts module because the broker plugin lives in
// its own repo — the JSON shape is the contract, not the Go type
// identity. Must stay in sync with `contracts/services/assets/assets/
// constants.go`.
const (
	AuthTypePassword = "password"
	AuthTypeCert     = "cert"
)

// AuthEntry mirrors the cache shape written by the assets MS to L1
// (Pebble) and L2 (MinIO `mapex-asset-auth` bucket, slim
// AuthProjection). Field tags MUST stay in sync with the platform's
// `packages/contracts/services/assets/auth/dto.go::AuthProjection`.
type AuthEntry struct {
	Enabled           bool   `json:"enabled"`
	AssetUUID         string `json:"assetUUID"`
	OrgId             string `json:"orgId"`
	AuthType          string `json:"authType"`
	PasswordHash      string `json:"passwordHash"`
	CurrentCertSerial string `json:"currentCertSerial,omitempty"`
}

// AuthorizesPassword reports whether this entry can satisfy a
// password-mode CONNECT. Requires the asset to be enabled, declared
// as AuthType=password, AND to have a non-empty bcrypt hash. Cert-mode
// assets always return false here even if a stale hash lingers.
func (e *AuthEntry) AuthorizesPassword() bool {
	return e != nil && e.Enabled && e.AuthType == AuthTypePassword && e.PasswordHash != ""
}

// AuthorizesCertSerial reports whether the supplied cert serial matches
// the asset's single current cert. Empty CurrentCertSerial means the
// asset has no active cert (default deny). Case-insensitive comparison —
// OpenSSL hex output may vary in case across vendor toolchains.
// Password-mode assets always return false even when a cert serial
// happens to match — the asset has not opted in to cert auth.
func (e *AuthEntry) AuthorizesCertSerial(serial string) bool {
	if e == nil || !e.Enabled || serial == "" {
		return false
	}
	if e.AuthType != AuthTypeCert {
		return false
	}
	return e.CurrentCertSerial != "" && strings.EqualFold(e.CurrentCertSerial, serial)
}
