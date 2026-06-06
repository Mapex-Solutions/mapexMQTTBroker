# Bounded Context: Auth

**Service:** mqtt-broker
**Module path:** `src/modules/auth/`
**Owner:** Mapex Platform team
**Last reviewed:** 2026-06-05

## Purpose
The access-control boundary. It decides every device CONNECT (authentication)
and authorizes every publish/subscribe (ACL), deciding locally against a
tiered auth cache so the broker thread never makes a per-CONNECT HTTP call. It
owns the trust anchor: the asset's auth projection (enabled flag, orgId, auth
mode, password hash, cert serial).

## Ubiquitous Language
| Term | Meaning in this context | Not to be confused with |
|------|-------------------------|--------------------------|
| AuthEntry | Decided auth projection for one asset | The full asset document owned by the assets service |
| Decision | Verdict (allow/deny/error) + trusted (orgId, assetUUID) on allow | The MQTT CONNACK the broker sends |
| Tiered store | L1 Pebble → L2 MinIO → L3 HTTP, walked in latency order | A single cache |
| Authorize | Topic-level ACL check (events/ vs commands/) | Authenticate (the CONNECT decision) |

## Published Events (driven — outbound)
None. The module is read-only against the assets service.

## Consumed Events (driving — inbound)
| Event | Subject | Payload (ref) | Publishers |
|-------|---------|----------------|-------------|
| Asset auth invalidate | `mapexos.fanout.asset.invalidate` | `application/dtos.FanoutInvalidatePayload` | assets service |

## Driving Ports (what can call this module)
- `Service.Authenticate` — from the cgo BASIC_AUTH callback.
- `Service.Authorize` — from the cgo ACL_CHECK callback.
- NATS consumer on the invalidate subject (`interfaces/message/consumers/fanout`).

## Driven Ports (what this module requires)
- `AuthStore` — tiered read path (Pebble + MinIO + HTTP). `application/ports/authstore.go`.
- `AuthLookup` — L3 fallback fetch. `application/ports/authstore.go`.
- `PasswordVerifier` — bcrypt compare. `application/ports/verifier.go`.

## Invariants and Business Rules
- Default-deny: malformed username, missing entry, disabled asset, or
  credential mismatch denies.
- Fail-closed: every layer unreachable denies, never admits on a guess.
- Auth-mode mutual exclusion: an asset declares exactly one mode; the wrong
  credential shape denies before validation.
- Identity on the wire is the bare assetUUID; tenant scoping flows from the
  entry's orgId.

## Known Cross-Context Interactions
- Produces a `Decision` consumed by the cgo entry to drive **session** and
  **presence** on allow.
- Reads asset auth projections produced by the **assets** service (L2 bucket
  `mapex-asset-auth`, L3 `/internal/asset-auth/:assetUUID`), invalidated by its
  fanout on every asset CRUD.
