package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.bytebuilders.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Engine struct {
	mu             sync.RWMutex
	logger         *zap.Logger
	statsCollector *stats.Collector
	nc             *nats.Conn
	js             nats.JetStreamContext
	loadTestSpec   *config.LoadTestSpec
	publishers     []*Publisher
	cancel         context.CancelFunc
	eg             *errgroup.Group
}

func NewEngine(logger *zap.Logger, statsCollector *stats.Collector) *Engine {
	return &Engine{
		logger:         logger,
		statsCollector: statsCollector,
		publishers:     make([]*Publisher, 0),
	}
}

func (e *Engine) Start(ctx context.Context, loadTestSpec *config.LoadTestSpec, statsInterval time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.logger.Info("Starting engine with load test spec", zap.String("name", loadTestSpec.Name))
	if err := e.connect(loadTestSpec); err != nil {
		e.statsCollector.WriteFailure(err)
		e.logger.Error("Failed to connect to NATS", zap.Error(err))
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	e.loadTestSpec = loadTestSpec
	if err := e.statsCollector.SetConfig(loadTestSpec); err != nil {
		e.logger.Error("Failed to set stats collector config", zap.Error(err))
		return fmt.Errorf("failed to set stats collector config: %w", err)
	}

	engineCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel

	// Create errgroup with context
	e.eg, engineCtx = errgroup.WithContext(engineCtx)

	// Start stats collector in errgroup
	e.eg.Go(func() error {
		e.statsCollector.Start(engineCtx, statsInterval)
		return nil
	})

	if loadTestSpec.UseJetStream {
		if err := e.setupStreams(loadTestSpec); err != nil {
			e.logger.Error("Failed to setup streams", zap.Error(err))
			e.cleanup()
			return fmt.Errorf("failed to setup streams: %w", err)
		}
	}

	e.startPublishers(engineCtx, loadTestSpec)

	if err := e.startConsumers(engineCtx, loadTestSpec); err != nil {
		e.cleanup()
		e.logger.Error("Failed to start consumers", zap.Error(err))
		return fmt.Errorf("failed to start consumers: %w", err)
	}

	e.eg.Go(func() error {
		return e.startRampUp(engineCtx, loadTestSpec)
	})

	return nil
}

func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.logger.Info("Stopping engine")

	if e.cancel != nil {
		e.cancel()
	}

	var err error
	if e.eg != nil {
		err = e.eg.Wait()
	}

	e.cleanup()

	return err
}

func (e *Engine) connect(loadTestSpec *config.LoadTestSpec) error {
	opts := []nats.Option{
		nats.Name(loadTestSpec.ClientIDPrefix + "-engine"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			e.logger.Warn("Disconnected from NATS", zap.Error(err))
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			e.logger.Info("Reconnected to NATS")
		}),
		nats.ErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
			e.logger.Error("NATS error", zap.Error(err))
			e.statsCollector.RecordError(err)
		}),
	}

	if loadTestSpec.NATSCredsFile != "" {
		opts = append(opts, nats.UserCredentials(loadTestSpec.NATSCredsFile))
	}

	nc, err := nats.Connect(loadTestSpec.NATSURL, opts...)
	if err != nil {
		return err
	}

	e.nc = nc

	if loadTestSpec.UseJetStream {
		js, err := nc.JetStream()
		if err != nil {
			nc.Close()
			return err
		}
		e.js = js
	}

	return nil
}

// for each stream in the load test spec, create or update the stream in JetStream
// based on the configuration in the load test spec.
func (e *Engine) setupStreams(loadTestSpec *config.LoadTestSpec) error {
	for _, loadTestSpecStream := range loadTestSpec.Streams {
		for i := int32(0); i < loadTestSpecStream.Count; i++ {
			streamName := fmt.Sprintf("%s_%d", loadTestSpecStream.NamePrefix, i+1)
			subjects := loadTestSpecStream.GetFormattedSubjects(i + 1)

			streamConfig := &nats.StreamConfig{
				Name:     streamName,
				Subjects: subjects,
				Replicas: int(loadTestSpecStream.Replicas),

				Retention:            loadTestSpecStream.GetRetentionPolicy(),
				MaxAge:               loadTestSpecStream.GetMaxAge(),
				Storage:              loadTestSpecStream.GetStorageType(),
				DiscardNewPerSubject: loadTestSpecStream.GetDiscardNewPerSubject(),
				Discard:              loadTestSpecStream.GetDiscardPolicy(),
				MaxMsgs:              loadTestSpecStream.GetMaxMsgs(),
				MaxBytes:             loadTestSpecStream.GetMaxBytes(),
				MaxMsgsPerSubject:    loadTestSpecStream.GetMaxMsgsPerSubject(),
				MaxConsumers:         loadTestSpecStream.GetMaxConsumers(),
			}

			stream, err := e.js.StreamInfo(streamName)
			if err != nil && err != nats.ErrStreamNotFound {
				return fmt.Errorf("failed to get stream info: %w", err)
			}

			if stream == nil {
				_, err = e.js.AddStream(streamConfig)
				if err != nil {
					return fmt.Errorf("failed to create stream %s: %w", streamName, err)
				}
				e.logger.Info("Created stream", zap.String("name", streamName))
			} else {
				_, err = e.js.UpdateStream(streamConfig)
				if err != nil {
					return fmt.Errorf("failed to update stream %s: %w", streamName, err)
				}
				e.logger.Info("Updated stream", zap.String("name", streamName))
			}
		}
	}

	return nil
}

// startPublishers creates and starts publishers based on the load test spec and stream configurations
// and adds them to the engine's publishers slice.
func (e *Engine) startPublishers(ctx context.Context, loadTestSpec *config.LoadTestSpec) {
	for _, loadTestSpecStream := range loadTestSpec.Streams {
		// Only create publishers for streams that match the publisher's stream name prefix
		if loadTestSpecStream.NamePrefix != loadTestSpec.Publishers.StreamNamePrefix {
			continue
		}

		for i := int32(0); i < loadTestSpecStream.Count; i++ {
			streamName := fmt.Sprintf("%s_%d", loadTestSpecStream.NamePrefix, i+1)

			for j := int32(0); j < loadTestSpec.Publishers.CountPerStream; j++ {
				pubCfg := PublisherConfig{
					ID:               fmt.Sprintf("%s-pub-%d-%d", loadTestSpec.ClientIDPrefix, i+1, j+1),
					StreamName:       streamName,
					Subject:          loadTestSpecStream.FormatSubject(0, i+1),
					MessageSize:      loadTestSpec.Publishers.MessageSizeBytes,
					PublishRate:      loadTestSpec.Publishers.PublishRatePerSecond,
					TrackLatency:     loadTestSpec.Publishers.TrackLatency,
					PublishPattern:   loadTestSpec.Publishers.PublishPattern,
					PublishBurstSize: loadTestSpec.Publishers.PublishBurstSize,
					UseJetStream:     loadTestSpec.UseJetStream,
				}

				pub := NewPublisher(e.nc, e.js, pubCfg, e.statsCollector, e.logger)
				e.publishers = append(e.publishers, pub)

				pubID := pub.config.ID
				publisher := pub

				e.eg.Go(func() error {
					if err := publisher.Start(ctx); err != nil {
						e.logger.Error("Publisher failed", zap.String("id", pubID), zap.Error(err))
						e.statsCollector.RecordError(err)
						return fmt.Errorf("publisher %s failed: %w", pubID, err)
					}
					return nil
				})
			}
		}
	}
}

// startConsumers creates and starts consumers based on the load test spec and stream configurations
// and adds them to the engine's consumers slice.
func (e *Engine) startConsumers(ctx context.Context, loadTestSpec *config.LoadTestSpec) error {
	consumerStartErrGroup := &errgroup.Group{}

	for _, loadTestSpecStream := range loadTestSpec.Streams {
		// Only create consumers for streams that match the consumer's stream name prefix
		if loadTestSpecStream.NamePrefix != loadTestSpec.Consumers.StreamNamePrefix {
			continue
		}

		for i := int32(0); i < loadTestSpecStream.Count; i++ {
			streamName := fmt.Sprintf("%s_%d", loadTestSpecStream.NamePrefix, i+1)

			for j := int32(0); j < loadTestSpec.Consumers.CountPerStream; j++ {
				consCfg := ConsumerConfig{
					ID:             fmt.Sprintf("%s-con-%d-%d", loadTestSpec.ClientIDPrefix, i+1, j+1),
					StreamName:     streamName,
					DurableName:    fmt.Sprintf("%s_%d_%d", loadTestSpec.Consumers.DurableNamePrefix, i+1, j+1),
					Type:           loadTestSpec.Consumers.Type,
					AckWaitSeconds: loadTestSpec.Consumers.AckWaitSeconds,
					MaxAckPending:  int(loadTestSpec.Consumers.MaxAckPending),
					ConsumeDelayMs: loadTestSpec.Consumers.ConsumeDelayMs,
					AckPolicy:      loadTestSpec.Consumers.AckPolicy,
					UseJetStream:   loadTestSpec.UseJetStream,
					Subject:        loadTestSpecStream.FormatSubject(0, i+1),
				}

				cons := NewConsumer(e.nc, e.js, consCfg, e.statsCollector, e.logger)

				consID := cons.config.ID
				consumer := cons

				consumerStartErrGroup.Go(func() error {
					if err := consumer.Start(ctx); err != nil {
						e.statsCollector.RecordError(err)
						e.logger.Error("consumer failed", zap.String("id", consID), zap.Error(err))
						return fmt.Errorf("consumer %s failed: %w", consID, err)
					}
					e.eg.Go(func() error {
						<-ctx.Done()
						if consumerErr := consumer.cleanup(); consumerErr != nil {
							e.logger.Error("consumer cleanup failed", zap.String("id", consID), zap.Error(consumerErr))
							return fmt.Errorf("consumer %s cleanup failed: %w", consID, consumerErr)
						}
						return nil
					})
					return nil
				})
			}
		}
	}

	if err := consumerStartErrGroup.Wait(); err != nil {
		e.logger.Error("one or more consumers failed to start", zap.Error(err))
		return fmt.Errorf("one or more consumers failed to start: %w", err)
	}

	return nil
}

func (e *Engine) startRampUp(ctx context.Context, loadTestSpec *config.LoadTestSpec) error {
	rampUpDuration := loadTestSpec.RampUpDuration()

	e.logger.Info("Starting ramp-up process", zap.Duration("duration", rampUpDuration))
	if rampUpDuration == 0 {
		e.logger.Info("No ramp-up configured, starting at full rate")
		e.setAllPublishersToFullRate()
		e.statsCollector.SetRampUpStatus(false, time.Time{}, 0, 0, 0)
		return nil
	}

	rampUpStart := time.Now()
	rampUpTimer := time.NewTimer(rampUpDuration)
	rampUpTicker := time.NewTicker(time.Second)

	targetRate := int32(0)
	if len(e.publishers) > 0 {
		targetRate = e.publishers[0].GetTargetRate()
	}
	e.statsCollector.SetRampUpStatus(true, rampUpStart, rampUpDuration, 1, targetRate)

	for {
		select {
		case <-ctx.Done():
			rampUpTimer.Stop()
			rampUpTicker.Stop()
			return ctx.Err()

		case <-rampUpTimer.C:
			e.logger.Info("Ramp-up complete")
			e.setAllPublishersToFullRate()
			e.statsCollector.SetRampUpStatus(false, time.Time{}, 0, 0, 0)
			rampUpTimer.Stop()
			rampUpTicker.Stop()

		case <-rampUpTicker.C:
			elapsed := time.Since(rampUpStart)
			progress := min(float64(elapsed)/float64(rampUpDuration), 1.0)
			e.updatePublisherRates(progress)

			currentRate := int32(0)
			targetRate := int32(0)
			for _, pub := range e.publishers {
				targetRate += pub.GetTargetRate()
				currentRate += pub.GetCurrentRate()
			}
			e.statsCollector.SetRampUpStatus(true, rampUpStart, rampUpDuration, currentRate, targetRate)
		}
	}
}

// setAllPublishersToFullRate sets all publishers to their target rate
func (e *Engine) setAllPublishersToFullRate() {
	for _, pub := range e.publishers {
		pub.SetRate(pub.GetTargetRate())
	}
	e.logger.Info("All publishers set to full rate", zap.Int("publishers", len(e.publishers)))
}

// updatePublisherRates updates all publisher rates based on ramp-up progress
func (e *Engine) updatePublisherRates(progress float64) {
	totalCurrentRate, totalTargetRate := int32(0), int32(0)
	for _, pub := range e.publishers {
		targetRate := pub.GetTargetRate()
		// Start at 1 msg/sec and gradually increase to target rate
		currentRate := min(max(int32(1+(float64(targetRate-1)*progress)), 1), targetRate)
		pub.SetRate(currentRate)
		totalCurrentRate += pub.GetCurrentRate()
		totalTargetRate += pub.GetTargetRate()
	}

	e.logger.Debug("Ramp-up progress",
		zap.Float64("progress", progress*100),
		zap.Int32("total_current_rate", totalCurrentRate),
		zap.Int32("total_target_rate", totalTargetRate),
		zap.Int("num_publishers", len(e.publishers)),
	)
}

func (e *Engine) cleanup() {
	if e.cancel != nil {
		e.cancel()
	}
	if e.nc != nil && e.nc.IsConnected() {
		e.nc.Close()
	}

	e.publishers = e.publishers[:0]
}
