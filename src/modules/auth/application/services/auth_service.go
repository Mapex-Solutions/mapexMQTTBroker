package services

import (
	"context"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
	domainsvc "github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/textutil"
)

var _ ports.Service = (*Service)(nil)

// New constructs the auth service from its port dependencies.
func New(store ports.AuthStore, verifier ports.PasswordVerifier, log logging.Logger) *Service {
	if log == nil {
		log = logging.NopLogger{}
	}
	return &Service{store: store, verifier: verifier, log: log}
}

// Authenticate decides a CONNECT. It parses the bare assetUUID, loads its
// AuthEntry from the tiered store, confirms the asset is enabled, then decides
// by the asset's declared auth mode. On allow it returns the trusted
// (orgId, assetUUID) so the entry can record the session and emit presence.
func (s *Service) Authenticate(ctx context.Context, username, password, certSerial string) ports.Decision {
	assetUUID, ok := domainsvc.ParseUsername(username)
	if !ok {
		s.log.Debug("[SERVICE:Auth] deny: malformed username user=%s", textutil.Truncate(username, 64))
		return ports.Decision{Result: ports.AuthDeny}
	}
	entry, decided, res := s.loadAuthEntry(ctx, assetUUID, username)
	if decided {
		return ports.Decision{Result: res}
	}
	if !entry.Enabled {
		s.log.Debug("[SERVICE:Auth] deny: asset disabled user=%s", textutil.Truncate(username, 64))
		return ports.Decision{Result: ports.AuthDeny}
	}
	res = s.decideAuthMode(entry, username, password, certSerial)
	if res == ports.AuthAllow {
		return ports.Decision{Result: ports.AuthAllow, OrgID: entry.OrgId, AssetUUID: assetUUID}
	}
	return ports.Decision{Result: res}
}

// Authorize returns whether the device may perform the access on the topic.
// Pure ACL check — no I/O — so it runs inline on the broker's event loop.
func (s *Service) Authorize(username, topic string, acc int) bool {
	return domainsvc.CheckACL(username, topic, acc)
}
