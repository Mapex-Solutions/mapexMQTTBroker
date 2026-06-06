package constants

// MaxIngressPayloadBytes caps the device payload size the broker forwards to
// NATS. The default NATS server max_payload is 1 MiB; we leave headroom for
// the JSON envelope, metadata fields, and base64 inflation. Devices
// publishing larger payloads are dropped with a structured warn log.
const MaxIngressPayloadBytes = 900 * 1024
