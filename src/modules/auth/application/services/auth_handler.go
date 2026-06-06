package services

import (
	"context"
	"errors"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/constants"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/entities"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/textutil"
)

// loadAuthEntry fetches the AuthEntry and maps store outcomes to an early
// decision. The bool reports whether the caller should short-circuit with the
// returned result: a missing entry denies (default-deny), an unreachable store
// errors (fail-closed), and a missing store is a misconfiguration error.
func (s *Service) loadAuthEntry(ctx context.Context, assetUUID, username string) (*entities.AuthEntry, bool, ports.AuthResult) {
	if s.store == nil {
		s.log.Error("[SERVICE:Auth] authenticate called without an auth store configured")
		return nil, true, ports.AuthError
	}
	entry, err := s.store.Get(ctx, assetUUID)
	if err != nil {
		if errors.Is(err, ports.ErrAuthEntryNotFound) {
			s.log.Debug("[SERVICE:Auth] deny: entry not found user=%s", textutil.Truncate(username, 64))
			return nil, true, ports.AuthDeny
		}
		s.log.Warn("[SERVICE:Auth] error: store unavailable user=%s err=%v", textutil.Truncate(username, 64), err)
		return nil, true, ports.AuthError
	}
	return entry, false, ports.AuthAllow
}

// decideAuthMode enforces auth-mode mutual exclusion: the asset declares ONE
// mode and the broker rejects any CONNECT presenting the wrong credential
// shape. This dispatch runs before credential validation so a misconfigured
// device surface-fails on the mode mismatch.
func (s *Service) decideAuthMode(entry *entities.AuthEntry, username, password, certSerial string) ports.AuthResult {
	switch entry.AuthType {
	case constants.AuthTypeCert:
		return s.authorizeCert(entry, username, certSerial)
	case constants.AuthTypePassword:
		return s.authorizePassword(entry, username, password, certSerial)
	default:
		s.log.Warn("[SERVICE:Auth] deny: asset has unknown authType=%q user=%s",
			entry.AuthType, textutil.Truncate(username, 64))
		return ports.AuthDeny
	}
}

// authorizeCert validates a cert-mode CONNECT. A cert-mode asset reached on the
// plaintext listener (no serial) is denied; otherwise the presented serial
// must match the asset's single active cert.
func (s *Service) authorizeCert(entry *entities.AuthEntry, username, certSerial string) ports.AuthResult {
	if certSerial == "" {
		s.log.Debug("[SERVICE:Auth] deny: cert-mode asset reached on plaintext listener user=%s",
			textutil.Truncate(username, 64))
		return ports.AuthDeny
	}
	if entry.AuthorizesCertSerial(certSerial) {
		s.log.Debug("[SERVICE:Auth] allow: cert user=%s serial=%s",
			textutil.Truncate(username, 64), textutil.Truncate(certSerial, 32))
		return ports.AuthAllow
	}
	s.log.Debug("[SERVICE:Auth] deny: cert serial not in active list user=%s serial=%s",
		textutil.Truncate(username, 64), textutil.Truncate(certSerial, 32))
	return ports.AuthDeny
}

// authorizePassword validates a password-mode CONNECT. A password-mode asset
// presenting a cert is denied; otherwise the bcrypt compare runs locally on
// the broker thread against the entry's stored hash.
func (s *Service) authorizePassword(entry *entities.AuthEntry, username, password, certSerial string) ports.AuthResult {
	if certSerial != "" {
		s.log.Debug("[SERVICE:Auth] deny: password-mode asset presented a cert user=%s",
			textutil.Truncate(username, 64))
		return ports.AuthDeny
	}
	if !entry.AuthorizesPassword() {
		s.log.Debug("[SERVICE:Auth] deny: no password configured user=%s", textutil.Truncate(username, 64))
		return ports.AuthDeny
	}
	if s.verifier == nil {
		s.log.Error("[SERVICE:Auth] authenticate cannot bcrypt: no password verifier configured")
		return ports.AuthError
	}
	if s.verifier.CompareLocal(entry.PasswordHash, password) {
		s.log.Debug("[SERVICE:Auth] allow: password user=%s", textutil.Truncate(username, 64))
		return ports.AuthAllow
	}
	s.log.Debug("[SERVICE:Auth] deny: password mismatch user=%s", textutil.Truncate(username, 64))
	return ports.AuthDeny
}
