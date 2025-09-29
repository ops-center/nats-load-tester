/*
Copyright AppsCode Inc. and Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opscenter.dev/nats-load-tester/internal/config"
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
	mu                      sync.Mutex
	nc                      *nats.Conn
	js                      jetstream.StreamConsumerManager
	config                  ConsumerConfig
	statsCollector          statsCollector
	logger                  *zap.Logger
	subscription            *nats.Subscription
	jetstreamConsumeContext jetstream.ConsumeContext
}

func NewConsumer(nc *nats.Conn, js jetstream.StreamConsumerManager, cfg ConsumerConfig, stats statsCollector, logger *zap.Logger) ConsumerInterface {
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
	ackPolicy := jetstream.AckExplicitPolicy
	switch c.config.AckPolicy {
	case "none":
		ackPolicy = jetstream.AckNonePolicy
	case "all":
		ackPolicy = jetstream.AckAllPolicy
	}

	consumerConfig := jetstream.ConsumerConfig{
		Durable:       c.config.DurableName,
		AckPolicy:     ackPolicy,
		AckWait:       time.Duration(c.config.AckWaitSeconds) * time.Second,
		MaxAckPending: c.config.MaxAckPending,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		FilterSubject: c.config.Subject,
	}

	switch c.config.Type {
	case config.ConsumerTypePush:
		consumerConfig.DeliverSubject = fmt.Sprintf("_INBOX.%s", c.config.DurableName)
		pushConsumer, err := c.js.CreateOrUpdatePushConsumer(ctx, c.config.StreamName, consumerConfig)
		if err != nil {
			return fmt.Errorf("failed to create consumer: %w", err)
		}
		consumeContext, err := pushConsumer.Consume(c.handleMessage)
		if err != nil {
			return fmt.Errorf("failed to start push consumer: %w", err)
		}
		c.jetstreamConsumeContext = consumeContext
		return nil

	case config.ConsumerTypePull:
		pullConsumer, err := c.js.CreateOrUpdateConsumer(ctx, c.config.StreamName, consumerConfig)
		if err != nil {
			return fmt.Errorf("failed to create consumer: %w", err)
		}
		consumeContext, err := pullConsumer.Consume(c.handleMessage)
		if err != nil {
			return fmt.Errorf("failed to start pull consumer: %w", err)
		}
		c.jetstreamConsumeContext = consumeContext
		return nil

	default:
		return fmt.Errorf("invalid consumer type: %s", c.config.Type)
	}
}

func (c *Consumer) startCoreConsumer() error {
	msgHandler := func(msg *nats.Msg) {
		c.handleNatsMessage(msg)
	}

	sub, err := c.nc.Subscribe(c.config.Subject, msgHandler)
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	c.subscription = sub
	return nil
}

func (c *Consumer) handleNatsMessage(msg *nats.Msg) {
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

func (c *Consumer) handleMessage(msg jetstream.Msg) {
	if c.config.ConsumeDelayMs > 0 {
		time.Sleep(time.Duration(c.config.ConsumeDelayMs) * time.Millisecond)
	}

	msgData := msg.Data()
	// Only attempt to read timestamp if latency tracking is enabled and message is long enough
	if c.config.TrackLatency && len(msgData) >= 8 {
		timestamp := binary.LittleEndian.Uint64(msgData[:8])
		latency := time.Since(time.Unix(0, int64(timestamp)))
		c.statsCollector.RecordLatency(latency)
	}

	if c.config.AckPolicy != "none" {
		if err := msg.Ack(); err != nil {
			c.logger.Error("Failed to ack message", zap.Error(err))
			c.statsCollector.RecordConsumeError(err)
			return
		}
	}

	c.statsCollector.RecordConsume()
}

func (c *Consumer) Cleanup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.subscription != nil {
		// Drain handles both unsubscribe and cleanup
		if err := c.subscription.Drain(); err != nil {
			return fmt.Errorf("failed to drain subscription: %w", err)
		}
		c.subscription = nil
	}
	if c.jetstreamConsumeContext != nil {
		c.jetstreamConsumeContext.Drain()
		c.jetstreamConsumeContext = nil
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
func CreateConsumers(ctx context.Context, nc *nats.Conn, js jetstream.StreamConsumerManager, loadTestSpec *config.LoadTestSpec, statsCollector statsCollector, logger *zap.Logger, eg *errgroup.Group) ([]ConsumerInterface, error) {
	consumers := make([]ConsumerInterface, 0)
	consumerStartErrGroup := &errgroup.Group{}

	for _, streamSpec := range loadTestSpec.Streams {
		// Only create consumers for streams that match the consumer's stream name prefix
		if streamSpec.NamePrefix != loadTestSpec.Consumers.StreamNamePrefix {
			continue
		}

		streamNames := streamSpec.GetFormattedStreamNames()
		for streamIndex, streamName := range streamNames {
			subjects := streamSpec.GetFormattedSubjects(int32(streamIndex + 1))

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
						if err := exponentialBackoff(ctx, 1*time.Second, 2, 5, 5*time.Second, func() error {
							return consumer.Start(ctx)
						}); err != nil {
							statsCollector.RecordError(err)
							logger.Error("consumer failed",
								zap.String("id", consumer.GetID()),
								zap.String("stream", consumer.GetStreamName()),
								zap.String("subject", consumer.GetSubject()),
								zap.Error(err))
							return fmt.Errorf("consumer %s failed: %w", consumer.GetID(), err)
						}

						logger.Info("consumer started",
							zap.String("id", consumer.GetID()),
							zap.String("stream", consumer.GetStreamName()),
							zap.String("subject", consumer.GetSubject()))

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
