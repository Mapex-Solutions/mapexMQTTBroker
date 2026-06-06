package fanout

// defaultSubject is the NATS subject the assets service publishes
// invalidations to. Operators can override per environment so multiple
// deploys can share a NATS cluster.
const defaultSubject = "mapexos.fanout.asset.invalidate"
