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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opscenter.dev/nats-load-tester/internal/config"
	"go.opscenter.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	engineCleanupTimeout = 5 * time.Minute
)

type Engine struct {
	mu       sync.RWMutex
	stopped  atomic.Bool
	errGroup *errgroup.Group
	cancel   context.CancelFunc

	logger         *zap.Logger
	statsCollector *stats.Collector

	natsConn             *nats.Conn
	natsJetStreamContext jetstream.JetStream
	streamManager        StreamManagerInterface

	loadTestSpec     *config.LoadTestSpec
	rampUpController RampUpControllerInterface

	enablePublishers      bool
	publishCircuitBreaker circuitBreaker
	publishers            []PublisherInterface

	enableConsumers       bool
	consumeCircuitBreaker circuitBreaker
	consumers             []ConsumerInterface
}

func NewEngine(logger *zap.Logger, statsCollector *stats.Collector, enablePublishers, enableConsumers bool) *Engine {
	return &Engine{
		logger: logger,
		stopped: *func() *atomic.Bool {
			b := atomic.Bool{}
			b.Store(true)
			return &b
		}(),
		statsCollector:        statsCollector,
		enablePublishers:      enablePublishers,
		enableConsumers:       enableConsumers,
		publishers:            make([]PublisherInterface, 0),
		consumers:             make([]ConsumerInterface, 0),
		natsJetStreamContext:  nil,
		publishCircuitBreaker: newCircuitBreaker("publisher", 5, 15*time.Second, logger),
		consumeCircuitBreaker: newCircuitBreaker("consumer", 5, 15*time.Second, logger),
	}
}

func (e *Engine) Start(ctx context.Context, loadTestSpec *config.LoadTestSpec, statsInterval time.Duration) error {
	if !e.stopped.CompareAndSwap(true, false) {
		e.logger.Warn("Start() called, but engine is already running")
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	e.logger.Info("Starting engine with load test spec", zap.String("name", loadTestSpec.Name))
	if err := e.connect(loadTestSpec); err != nil {
		e.statsCollector.WriteFailure(err)
		e.logger.Error("Failed to connect to NATS", zap.Error(err))
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	e.logger.Info("Connected to NATS", zap.String("url", e.natsConn.ConnectedUrl()), zap.String("client_id", e.natsConn.Opts.Name))

	e.logger.Info("Setting up collector with load test spec", zap.String("name", loadTestSpec.Name))
	e.loadTestSpec = loadTestSpec
	if err := e.statsCollector.SetConfig(loadTestSpec); err != nil {
		e.logger.Error("Failed to set stats collector config", zap.Error(err))
		return fmt.Errorf("failed to set stats collector config: %w", err)
	}

	engineCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel

	e.errGroup, engineCtx = errgroup.WithContext(engineCtx)

	if loadTestSpec.UseJetStream {
		e.logger.Info("Setting up JetStream stream manager")
		e.streamManager = NewStreamManager(e.natsJetStreamContext, e.logger)
	}

	e.logger.Info("Setting up ramp-up controller")
	e.rampUpController = NewRampUpManager(e.logger, e.statsCollector)

	e.logger.Info("Starting stats collector")
	e.errGroup.Go(func() error {
		e.statsCollector.Start(engineCtx, statsInterval)
		return nil
	})

	if loadTestSpec.UseJetStream {
		e.logger.Info("Setting up streams for load test spec", zap.String("name", loadTestSpec.Name))
		if err := e.streamManager.SetupStreams(engineCtx, loadTestSpec); err != nil {
			e.logger.Error("Failed to setup streams", zap.Error(err))
			e.cleanup(engineCleanupTimeout)
			return fmt.Errorf("failed to setup streams: %w", err)
		}
	}

	var err error
	if e.enableConsumers {
		e.logger.Info("Creating consumers for load test spec", zap.String("name", loadTestSpec.Name))
		e.consumers, err = CreateConsumers(engineCtx, e.natsConn, e.natsJetStreamContext, loadTestSpec, e.statsCollector, e.logger, e.errGroup, e.consumeCircuitBreaker)
		if err != nil {
			e.cleanup(engineCleanupTimeout)
			e.logger.Error("Failed to start consumers", zap.Error(err))
			return fmt.Errorf("failed to start consumers: %w", err)
		}
	}

	if e.enablePublishers {
		e.logger.Info("Creating publishers for load test spec", zap.String("name", loadTestSpec.Name))
		e.publishers = CreatePublishers(engineCtx, e.natsConn, e.natsJetStreamContext, loadTestSpec, e.statsCollector, e.logger, e.errGroup, e.publishCircuitBreaker)
	}

	e.errGroup.Go(func() error {
		e.logger.Info("Starting ramp-up process", zap.Duration("duration", loadTestSpec.Duration()))
		return e.rampUpController.Start(engineCtx, e.publishers, loadTestSpec.RampUpDuration())
	})

	return nil
}

func (e *Engine) Stop(ctx context.Context) error {
	if e == nil || !e.stopped.CompareAndSwap(false, true) {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	e.logger.Info("Stopping engine")

	if e.cancel != nil {
		e.cancel()
	}

	var err error
	if e.errGroup != nil {
		err = e.errGroup.Wait()
	}

	e.cleanup(engineCleanupTimeout)

	return err
}

func (e *Engine) connect(loadTestSpec *config.LoadTestSpec) error {
	opts := []nats.Option{
		nats.Name(loadTestSpec.ClientIDPrefix + "-engine"),
		nats.RetryOnFailedConnect(true),
		nats.ReconnectBufSize(100 * 1024 * 1024), // 100MB
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

	e.natsConn = nc

	if loadTestSpec.UseJetStream {
		js, err := jetstream.New(
			e.natsConn,
			jetstream.WithPublishAsyncMaxPending(16384),
		)
		if err != nil {
			nc.Close()
			return err
		}
		e.natsJetStreamContext = js
	}

	return nil
}

func (e *Engine) cleanup(cleanupTimeout time.Duration) {
	if e.cancel != nil {
		e.cancel()
	}
	// create a new context here as cleanup is usually called when the overlying context is cancelled
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	e.logger.Info("Cleaning up publishers")
	for _, publisher := range e.publishers {
		publisher.Cleanup()
	}

	e.logger.Info("Cleaning up consumers")
	for _, consumer := range e.consumers {
		if err := consumer.Cleanup(); err != nil {
			e.logger.Error("Consumer cleanup failed",
				zap.String("id", consumer.GetID()),
				zap.String("stream", consumer.GetStreamName()),
				zap.String("subject", consumer.GetSubject()),
				zap.Error(err))
		}
	}

	e.logger.Info("Cleaning up streams")
	if e.loadTestSpec != nil && e.loadTestSpec.UseJetStream && e.streamManager != nil {
		if err := exponentialBackoff(cleanupCtx, 1*time.Second, 1.5, 5, 5*time.Second, func() error {
			return e.streamManager.CleanupStreams(cleanupCtx, e.loadTestSpec)
		}); err != nil {
			e.logger.Error("Stream cleanup failed - streams may remain in NATS",
				zap.Error(err),
				zap.String("test_name", e.loadTestSpec.Name))
			e.statsCollector.RecordError(fmt.Errorf("stream cleanup failed: %w", err))
		}
	}

	if e.natsConn != nil {
		if e.natsConn.IsConnected() {
			// Drain connection to allow pending messages to complete
			if err := e.natsConn.Drain(); err != nil {
				e.logger.Warn("Failed to drain NATS connection", zap.Error(err))
			}
		}
		e.natsConn.Close()
	}

	e.publishCircuitBreaker.forceReset()
	e.consumeCircuitBreaker.forceReset()
	e.natsJetStreamContext = nil
	e.natsConn = nil
	e.loadTestSpec = nil
	e.streamManager = nil
	e.rampUpController = nil
	e.errGroup = nil
	e.publishers = nil
	e.consumers = nil
}
