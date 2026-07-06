package ports

// Deliverer is the driven port the downlink service needs: publish a payload
// to a broker-local MQTT topic so the subscribed device receives it. The cgo
// entry implements it with mosquitto_broker_publish; tests inject a fake.
type Deliverer interface {
	Deliver(topic string, payload []byte, qos int) error
}
