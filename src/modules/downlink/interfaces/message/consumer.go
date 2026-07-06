package message

import (
	"errors"

	"github.com/nats-io/nats.go"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/downlink/application/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// NewConsumer validates the config and constructs the consumer. Errors when
// required deps are nil or names are empty; defaults the logger.
func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	if cfg.Conn == nil {
		return nil, errors.New("nats conn is required")
	}
	if cfg.Service == nil {
		return nil, errors.New("downlink service is required")
	}
	if cfg.Subject == "" || cfg.Stream == "" || cfg.Durable == "" || cfg.Queue == "" {
		return nil, errors.New("subject, stream, durable, and queue are required")
	}
	if cfg.Log == nil {
		cfg.Log = logging.NopLogger{}
	}
	return &Consumer{cfg: cfg}, nil
}

// Start binds the durable queue-group JetStream subscription. WorkQueue
// retention on the stream + explicit acks give at-least-once delivery with
// one broker node handling each command.
func (c *Consumer) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sub != nil {
		return errors.New("downlink consumer already started")
	}
	if c.closed {
		return errors.New("downlink consumer is closed")
	}

	js, err := c.cfg.Conn.JetStream()
	if err != nil {
		return err
	}
	sub, err := js.QueueSubscribe(c.cfg.Subject, c.cfg.Queue, c.handle,
		nats.Durable(c.cfg.Durable),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.BindStream(c.cfg.Stream),
	)
	if err != nil {
		return err
	}
	c.sub = sub
	c.cfg.Log.Info("[CONSUMER:Downlink] started stream=%s subject=%s durable=%s", c.cfg.Stream, c.cfg.Subject, c.cfg.Durable)
	return nil
}

// handle routes one envelope to the service. Malformed envelopes are TERM'd
// (redelivery cannot fix them); everything else is ACK'd — the broker is a
// dumb transport, the Asset MS reconciler owns retries.
func (c *Consumer) handle(msg *nats.Msg) {
	if err := c.cfg.Service.HandleMessage(msg.Data); errors.Is(err, services.ErrMalformedEnvelope) {
		_ = msg.Term()
		return
	}
	_ = msg.Ack()
}

// Stop unsubscribes. Idempotent.
func (c *Consumer) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.sub == nil {
		return nil
	}
	err := c.sub.Unsubscribe()
	c.sub = nil
	return err
}
