package dtos

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/entities"
)

// AuthProjection mirrors the slim contract written by the assets service to
// `mapex-asset-auth` and returned by `/internal/asset-auth/:uuid`. Forward-
// compatible because json.Unmarshal ignores unknown keys. The MQTT auth fields
// are nested under `mqtt` (symmetric with the LNS `lorawan` block) and flatten
// into AuthEntry on projection.
type AuthProjection struct {
	AssetUUID string    `json:"assetUUID"`
	OrgId     string    `json:"orgId"`
	Enabled   bool      `json:"enabled"`
	Type      string    `json:"type"`
	Mqtt      *MqttAuth `json:"mqtt"`
}

// MqttAuth is the nested MQTT auth block. Set when Type == "mqtt".
type MqttAuth struct {
	AuthType          string `json:"authType"`
	PasswordHash      string `json:"passwordHash"`
	CurrentCertSerial string `json:"currentCertSerial"`
}

// authProjectionEnvelope is the platform's standard HTTP response envelope
// (`{status, errors, data}`). The L3 HTTP path returns this; the L2 MinIO
// object is the raw AuthProjection without an envelope.
type authProjectionEnvelope struct {
	Data AuthProjection `json:"data"`
}

// ToAuthEntry projects the slim AuthProjection into the AuthEntry the broker
// caches and decides CONNECTs against.
func (p *AuthProjection) ToAuthEntry() *entities.AuthEntry {
	entry := &entities.AuthEntry{
		Enabled:   p.Enabled,
		AssetUUID: p.AssetUUID,
		OrgId:     p.OrgId,
	}
	if p.Mqtt != nil {
		entry.AuthType = p.Mqtt.AuthType
		entry.PasswordHash = p.Mqtt.PasswordHash
		entry.CurrentCertSerial = p.Mqtt.CurrentCertSerial
	}
	return entry
}

// DecodeReadModelEnvelopeToAuthEntry parses the standard HTTP response
// envelope (used by the L3 fallback) and projects to AuthEntry.
func DecodeReadModelEnvelopeToAuthEntry(r io.Reader) (*entities.AuthEntry, error) {
	var env authProjectionEnvelope
	if err := json.NewDecoder(r).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode auth projection envelope: %w", err)
	}
	return env.Data.ToAuthEntry(), nil
}

// DecodeReadModelToAuthEntry parses the raw AuthProjection JSON (no envelope)
// and projects to AuthEntry. Used by the L2 MinIO path.
func DecodeReadModelToAuthEntry(r io.Reader) (*entities.AuthEntry, error) {
	var proj AuthProjection
	if err := json.NewDecoder(r).Decode(&proj); err != nil {
		return nil, fmt.Errorf("decode auth projection: %w", err)
	}
	return proj.ToAuthEntry(), nil
}
