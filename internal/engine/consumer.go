package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
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
	TrackLatency   bool
}

type Consumer struct {
	nc             *nats.Conn
	js             nats.JetStreamContext
	config         ConsumerConfig
	statsCollector statsCollector
	logger         *zap.Logger
	subscription   *nats.Subscription
}

func NewConsumer(nc *nats.Conn, js nats.JetStreamContext, cfg ConsumerConfig, stats statsCollector, logger *zap.Logger) ConsumerInterface {
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

		consumerConfig := &nats.ConsumerConfig{
			Durable:       c.config.DurableName,
			AckPolicy:     ackPolicy,
			AckWait:       time.Duration(c.config.AckWaitSeconds) * time.Second,
			MaxAckPending: c.config.MaxAckPending,
			DeliverPolicy: nats.DeliverAllPolicy,
		}

		// For push consumers, we need to specify a DeliverSubject
		if c.config.Type == "push" {
			// Generate a unique delivery subject for this push consumer
			consumerConfig.DeliverSubject = fmt.Sprintf("_INBOX.%s", c.config.DurableName)
		}

		_, err = c.js.AddConsumer(c.config.StreamName, consumerConfig)
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
			return c.Cleanup()
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

func (c *Consumer) handleMessage(msg *nats.Msg) {
	if c.config.ConsumeDelayMs > 0 {
		time.Sleep(time.Duration(c.config.ConsumeDelayMs) * time.Millisecond)
	}

	// Only attempt to read timestamp if latency tracking is enabled and message is long enough
	if c.config.TrackLatency && len(msg.Data) >= 8 {
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

func (c *Consumer) Cleanup() error {
	if c.subscription != nil {
		// Drain handles both unsubscribe and cleanup
		if err := c.subscription.Drain(); err != nil {
			return fmt.Errorf("failed to drain subscription: %w", err)
		}
	}
	return nil
}

// GetID returns the consumer's ID
func (c *Consumer) GetID() string {
	return c.config.ID
}

// GetStreamName returns the consumer's stream name
func (c *Consumer) GetStreamName() string {
	return c.config.StreamName
}

// GetSubject returns the consumer's subject
func (c *Consumer) GetSubject() string {
	return c.config.Subject
}

// CreateConsumers creates and starts consumers based on the load test spec and stream configurations
func CreateConsumers(ctx context.Context, nc *nats.Conn, js nats.JetStreamContext, loadTestSpec *config.LoadTestSpec, statsCollector statsCollector, logger *zap.Logger, eg *errgroup.Group) ([]ConsumerInterface, error) {
	consumers := make([]ConsumerInterface, 0)
	consumerStartErrGroup := &errgroup.Group{}

	for _, loadTestSpecStream := range loadTestSpec.Streams {
		// Only create consumers for streams that match the consumer's stream name prefix
		if loadTestSpecStream.NamePrefix != loadTestSpec.Consumers.StreamNamePrefix {
			continue
		}

		streamNames := loadTestSpecStream.GetFormattedStreamNames()
		for streamIndex, streamName := range streamNames {
			subjects := loadTestSpecStream.GetFormattedSubjects(int32(streamIndex + 1))

			for subjectIndex, subject := range subjects {
				for j := int32(0); j < loadTestSpec.Consumers.CountPerStream; j++ {
					consCfg := ConsumerConfig{
						ID:             fmt.Sprintf("%s-con-%d-%d-%d", loadTestSpec.ClientIDPrefix, streamIndex+1, subjectIndex, j+1),
						StreamName:     streamName,
						DurableName:    fmt.Sprintf("%s_%d_%d_%d", loadTestSpec.Consumers.DurableNamePrefix, streamIndex+1, subjectIndex, j+1),
						Type:           loadTestSpec.Consumers.Type,
						AckWaitSeconds: loadTestSpec.Consumers.AckWaitSeconds,
						MaxAckPending:  int(loadTestSpec.Consumers.MaxAckPending),
						ConsumeDelayMs: loadTestSpec.Consumers.ConsumeDelayMs,
						AckPolicy:      loadTestSpec.Consumers.AckPolicy,
						UseJetStream:   loadTestSpec.UseJetStream,
						Subject:        subject,
						TrackLatency:   loadTestSpec.Publishers.TrackLatency,
					}

					consumer := NewConsumer(nc, js, consCfg, statsCollector, logger)
					consumers = append(consumers, consumer)

					consumerStartErrGroup.Go(func() error {
						if err := consumer.Start(ctx); err != nil {
							statsCollector.RecordError(err)
							logger.Error("consumer failed",
								zap.String("id", consumer.GetID()),
								zap.String("stream", consumer.GetStreamName()),
								zap.String("subject", consumer.GetSubject()),
								zap.Error(err))
							return fmt.Errorf("consumer %s failed: %w", consumer.GetID(), err)
						}
						eg.Go(func() error {
							<-ctx.Done()
							if consumerErr := consumer.Cleanup(); consumerErr != nil {
								logger.Error("consumer cleanup failed",
									zap.String("id", consumer.GetID()),
									zap.String("stream", consumer.GetStreamName()),
									zap.String("subject", consumer.GetSubject()),
									zap.Error(consumerErr))
								return fmt.Errorf("consumer %s cleanup failed: %w", consumer.GetID(), consumerErr)
							}
							return nil
						})
						return nil
					})
				}
			}
		}
	}

	if err := consumerStartErrGroup.Wait(); err != nil {
		logger.Error("one or more consumers failed to start", zap.Error(err))
		return consumers, fmt.Errorf("one or more consumers failed to start: %w", err)
	}

	return consumers, nil
}
