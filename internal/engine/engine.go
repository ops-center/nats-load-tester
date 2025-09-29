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
	"time"

	"github.com/nats-io/nats.go"
	"go.opscenter.dev/nats-load-tester/internal/config"
	"go.opscenter.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Engine struct {
	mu                   sync.RWMutex
	logger               *zap.Logger
	enablePublishers     bool
	enableConsumers      bool
	statsCollector       *stats.Collector
	natsConn             *nats.Conn
	natsJetStreamContext nats.JetStreamContext
	loadTestSpec         *config.LoadTestSpec
	publishers           []PublisherInterface
	consumers            []ConsumerInterface
	cancel               context.CancelFunc
	errGroup             *errgroup.Group
	streamManager        StreamManagerInterface
	rampUpController     RampUpControllerInterface
}

func NewEngine(logger *zap.Logger, statsCollector *stats.Collector, enablePublishers, enableConsumers bool) *Engine {
	return &Engine{
		logger:               logger,
		statsCollector:       statsCollector,
		enablePublishers:     enablePublishers,
		enableConsumers:      enableConsumers,
		publishers:           make([]PublisherInterface, 0),
		consumers:            make([]ConsumerInterface, 0),
		natsJetStreamContext: nil,
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

	e.errGroup, engineCtx = errgroup.WithContext(engineCtx)

	if loadTestSpec.UseJetStream {
		e.streamManager = NewStreamManager(e.natsJetStreamContext, e.logger)
	}
	e.rampUpController = NewRampUpManager(e.logger, e.statsCollector)

	// Start stats collector in errgroup
	e.errGroup.Go(func() error {
		e.statsCollector.Start(engineCtx, statsInterval)
		return nil
	})

	if loadTestSpec.UseJetStream {
		if err := e.streamManager.SetupStreams(engineCtx, loadTestSpec); err != nil {
			e.logger.Error("Failed to setup streams", zap.Error(err))
			e.cleanup()
			return fmt.Errorf("failed to setup streams: %w", err)
		}
	}

	if e.enablePublishers {
		e.publishers = CreatePublishers(engineCtx, e.natsConn, e.natsJetStreamContext, loadTestSpec, e.statsCollector, e.logger, e.errGroup)
	}

	var err error
	if e.enableConsumers {
		e.consumers, err = CreateConsumers(engineCtx, e.natsConn, e.natsJetStreamContext, loadTestSpec, e.statsCollector, e.logger, e.errGroup)
		if err != nil {
			e.cleanup()
			e.logger.Error("Failed to start consumers", zap.Error(err))
			return fmt.Errorf("failed to start consumers: %w", err)
		}
	}

	e.errGroup.Go(func() error {
		return e.rampUpController.Start(engineCtx, e.publishers, loadTestSpec.RampUpDuration())
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
	if e.errGroup != nil {
		err = e.errGroup.Wait()
	}

	e.cleanup()

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

	/// TODO: migrate to the newer "github.com/nats-io/nats.go/jetstream" api
	if loadTestSpec.UseJetStream {
		js, err := nc.JetStream()
		if err != nil {
			nc.Close()
			return err
		}
		e.natsJetStreamContext = js
	}

	return nil
}

func (e *Engine) cleanup() {
	if e.cancel != nil {
		e.cancel()
	}

	// Clean up consumers
	for _, consumer := range e.consumers {
		if err := consumer.Cleanup(); err != nil {
			e.logger.Error("Consumer cleanup failed",
				zap.String("id", consumer.GetID()),
				zap.String("stream", consumer.GetStreamName()),
				zap.String("subject", consumer.GetSubject()),
				zap.Error(err))
		}
	}

	// Clean up streams if JetStream is enabled
	if e.loadTestSpec != nil && e.loadTestSpec.UseJetStream && e.streamManager != nil {
		if err := e.streamManager.CleanupStreams(e.loadTestSpec); err != nil {
			e.logger.Error("Stream cleanup failed", zap.Error(err))
		}
	}

	if e.natsConn != nil && e.natsConn.IsConnected() {
		e.natsConn.Close()
	}

	e.natsJetStreamContext = nil
	e.natsConn = nil
	e.loadTestSpec = nil
	e.streamManager = nil
	e.publishers = e.publishers[:0]
	e.consumers = e.consumers[:0]
}
