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
	mu               sync.RWMutex
	logger           *zap.Logger
	statsCollector   *stats.Collector
	nc               *nats.Conn
	js               nats.JetStreamContext
	loadTestSpec     *config.LoadTestSpec
	publishers       []PublisherInterface
	consumers        []ConsumerInterface
	cancel           context.CancelFunc
	eg               *errgroup.Group
	streamManager    StreamManagerInterface
	rampUpController RampUpControllerInterface
}

func NewEngine(logger *zap.Logger, statsCollector *stats.Collector) *Engine {
	return &Engine{
		logger:         logger,
		statsCollector: statsCollector,
		publishers:     make([]PublisherInterface, 0),
		consumers:      make([]ConsumerInterface, 0),
		js:             nil,
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

	e.eg, engineCtx = errgroup.WithContext(engineCtx)

	if loadTestSpec.UseJetStream {
		e.streamManager = NewStreamManager(e.js, e.logger)
	}
	e.rampUpController = NewRampUpManager(e.logger, e.statsCollector)

	// Start stats collector in errgroup
	e.eg.Go(func() error {
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

	e.publishers = CreatePublishers(engineCtx, e.nc, e.js, loadTestSpec, e.statsCollector, e.logger, e.eg)

	var err error
	e.consumers, err = CreateConsumers(engineCtx, e.nc, e.js, loadTestSpec, e.statsCollector, e.logger, e.eg)
	if err != nil {
		e.cleanup()
		e.logger.Error("Failed to start consumers", zap.Error(err))
		return fmt.Errorf("failed to start consumers: %w", err)
	}

	e.eg.Go(func() error {
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

	if e.nc != nil && e.nc.IsConnected() {
		e.nc.Close()
	}

	e.publishers = e.publishers[:0]
	e.consumers = e.consumers[:0]
}
