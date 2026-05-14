package broker

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// MaxIngressPayloadBytes caps the device payload size we will forward
// to NATS as part of an IngressMessage. The default NATS server
// max_payload is 1 MiB; we leave headroom for the JSON envelope,
// metadata fields, and base64 inflation. Devices publishing larger
// payloads than this are dropped with a structured warn log so ops
// sees the offending asset + topic without enabling debug.
const MaxIngressPayloadBytes = 900 * 1024

// PluginRuntime aggregates the runtime state the cgo entry holds at
// process scope: the loaded config, the async NATS publisher, the
// HTTP auth client, the TieredStore for the cached read path, and
// a Logger. Stays in a struct so unit tests can instantiate a
// runtime with fakes and exercise every path end-to-end without cgo.
type PluginRuntime struct {
	Config Config
	Async  *AsyncPublisher
	Auth   *AuthClient
	Store  AuthStore
	Fanout *FanoutConsumer
	Log    Logger

	// sessions tracks the trusted orgId per connected client so the
	// Disconnect / Message callbacks can publish ingress + presence
	// without re-reading the AuthEntry. Populated on AuthAllow,
	// drained on Disconnect. Bare assetUUID on wire means the orgId
	// is no longer derivable from the username — this map fills the
	// gap. sync.Map gives us lock-free reads on the hot path.
	sessions sync.Map
}

// SessionInfo is the per-connection state captured at CONNECT time.
// Held in PluginRuntime.sessions keyed by Mosquitto clientID so the
// later Disconnect / Message callbacks can compose NATS subjects
// with the trusted orgId from the auth projection.
type SessionInfo struct {
	OrgID     string
	AssetUUID string
}

// rememberSession stores the trusted (orgId, assetUUID) for the
// supplied clientID. Idempotent — a re-CONNECT under the same
// clientID overwrites the prior entry.
func (r *PluginRuntime) rememberSession(clientID, orgID, assetUUID string) {
	if clientID == "" {
		return
	}
	r.sessions.Store(clientID, SessionInfo{OrgID: orgID, AssetUUID: assetUUID})
}

// LookupSession returns the SessionInfo captured at CONNECT for the
// supplied clientID. Returns zero-value + false when no auth has
// taken place — caller MUST treat that as a non-actionable event
// (service-account user, race during disconnect, malformed callback).
func (r *PluginRuntime) LookupSession(clientID string) (SessionInfo, bool) {
	if clientID == "" {
		return SessionInfo{}, false
	}
	v, ok := r.sessions.Load(clientID)
	if !ok {
		return SessionInfo{}, false
	}
	info, ok := v.(SessionInfo)
	if !ok {
		return SessionInfo{}, false
	}
	return info, true
}

// ForgetSession removes the per-connection entry. Called on
// Disconnect so stale clientIDs do not accumulate over a long
// process lifetime. Exported so the cgo trampoline can call it
// without reaching into unexported helpers.
func (r *PluginRuntime) ForgetSession(clientID string) {
	if clientID == "" {
		return
	}
	r.sessions.Delete(clientID)
}

// PublishConnect emits a CONNECT advisory to the presence subject.
// Returns true on accept, false on overflow drop. Marshalling errors
// (which should never happen for the well-typed PresenceAdvisory) are
// silently treated as drops with the dropped counter incremented.
func (r *PluginRuntime) PublishConnect(orgID, assetUUID, clientID, sourceIP string, ts time.Time) bool {
	r.logger().Debug("connect: org=%s asset=%s client=%s ip=%s",
		truncate(orgID, 64), truncate(assetUUID, 64), truncate(clientID, 64), sourceIP)
	adv := PresenceAdvisory{
		Event:     EventConnect,
		OrgID:     orgID,
		AssetUUID: assetUUID,
		ClientID:  clientID,
		SourceIP:  sourceIP,
		Timestamp: ts,
	}
	return r.publish(r.Config.SubjectPresence, adv)
}

// PublishDisconnect emits a DISCONNECT advisory with the broker's
// reason code translated to a stable text bucket via
// MapDisconnectReason. ts is the broker's wall-clock time at the
// disconnect callback.
func (r *PluginRuntime) PublishDisconnect(orgID, assetUUID, clientID, sourceIP string, reasonCode int, ts time.Time) bool {
	reason := MapDisconnectReason(reasonCode)
	r.logger().Debug("disconnect: org=%s asset=%s client=%s reason=%s code=%d",
		truncate(orgID, 64), truncate(assetUUID, 64), truncate(clientID, 64), reason, reasonCode)
	adv := PresenceAdvisory{
		Event:      EventDisconnect,
		OrgID:      orgID,
		AssetUUID:  assetUUID,
		ClientID:   clientID,
		SourceIP:   sourceIP,
		Timestamp:  ts,
		ReasonCode: reasonCode,
		ReasonText: reason,
	}
	return r.publish(r.Config.SubjectPresence, adv)
}

// PublishIngress emits an MQTT message envelope to a per-device
// ingress subject so the JS-Executor's wildcard consumer at
// "{prefix}.>" routes each device's messages to its own filter chain
// without a runtime fan-in step. Final subject:
// "{SubjectIngressPrefix}.{orgId}.{assetUUID}".
//
// Validates two safety invariants before publishing:
//
//   - orgId / assetUUID MUST NOT contain NATS-illegal subject tokens
//     (".", "*", ">", whitespace) — otherwise the composed subject
//     would route to the wrong filter or fail server-side. Drops the
//     message with a warn log so the offender is visible.
//
//   - Payload size MUST be under MaxIngressPayloadBytes (default
//     ~900 KiB). Messages above the threshold are dropped with a warn
//     log identifying the asset + topic + size; the underlying NATS
//     server would reject anyway, this just surfaces it earlier.
//
// The plugin holds the message payload in memory only long enough to
// marshal — backpressure on the async queue translates to drops, and
// the broker's keep-alive logic already retransmits MQTT messages on
// QoS 1+ if the device retries.
func (r *PluginRuntime) PublishIngress(orgID, assetUUID, clientID, topic string, payload []byte, qos int, retain bool, ts time.Time) bool {
	if !validSubjectToken(orgID) || !validSubjectToken(assetUUID) {
		r.logger().Warn("ingress dropped: invalid subject token org=%q asset=%q topic=%s",
			truncate(orgID, 64), truncate(assetUUID, 64), truncate(topic, 128))
		r.Async.dropped.Add(1)
		return false
	}
	if len(payload) > MaxIngressPayloadBytes {
		r.logger().Warn("ingress dropped: payload too large org=%s asset=%s topic=%s size=%d max=%d",
			truncate(orgID, 64), truncate(assetUUID, 64), truncate(topic, 128),
			len(payload), MaxIngressPayloadBytes)
		r.Async.dropped.Add(1)
		return false
	}

	msg := IngressMessage{
		OrgID:     orgID,
		AssetUUID: assetUUID,
		ClientID:  clientID,
		Topic:     topic,
		Payload:   payload,
		QoS:       qos,
		Retain:    retain,
		Timestamp: ts,
	}
	subject := r.Config.SubjectIngressPrefix + "." + orgID + "." + assetUUID
	return r.publish(subject, msg)
}

// Authenticate is the plugin-thread entry point invoked from the
// cgo MOSQ_EVT_BASIC_AUTH callback. It runs SYNCHRONOUSLY on the
// broker thread because Mosquitto blocks the CONNECT handshake
// awaiting the auth decision — async would defeat the protocol.
//
// Flow:
//
//  1. Parse username (bare assetUUID). Malformed → DENY.
//  2. Lookup AuthEntry via TieredStore (L1 → L2 → L3).
//     Not found → DENY (default-deny).
//     Store error → ERROR (caller maps to AUTH-fail; broker drops
//     the CONNECT and the device retries).
//  3. Asset enabled? Mismatch → DENY.
//  4. Auth method:
//     - certSerial != "" → check entry.AuthorizesCertSerial
//     - else → bcrypt(entry.PasswordHash, password) via AuthClient
//  5. On Allow → persist the trusted (orgId, assetUUID) under the
//     supplied clientID so later Disconnect / Message callbacks can
//     compose NATS subjects without a re-lookup.
//
// certSerial is the device cert's serial (hex) extracted at the cgo
// boundary via OpenSSL. Empty when the device used password auth on
// the plaintext listener. The wire username carries no orgId — the
// auth projection's OrgId is the trust anchor and ships with the
// returned entry; tenant scoping is server-side only.
func (r *PluginRuntime) Authenticate(ctx context.Context, username, password, clientID, certSerial string) AuthResult {
	assetUUID, ok := ResolveUsername(username)
	if !ok {
		r.logger().Debug("auth deny: malformed username user=%s", truncate(username, 64))
		return AuthDeny
	}

	if r.Store == nil {
		r.logger().Error("authenticate called without an auth store configured")
		return AuthError
	}

	entry, err := r.Store.Get(ctx, assetUUID)
	if err != nil {
		if errIs(err, ErrAuthEntryNotFound) {
			r.logger().Debug("auth deny: entry not found user=%s", truncate(username, 64))
			return AuthDeny
		}
		r.logger().Warn("auth error: store unavailable user=%s err=%v", truncate(username, 64), err)
		return AuthError
	}

	if !entry.Enabled {
		r.logger().Debug("auth deny: asset disabled user=%s", truncate(username, 64))
		return AuthDeny
	}

	// Mutual exclusion gate: the asset declares ONE auth mode and the
	// broker rejects any CONNECT that presents the wrong shape. This
	// runs before credential validation so a misconfigured device
	// surface-fails on the mode mismatch instead of a confusing
	// "password mismatch" or "cert not in active list" message.
	switch entry.AuthType {
	case AuthTypeCert:
		if certSerial == "" {
			r.logger().Debug("auth deny: cert-mode asset reached on plaintext listener user=%s",
				truncate(username, 64))
			return AuthDeny
		}
		if entry.AuthorizesCertSerial(certSerial) {
			r.logger().Debug("auth allow: cert user=%s serial=%s", truncate(username, 64), truncate(certSerial, 32))
			r.rememberSession(clientID, entry.OrgId, assetUUID)
			return AuthAllow
		}
		r.logger().Debug("auth deny: cert serial not in active list user=%s serial=%s",
			truncate(username, 64), truncate(certSerial, 32))
		return AuthDeny

	case AuthTypePassword:
		if certSerial != "" {
			r.logger().Debug("auth deny: password-mode asset presented a cert user=%s",
				truncate(username, 64))
			return AuthDeny
		}
		if !entry.AuthorizesPassword() {
			r.logger().Debug("auth deny: no password configured user=%s", truncate(username, 64))
			return AuthDeny
		}
		if r.Auth == nil {
			r.logger().Error("authenticate cannot bcrypt: no AuthClient (legacy path)")
			return AuthError
		}
		if r.Auth.CompareLocal(entry.PasswordHash, password) {
			r.logger().Debug("auth allow: password user=%s", truncate(username, 64))
			r.rememberSession(clientID, entry.OrgId, assetUUID)
			return AuthAllow
		}
		r.logger().Debug("auth deny: password mismatch user=%s", truncate(username, 64))
		return AuthDeny

	default:
		// Empty or unknown authType — asset is misconfigured. Default
		// deny rather than guessing which credential to validate.
		r.logger().Warn("auth deny: asset has unknown authType=%q user=%s",
			entry.AuthType, truncate(username, 64))
		return AuthDeny
	}
}

// errIs is a tiny errors.Is shim — pulled into a helper so the cgo
// trampoline path doesn't need to import errors at the call site.
func errIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		// no Unwrap chain support needed yet; sentinels are returned directly
		return false
	}
	return false
}

// publish marshals the supplied envelope and hands it to the async
// publisher. Marshalling failures are translated to drops; the
// plugin's broader strategy is liveness-over-correctness on the
// broker thread, so individual lost events are acceptable as long as
// the broker never blocks.
func (r *PluginRuntime) publish(subject string, payload any) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		r.logger().Error("marshal failure: subject=%s err=%v", subject, err)
		r.Async.dropped.Add(1)
		return false
	}
	return r.Async.Enqueue(subject, data)
}

// logger returns the runtime's Logger or a no-op fallback so callers
// that initialize PluginRuntime without setting Log don't panic. The
// production cgo init MUST set Log; this guard is for tests + edge
// scenarios.
func (r *PluginRuntime) logger() Logger {
	if r.Log == nil {
		return nopLogger{}
	}
	return r.Log
}

// ResolveUsername validates the device username. Re-export of
// parseUsername with a stable name the cgo entry binds against
// without reaching into unexported helpers. The platform username
// is the bare assetUUID — no orgId is derivable from the wire.
func ResolveUsername(username string) (assetUUID string, ok bool) {
	return parseUsername(username)
}

// validSubjectToken rejects strings that would corrupt a NATS subject
// when concatenated. NATS forbids:
//
//   - "." (token separator — would split a single token into many)
//   - "*" (single-token wildcard at subscribe time)
//   - ">" (multi-token wildcard at subscribe time)
//   - whitespace (parsed as protocol-line separator)
//
// Empty tokens are also rejected — an empty orgId or assetUUID would
// produce "..xxx" which NATS rejects but more importantly indicates a
// platform invariant violation upstream (assets MS should never persist
// empty identity tokens).
func validSubjectToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch r {
		case '.', '*', '>', ' ', '\t', '\n', '\r':
			return false
		}
	}
	return true
}
