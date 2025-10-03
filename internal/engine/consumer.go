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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opscenter.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	AckTimeoutSeconds              = 5
	AckRetryInitialDelayMs         = 100
	AckRetryBackoffFactor          = 2.0
	AckRetryMaxAttempts            = 4
	AckRetryMaxDelayMs             = 500
	ConsumerStartRetryDelaySeconds = 1
	ConsumerStartRetryFactor       = 2
	ConsumerStartMaxAttempts       = 5
	ConsumerStartMaxDelaySeconds   = 5
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
	mu             sync.Mutex
	nc             *nats.Conn
	js             jetstream.StreamConsumerManager
	config         ConsumerConfig
	statsCollector statsCollector
	logger         *zap.Logger

	subscription            *nats.Subscription
	jetstreamConsumeContext jetstream.ConsumeContext
	stopped                 atomic.Bool
	circuitBreaker          circuitBreaker
}

func NewConsumer(nc *nats.Conn, js jetstream.StreamConsumerManager, cfg ConsumerConfig, stats statsCollector, logger *zap.Logger, cb circuitBreaker) ConsumerInterface {
	return &Consumer{
		nc:             nc,
		js:             js,
		config:         cfg,
		statsCollector: stats,
		logger:         logger,
		circuitBreaker: cb,
	}
}

func (c *Consumer) Start(ctx context.Context) error {
	if c.config.UseJetStream && c.js != nil {
		return c.startJetStreamConsumer(ctx)
	}

	return c.startCoreConsumer(ctx)
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
		consumerConfig.DeliverSubject = nats.NewInbox()
		var pushConsumer jetstream.PushConsumer
		if err := c.circuitBreaker.call(func() error {
			var err error
			pushConsumer, err = c.js.CreateOrUpdatePushConsumer(ctx, c.config.StreamName, consumerConfig)
			return err
		}); err != nil {
			c.statsCollector.RecordError(err)
			return fmt.Errorf("failed to create push consumer: %w", err)
		}

		consumeContext, err := pushConsumer.Consume(
			func(msg jetstream.Msg) {
				c.handleNatsJetstreamMessage(ctx, msg)
			},
			jetstream.ConsumeErrHandler(func(consumeCtx jetstream.ConsumeContext, err error) {
				c.logger.Error("Push consumer error",
					zap.String("id", c.config.ID),
					zap.String("stream", c.config.StreamName),
					zap.String("subject", c.config.Subject),
					zap.Error(err))
				c.statsCollector.RecordError(err)
			}),
		)
		if err != nil {
			c.statsCollector.RecordError(err)
			return fmt.Errorf("failed to start push consumer: %w", err)
		}
		c.jetstreamConsumeContext = consumeContext
		return nil

	case config.ConsumerTypePull:
		var pullConsumer jetstream.Consumer
		if err := c.circuitBreaker.call(func() error {
			var err error
			pullConsumer, err = c.js.CreateOrUpdateConsumer(ctx, c.config.StreamName, consumerConfig)
			return err
		}); err != nil {
			c.statsCollector.RecordError(err)
			return fmt.Errorf("failed to create pull consumer: %w", err)
		}

		consumeContext, err := pullConsumer.Consume(
			func(msg jetstream.Msg) {
				c.handleNatsJetstreamMessage(ctx, msg)
			},
			jetstream.ConsumeErrHandler(func(consumeCtx jetstream.ConsumeContext, err error) {
				c.logger.Error("Pull consumer error",
					zap.String("id", c.config.ID),
					zap.String("stream", c.config.StreamName),
					zap.String("subject", c.config.Subject),
					zap.Error(err))
				c.statsCollector.RecordError(err)
			}),
		)
		if err != nil {
			c.statsCollector.RecordError(err)
			return fmt.Errorf("failed to start pull consumer: %w", err)
		}
		c.jetstreamConsumeContext = consumeContext
		return nil

	default:
		return fmt.Errorf("invalid consumer type: %s", c.config.Type)
	}
}

func (c *Consumer) startCoreConsumer(ctx context.Context) error {
	msgHandler := func(msg *nats.Msg) {
		c.handleNatsCoreMessage(ctx, msg)
	}

	sub, err := c.nc.Subscribe(c.config.Subject, msgHandler)
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	c.subscription = sub
	return nil
}

func (c *Consumer) handleNatsCoreMessage(ctx context.Context, msg *nats.Msg) {
	if c.stopped.Load() {
		c.logger.Warn("Received nats core message after consumer stopped")
		return
	}

	c.processMessage(ctx, msg.Data)
	c.statsCollector.RecordConsume()
}

func (c *Consumer) handleNatsJetstreamMessage(ctx context.Context, msg jetstream.Msg) {
	if c.stopped.Load() {
		c.logger.Warn("Received nats jetstream message after consumer stopped")
		return
	}

	select {
	case <-ctx.Done():
		return
	default:
	}

	c.processMessage(ctx, msg.Data())

	if c.config.AckPolicy != "none" {
		if err := exponentialBackoff(ctx, AckRetryInitialDelayMs*time.Millisecond, AckRetryBackoffFactor, AckRetryMaxAttempts, AckRetryMaxDelayMs*time.Millisecond, func() error {
			return c.circuitBreaker.call(msg.Ack)
		}); errors.Is(err, errCircuitOpen) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		} else if err != nil {
			c.statsCollector.RecordConsumeError(err)
			if nakErr := msg.Nak(); nakErr != nil {
				c.logger.Error("Failed to NAK message", zap.Error(nakErr))
			}
			return
		}
	}

	c.statsCollector.RecordConsume()
}

func (c *Consumer) processMessage(ctx context.Context, data []byte) {
	if c.config.ConsumeDelayMs > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(c.config.ConsumeDelayMs) * time.Millisecond):
		}
	}

	if c.config.TrackLatency && len(data) >= 8 {
		timestamp := binary.LittleEndian.Uint64(data[:8])
		latency := time.Since(time.Unix(0, int64(timestamp)))
		c.statsCollector.RecordLatency(latency)
	}
}

func (c *Consumer) Cleanup() error {
	if c == nil || !c.stopped.CompareAndSwap(false, true) {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error
	if c.subscription != nil {
		if c.subscription.IsValid() {
			if err := c.subscription.Drain(); err != nil {
				c.logger.Warn("Failed to drain subscription, attempting unsubscribe",
					zap.String("id", c.config.ID),
					zap.Error(err))
				if unsubErr := c.subscription.Unsubscribe(); unsubErr != nil {
					errs = append(errs, fmt.Errorf("failed to unsubscribe: %w", unsubErr))
				}
			}
		}
		c.subscription = nil
	}

	if c.jetstreamConsumeContext != nil {
		c.jetstreamConsumeContext.Stop()
		c.jetstreamConsumeContext = nil
	}

	if c.config.UseJetStream && c.js != nil && c.config.DurableName != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := c.js.DeleteConsumer(ctx, c.config.StreamName, c.config.DurableName); err != nil &&
			!errors.Is(err, jetstream.ErrConsumerNotFound) &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) {
			errs = append(errs, fmt.Errorf("failed to delete consumer: %w", err))
		}
	}

	c.nc = nil
	c.js = nil
	c.statsCollector = nil

	if len(errs) > 0 {
		return errors.Join(errs...)
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
func CreateConsumers(ctx context.Context, nc *nats.Conn, js jetstream.StreamConsumerManager, loadTestSpec *config.LoadTestSpec, statsCollector statsCollector, logger *zap.Logger, eg *errgroup.Group, cb circuitBreaker) ([]ConsumerInterface, error) {
	consumers := make([]ConsumerInterface, 0)
	consumerStartErrGroup, consumerCtx := errgroup.WithContext(ctx)

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

					consumerStartErrGroup.Go(func() error {
						var consumer ConsumerInterface

						if err := exponentialBackoff(consumerCtx, ConsumerStartRetryDelaySeconds*time.Second, ConsumerStartRetryFactor, ConsumerStartMaxAttempts, ConsumerStartMaxDelaySeconds*time.Second, func() error {
							consumer = NewConsumer(nc, js, consCfg, statsCollector, logger, cb)
							if startErr := consumer.Start(consumerCtx); startErr != nil {
								logger.Warn("failed to start consumer, retrying", zap.String("id", consCfg.ID), zap.Error(startErr))
								if err := consumer.Cleanup(); err != nil {
									logger.Error("consumer cleanup failed after start error", zap.String("id", consCfg.ID), zap.Error(err))
									return err
								}
								return startErr
							}
							return nil
						}); errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
							return nil
						} else if err != nil {
							statsCollector.RecordError(err)
							return fmt.Errorf("consumer %s failed: %w", consumer.GetID(), err)
						}

						consumers = append(consumers, consumer)

						eg.Go(func() error {
							<-consumerCtx.Done()
							if consumerErr := consumer.Cleanup(); consumerErr != nil {
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

	logger.Info("All consumers started", zap.Int("count", len(consumers)))

	return consumers, nil
}
