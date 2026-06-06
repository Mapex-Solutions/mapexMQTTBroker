# Bounded Context: Presence

**Service:** mqtt-broker
**Module path:** `src/modules/presence/`
**Owner:** Mapex Platform team
**Last reviewed:** 2026-06-05

## Purpose
The online/offline-edge boundary. It publishes a connect or disconnect
advisory to the shared presence subject whenever a device's session opens or
closes, translating broker disconnect reason codes into stable text buckets.

## Ubiquitous Language
| Term | Meaning in this context | Not to be confused with |
|------|-------------------------|--------------------------|
| Presence advisory | Connect/disconnect event with trusted identity + reason | The broker's `$SYS` client counters |
| Reason bucket | Stable text label for a Mosquitto disconnect code | The raw numeric reason code |

## Published Events (driven — outbound)
| Event | Subject | Payload (ref) | Consumers |
|-------|---------|----------------|-----------|
| Connect / disconnect | `{env}.mapexos.mqtt.presence.advisory` | `application/dtos.PresenceAdvisory` | healthmonitor |

## Consumed Events (driving — inbound)
None — invoked directly by the cgo entry.

## Driving Ports (what can call this module)
- `Service.PublishConnect` / `Service.PublishDisconnect` — from the cgo
  BASIC_AUTH (success) and DISCONNECT callbacks.

## Driven Ports (what this module requires)
- `Publisher` — async NATS enqueue + drop accounter. `application/ports/publisher.go`.

## Invariants and Business Rules
- A successful auth is the canonical connect signal (Mosquitto 2.0.x has no
  `MOSQ_EVT_CONNECT`).
- Marshalling failure or queue overflow drops the advisory rather than
  blocking the broker thread.

## Known Cross-Context Interactions
- Invoked by the cgo entry after **auth** allows and **session** records the
  identity. Consumed by **healthmonitor** for the online/offline edge.
