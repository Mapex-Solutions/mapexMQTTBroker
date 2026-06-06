# Bounded Context: Session

**Service:** mqtt-broker
**Module path:** `src/modules/session/`
**Owner:** Mapex Platform team
**Last reviewed:** 2026-06-05

## Purpose
The connection-identity boundary. It tracks the trusted (orgId, assetUUID) per
Mosquitto clientID across the auth, presence and ingress callbacks, so the
Disconnect / Message events can compose NATS subjects without re-reading the
auth projection. The bare assetUUID on the wire no longer carries the orgId —
this module fills the gap. Purely in-memory state, no I/O.

## Ubiquitous Language
| Term | Meaning in this context | Not to be confused with |
|------|-------------------------|--------------------------|
| Session | Trusted (orgId, assetUUID) captured at CONNECT, keyed by clientID | The MQTT session/persistence state owned by the broker |

## Published Events (driven — outbound)
None.

## Consumed Events (driving — inbound)
None — invoked directly by the cgo entry.

## Driving Ports (what can call this module)
- `Service.Remember` — from the cgo BASIC_AUTH (success) callback.
- `Service.Lookup` — from the cgo DISCONNECT and MESSAGE callbacks.
- `Service.Forget` — from the cgo DISCONNECT callback.

## Driven Ports (what this module requires)
None — the store is an in-memory `sync.Map`.

## Invariants and Business Rules
- Remember is idempotent; a re-CONNECT under the same clientID overwrites.
- Lookup returns false for any clientID that never authenticated (service
  accounts, stale callbacks) — callers MUST treat that as non-actionable.

## Known Cross-Context Interactions
- Written by the cgo entry after **auth** allows; read by it to drive
  **presence** and **ingress**.
