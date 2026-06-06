# Bounded Context: Ingress

**Service:** mqtt-broker
**Module path:** `src/modules/ingress/`
**Owner:** Mapex Platform team
**Last reviewed:** 2026-06-05

## Purpose
The device-data-forwarding boundary. It validates and projects each MQTT
publish onto a per-device NATS subject so downstream stream workers route each
device's messages without a fan-in step. It owns the payload-size cap and the
subject-token safety invariants.

## Ubiquitous Language
| Term | Meaning in this context | Not to be confused with |
|------|-------------------------|--------------------------|
| Ingress | A device publish forwarded onto NATS | NATS JetStream ingestion downstream |
| Subject token | An orgId / assetUUID segment of the composed subject | The MQTT topic the device published to |

## Published Events (driven — outbound)
| Event | Subject | Payload (ref) | Consumers |
|-------|---------|----------------|-----------|
| Device message | `{env}.mapexos.mqtt.data.{orgId}.{assetUUID}` | `application/dtos.IngressMessage` | js-executor |

## Consumed Events (driving — inbound)
None — invoked directly by the cgo entry.

## Driving Ports (what can call this module)
- `Service.Publish` — from the cgo MESSAGE callback.

## Driven Ports (what this module requires)
- `Publisher` — async NATS enqueue + drop accounter. `application/ports/publisher.go`.

## Invariants and Business Rules
- NATS-illegal subject tokens (`.`, `*`, `>`, whitespace, empty) drop the
  message with a warn log.
- Payloads above `MaxIngressPayloadBytes` (900 KiB) drop with a warn log.
- Drop-on-overflow: the broker thread never blocks on a slow downstream.

## Known Cross-Context Interactions
- Invoked by the cgo entry using the trusted identity from **session**.
  Consumed by **js-executor** via a wildcard subscription on `{prefix}.>`.
