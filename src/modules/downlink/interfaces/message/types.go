package message

import (
	"sync"

	"github.com/nats-io/nats.go"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/downlink/application/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// ConsumerConfig aggregates everything the downlink consumer needs. The stream
// is provisioned by the deployment (nats-init) — the consumer binds, it does
// not create.
type ConsumerConfig struct {
	Conn    *nats.Conn
	Service *services.Service
	Subject string
	Stream  string
	Durable string
	Queue   string
	Log     logging.Logger
}

// Consumer is the durable queue-group JetStream subscription on the MQTT
// downlink subject.
type Consumer struct {
	cfg ConsumerConfig

	mu     sync.Mutex
	sub    *nats.Subscription
	closed bool
}
