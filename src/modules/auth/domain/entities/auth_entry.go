package entities

import (
	"strings"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/constants"
)

// AuthEntry is the broker-internal, flat cache shape held in L1 (Pebble). It is
// projected from the wire AuthProjection, whose MQTT fields are nested under a
// `mqtt` block (see dtos.AuthProjection / the platform contract
// `packages/contracts/services/assets/auth/dto.go::AuthProjection`). The wire
// nesting flattens here; these tags are the L1 storage format, broker-owned.
type AuthEntry struct {
	Enabled           bool   `json:"enabled"`
	AssetUUID         string `json:"assetUUID"`
	OrgId             string `json:"orgId"`
	AuthType          string `json:"authType"`
	PasswordHash      string `json:"passwordHash"`
	CurrentCertSerial string `json:"currentCertSerial,omitempty"`
}

// AuthorizesPassword reports whether this entry can satisfy a password-mode
// CONNECT. Requires the asset to be enabled, declared as AuthType=password,
// AND to have a non-empty bcrypt hash. Cert-mode assets always return false
// here even if a stale hash lingers.
func (e *AuthEntry) AuthorizesPassword() bool {
	return e != nil && e.Enabled && e.AuthType == constants.AuthTypePassword && e.PasswordHash != ""
}

// AuthorizesCertSerial reports whether the supplied cert serial matches the
// asset's single current cert. Empty CurrentCertSerial means the asset has no
// active cert (default deny). Case-insensitive comparison — OpenSSL hex output
// may vary in case across vendor toolchains. Password-mode assets always
// return false even when a cert serial happens to match.
func (e *AuthEntry) AuthorizesCertSerial(serial string) bool {
	if e == nil || !e.Enabled || serial == "" {
		return false
	}
	if e.AuthType != constants.AuthTypeCert {
		return false
	}
	return e.CurrentCertSerial != "" && strings.EqualFold(e.CurrentCertSerial, serial)
}
