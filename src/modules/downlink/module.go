// Package downlink is the platform->device command bounded context: it
// consumes the STATIC mqtt.downlink subject (DownlinkEnvelope, identity in
// the payload) and delivers each command to the device on the broker's device
// topic contract commands/{assetUUID}/{commandType}. The cgo entry provides
// the Deliverer (mosquitto_broker_publish); this module owns parse + routing.
package downlink

import (
	"github.com/nats-io/nats.go"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/downlink/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/downlink/application/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/downlink/interfaces/message"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// Deliverer is the driven port the downlink module requires (implemented by
// the cgo entry over mosquitto_broker_publish).
type Deliverer = ports.Deliverer

// Config aggregates the module wiring inputs.
type Config struct {
	Conn      *nats.Conn
	Deliverer Deliverer
	Subject   string
	Stream    string
	Durable   string
	Queue     string
	Log       logging.Logger
}

// Module owns the consumer lifecycle handle.
type Module struct {
	consumer *message.Consumer
}

// Build wires the service + consumer and starts consuming.
func Build(cfg Config) (*Module, error) {
	svc := services.New(cfg.Deliverer, cfg.Log)
	consumer, err := message.NewConsumer(message.ConsumerConfig{
		Conn:    cfg.Conn,
		Service: svc,
		Subject: cfg.Subject,
		Stream:  cfg.Stream,
		Durable: cfg.Durable,
		Queue:   cfg.Queue,
		Log:     cfg.Log,
	})
	if err != nil {
		return nil, err
	}
	if err := consumer.Start(); err != nil {
		return nil, err
	}
	return &Module{consumer: consumer}, nil
}

// Shutdown stops the consumer. Idempotent.
func (m *Module) Shutdown() {
	if m == nil || m.consumer == nil {
		return
	}
	_ = m.consumer.Stop()
}
