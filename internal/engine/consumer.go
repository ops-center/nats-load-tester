package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

type ConsumerConfig struct {
	ID             string
	StreamName     string
	DurableName    string
	Type           string
	AckWaitSeconds int64
	MaxAckPending  int
	ConsumeDelayMs int64
	AckPolicy      string
	UseJetStream   bool
	Subject        string
}

type Consumer struct {
	nc             *nats.Conn
	js             nats.JetStreamContext
	config         ConsumerConfig
	statsCollector statsCollector
	logger         *zap.Logger
	subscription   *nats.Subscription
}

type ConsumerStatsRecorder interface {
	RecordConsume()
	RecordConsumeError(error)
	RecordLatency(time.Duration)
	RecordPendingMessages(int)
}

func NewConsumer(nc *nats.Conn, js nats.JetStreamContext, cfg ConsumerConfig, stats statsCollector, logger *zap.Logger) *Consumer {
	return &Consumer{
		nc:             nc,
		js:             js,
		config:         cfg,
		statsCollector: stats,
		logger:         logger,
	}
}

func (c *Consumer) Start(ctx context.Context) error {
	c.logger.Info("Starting consumer",
		zap.String("id", c.config.ID),
		zap.String("stream", c.config.StreamName),
		zap.String("type", c.config.Type),
	)

	if c.config.UseJetStream && c.js != nil {
		return c.startJetStreamConsumer(ctx)
	}

	return c.startCoreConsumer()
}

func (c *Consumer) startJetStreamConsumer(ctx context.Context) error {
	consumerInfo, err := c.js.ConsumerInfo(c.config.StreamName, c.config.DurableName)
	if err != nil && err != nats.ErrConsumerNotFound {
		return fmt.Errorf("failed to get consumer info: %w", err)
	}

	if consumerInfo == nil {
		ackPolicy := nats.AckExplicitPolicy
		switch c.config.AckPolicy {
		case "none":
			ackPolicy = nats.AckNonePolicy
		case "all":
			ackPolicy = nats.AckAllPolicy
		}

		_, err = c.js.AddConsumer(c.config.StreamName, &nats.ConsumerConfig{
			Durable:       c.config.DurableName,
			AckPolicy:     ackPolicy,
			AckWait:       time.Duration(c.config.AckWaitSeconds) * time.Second,
			MaxAckPending: c.config.MaxAckPending,
			DeliverPolicy: nats.DeliverAllPolicy,
		})
		if err != nil {
			return fmt.Errorf("failed to create consumer: %w", err)
		}
	}

	if c.config.Type == "pull" {
		return c.startPullConsumer(ctx)
	}
	return c.startPushConsumer()
}

func (c *Consumer) startPushConsumer() error {
	msgHandler := func(msg *nats.Msg) {
		c.handleMessage(msg)
	}

	sub, err := c.js.Subscribe(c.config.Subject, msgHandler,
		nats.Durable(c.config.DurableName),
		nats.ManualAck(),
		nats.AckWait(time.Duration(c.config.AckWaitSeconds)*time.Second),
		nats.MaxAckPending(c.config.MaxAckPending),
	)
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	c.subscription = sub
	return nil
}

func (c *Consumer) startPullConsumer(ctx context.Context) error {
	sub, err := c.js.PullSubscribe(c.config.Subject, c.config.DurableName,
		nats.AckWait(time.Duration(c.config.AckWaitSeconds)*time.Second),
		nats.MaxAckPending(c.config.MaxAckPending),
	)
	if err != nil {
		return fmt.Errorf("failed to create pull subscription: %w", err)
	}

	c.subscription = sub

	for {
		select {
		case <-ctx.Done():
			return c.cleanup()
		default:
			msgs, err := sub.Fetch(10, nats.MaxWait(time.Second))
			if err != nil && err != nats.ErrTimeout {
				c.logger.Error("Failed to fetch messages", zap.Error(err))
				c.statsCollector.RecordConsumeError(err)
				continue
			}

			for _, msg := range msgs {
				c.handleMessage(msg)
			}
		}
	}
}

func (c *Consumer) startCoreConsumer() error {
	msgHandler := func(msg *nats.Msg) {
		c.handleMessage(msg)
	}

	sub, err := c.nc.Subscribe(c.config.Subject, msgHandler)
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	c.subscription = sub
	return nil
}

// TODO: make this less shitty
func (c *Consumer) handleMessage(msg *nats.Msg) {
	if c.config.ConsumeDelayMs > 0 {
		time.Sleep(time.Duration(c.config.ConsumeDelayMs) * time.Millisecond)
	}

	if len(msg.Data) >= 8 {
		timestamp := binary.LittleEndian.Uint64(msg.Data[:8])
		latency := time.Since(time.Unix(0, int64(timestamp)))
		c.statsCollector.RecordLatency(latency)
	}

	if c.config.UseJetStream && c.config.AckPolicy != "none" {
		if err := msg.Ack(); err != nil {
			c.logger.Error("Failed to ack message", zap.Error(err))
			c.statsCollector.RecordConsumeError(err)
			return
		}
	}

	c.statsCollector.RecordConsume()
}

func (c *Consumer) cleanup() error {
	if c.subscription != nil {
		// Drain handles both unsubscribe and cleanup
		if err := c.subscription.Drain(); err != nil {
			return fmt.Errorf("failed to drain subscription: %w", err)
		}
	}
	return nil
}
