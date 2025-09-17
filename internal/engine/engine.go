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
	currentConfig  *config.LoadTestConfig
	publishers     []*Publisher
	consumers      []*Consumer
	cancel         context.CancelFunc
	eg             *errgroup.Group
	running        bool
}

func NewEngine(logger *zap.Logger, statsCollector *stats.Collector) *Engine {
	return &Engine{
		logger:         logger,
		statsCollector: statsCollector,
		publishers:     make([]*Publisher, 0),
		consumers:      make([]*Consumer, 0),
	}
}

func (e *Engine) Start(ctx context.Context, cfg config.LoadTestConfig) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return fmt.Errorf("engine already running")
	}

	e.logger.Info("Starting engine with configuration", zap.String("name", cfg.Name))

	if err := e.connect(cfg); err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	e.currentConfig = &cfg
	e.statsCollector.SetConfig(cfg)

	engineCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.running = true

	// Create errgroup with context
	e.eg, engineCtx = errgroup.WithContext(engineCtx)

	if cfg.UseJetStream {
		if err := e.setupStreams(cfg); err != nil {
			e.cleanup()
			return fmt.Errorf("failed to setup streams: %w", err)
		}
	}

	if err := e.startPublishers(engineCtx, cfg); err != nil {
		e.cleanup()
		return fmt.Errorf("failed to start publishers: %w", err)
	}

	if err := e.startConsumers(engineCtx, cfg); err != nil {
		e.cleanup()
		return fmt.Errorf("failed to start consumers: %w", err)
	}

	e.eg.Go(func() error {
		return e.runTest(engineCtx, cfg)
	})

	return nil
}

func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return nil
	}

	e.logger.Info("Stopping engine")

	if e.cancel != nil {
		e.cancel()
	}

	// Wait for all goroutines and collect any errors
	var err error
	if e.eg != nil {
		err = e.eg.Wait()
	}

	e.cleanup()
	e.running = false

	return err
}

func (e *Engine) IsRunning() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.running
}

func (e *Engine) connect(cfg config.LoadTestConfig) error {
	opts := []nats.Option{
		nats.Name(cfg.ClientIDPrefix + "-engine"),
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

	if cfg.NATSCredsFile != "" {
		opts = append(opts, nats.UserCredentials(cfg.NATSCredsFile))
	}

	nc, err := nats.Connect(cfg.NATSURL, opts...)
	if err != nil {
		return err
	}

	e.nc = nc

	if cfg.UseJetStream {
		js, err := nc.JetStream()
		if err != nil {
			nc.Close()
			return err
		}
		e.js = js
	}

	return nil
}

func (e *Engine) setupStreams(cfg config.LoadTestConfig) error {
	for _, streamCfg := range cfg.Streams {
		for i := 0; i < streamCfg.Count; i++ {
			streamName := fmt.Sprintf("%s_%d", streamCfg.NamePrefix, i+1)

			subjects := make([]string, len(streamCfg.Subjects))
			for j, subject := range streamCfg.Subjects {
				subjects[j] = fmt.Sprintf(subject, i+1)
			}

			streamConfig := &nats.StreamConfig{
				Name:     streamName,
				Subjects: subjects,
				Replicas: streamCfg.Replicas,
				Storage:  nats.FileStorage,
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

// TODO: Fix the error handling
func (e *Engine) startPublishers(ctx context.Context, cfg config.LoadTestConfig) error {
	for _, streamCfg := range cfg.Streams {
		for i := 0; i < streamCfg.Count; i++ {
			streamName := fmt.Sprintf("%s_%d", streamCfg.NamePrefix, i+1)

			for j := 0; j < cfg.Publishers.CountPerStream; j++ {
				pubCfg := PublisherConfig{
					ID:             fmt.Sprintf("%s-pub-%d-%d", cfg.ClientIDPrefix, i+1, j+1),
					StreamName:     streamName,
					Subject:        fmt.Sprintf(streamCfg.Subjects[0], i+1),
					MessageSize:    cfg.Publishers.MessageSizeBytes,
					PublishRate:    cfg.Publishers.PublishRatePerSecond,
					TrackLatency:   cfg.Publishers.TrackLatency,
					PublishPattern: streamCfg.PublishPattern,
					UseJetStream:   cfg.UseJetStream,
				}

				pub := NewPublisher(e.nc, e.js, pubCfg, e.statsCollector, e.logger)
				e.publishers = append(e.publishers, pub)

				// Capture variables for closure
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

	return nil
}

// TODO: Fix the error handling
func (e *Engine) startConsumers(ctx context.Context, cfg config.LoadTestConfig) error {
	for _, streamCfg := range cfg.Streams {
		for i := 0; i < streamCfg.Count; i++ {
			streamName := fmt.Sprintf("%s_%d", streamCfg.NamePrefix, i+1)

			for j := 0; j < cfg.Consumers.CountPerStream; j++ {
				consCfg := ConsumerConfig{
					ID:             fmt.Sprintf("%s-con-%d-%d", cfg.ClientIDPrefix, i+1, j+1),
					StreamName:     streamName,
					DurableName:    fmt.Sprintf("%s_%d_%d", cfg.Consumers.DurableNamePrefix, i+1, j+1),
					Type:           cfg.Consumers.Type,
					AckWaitSeconds: cfg.Consumers.AckWaitSeconds,
					MaxAckPending:  cfg.Consumers.MaxAckPending,
					ConsumeDelayMs: cfg.Consumers.ConsumeDelayMs,
					AckPolicy:      cfg.Consumers.AckPolicy,
					UseJetStream:   cfg.UseJetStream,
					Subject:        fmt.Sprintf(streamCfg.Subjects[0], i+1),
				}

				cons := NewConsumer(e.nc, e.js, consCfg, e.statsCollector, e.logger)
				e.consumers = append(e.consumers, cons)

				// Capture variables for closure
				consID := cons.config.ID
				consumer := cons

				e.eg.Go(func() error {
					if err := consumer.Start(ctx); err != nil {
						e.logger.Error("Consumer failed", zap.String("id", consID), zap.Error(err))
						e.statsCollector.RecordError(err)
						return fmt.Errorf("consumer %s failed: %w", consID, err)
					}
					return nil
				})
			}
		}
	}

	return nil
}

func (e *Engine) runTest(ctx context.Context, cfg config.LoadTestConfig) error {
	testTimer := time.NewTimer(cfg.Duration())
	defer testTimer.Stop()

	checkpointTicker := time.NewTicker(cfg.CheckpointInterval())
	defer checkpointTicker.Stop()

	rampUpComplete := false
	rampUpTimer := time.NewTimer(cfg.RampUpDuration())
	if cfg.RampUpDuration() == 0 {
		rampUpComplete = true
		rampUpTimer.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-testTimer.C:
			e.logger.Info("Test duration complete")
			return nil

		case <-rampUpTimer.C:
			if !rampUpComplete {
				e.logger.Info("Ramp-up complete")
				rampUpComplete = true
				rampUpTimer.Stop()
			}

		case <-checkpointTicker.C:
			e.logger.Debug("Checkpoint")
		}
	}
}

func (e *Engine) cleanup() {
	if e.nc != nil && e.nc.IsConnected() {
		e.nc.Close()
	}

	e.publishers = e.publishers[:0]
	e.consumers = e.consumers[:0]
}
