package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.bytebuilders.dev/nats-load-tester/internal/controlplane"
	loadtest_engine "go.bytebuilders.dev/nats-load-tester/internal/engine"
	"go.bytebuilders.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
)

type Manager struct {
	mutex         sync.Mutex
	httpServer    *controlplane.HTTPServer
	logger        *zap.Logger
	configChannel chan *config.Config
}

func NewManager(port int, logger *zap.Logger) *Manager {
	configChannel := make(chan *config.Config, 100)
	httpServer := controlplane.NewHTTPServer(port, logger, configChannel)

	return &Manager{
		httpServer:    httpServer,
		logger:        logger,
		configChannel: configChannel,
	}
}

func (m *Manager) Start(ctx context.Context) error {
	var wg sync.WaitGroup

	wg.Go(func() {
		if err := m.httpServer.Start(ctx); err != nil {
			m.logger.Error("http server failed", zap.Error(err))
		}
	})

	wg.Go(func() {
		m.run(ctx)
	})

	wg.Wait()
	return nil
}

func (m *Manager) LoadDefaultConfig(configFilePath string) error {
	return loadDefaultConfig(m.configChannel, m.logger, configFilePath)
}

func (m *Manager) run(ctx context.Context) {
	var lastConfigCancel context.CancelFunc = nil

	for {
		select {
		case <-ctx.Done():
			if lastConfigCancel != nil {
				lastConfigCancel()
			}
			return

		case cfg := <-m.configChannel:
			m.logger.Info("Received new configuration")

			if lastConfigCancel != nil {
				lastConfigCancel()
			}

			m.mutex.Lock()

			func() {
				for {
					select {
					case newCfg := <-m.configChannel:
						cfg = newCfg
					case <-time.After(30 * time.Second):
						return
					default:
						return
					}
				}
			}()

			var cfgCtx context.Context
			cfgCtx, lastConfigCancel = context.WithCancel(ctx)

			m.httpServer.SetConfig(cfg)

			storage, err := createStorage(cfg.Storage, m.logger)
			if err != nil {
				m.logger.Error("failed to create storage", zap.Error(err))
				lastConfigCancel()
				m.mutex.Unlock()
				break
			}

			statsCollector := stats.NewCollector(m.logger, storage)
			m.httpServer.SetCollector(statsCollector)
			engine := loadtest_engine.NewEngine(m.logger, statsCollector)

			go func(ctx context.Context, cfg *config.Config, engine *loadtest_engine.Engine, storage stats.Storage) {
				defer m.mutex.Unlock()

				defer func() {
					m.httpServer.SetCollector(nil)
					m.httpServer.SetConfig(nil)
					if engine != nil {
						if err := engine.Stop(); err != nil {
							m.logger.Error("engine wait failed", zap.Error(err))
						}
					}
					if storage != nil {
						if err := storage.Close(); err != nil {
							m.logger.Error("failed to close storage", zap.Error(err))
						}
					}
				}()

				if err := m.processConfig(ctx, cfg, engine); err != nil {
					m.logger.Error("failed to process config", zap.Error(err))
				}
			}(cfgCtx, cfg, engine, storage)
		}
	}
}

func (m *Manager) processConfig(ctx context.Context, cfg *config.Config, engine *loadtest_engine.Engine) error {
	processLoadTestSpec := func(spec *config.LoadTestSpec, specName string) (bool, error) {
		engineCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		if err := engine.Start(engineCtx, spec, cfg.StatsInterval()); err != nil {
			return true, fmt.Errorf("failed to start engine: %w", err)
		}

		select {
		case <-ctx.Done():
			return true, nil
		case <-time.After(spec.Duration()):
			m.logger.Info(specName+" spec completed", zap.String("name", spec.Name))
		}

		if err := engine.Stop(); err != nil {
			m.logger.Error("engine wait failed", zap.Error(err))
		}
		return false, nil
	}

	for i, loadTestSpec := range cfg.LoadTestSpecs {
		m.logger.Info("Starting test configuration",
			zap.Int("index", i),
			zap.String("name", loadTestSpec.Name),
		)

		done, err := processLoadTestSpec(loadTestSpec, "Test")
		if done {
			if err != nil {
				return fmt.Errorf("failed to process load test spec: %w", err)
			}
			return nil
		}
		if err != nil {
			m.logger.Error("failed to process load test spec", zap.Error(err))
		}
		time.Sleep(5 * time.Second)
	}

	if cfg.RepeatPolicy.Enabled && len(cfg.LoadTestSpecs) > 0 {
		lastLoadTest := cfg.LoadTestSpecs[len(cfg.LoadTestSpecs)-1]
		repeatCount := 1

		for {
			select { // Possibly redundant
			case <-ctx.Done():
				m.logger.Info("Context cancelled, stopping repeat configuration")
				return nil
			default:
			}

			m.logger.Info("======================================\n")
			lastLoadTest.ApplyMultipliers(cfg.RepeatPolicy)

			m.logger.Info("Starting repeat configuration",
				zap.Int("iteration", repeatCount),
				zap.String("name", lastLoadTest.Name),
			)

			currentConfig, err := json.MarshalIndent(lastLoadTest, "", "  ")
			if err != nil {
				m.logger.Error("failed to marshal current config", zap.Error(err))
			} else {
				m.logger.Info("Current config: " + string(currentConfig))
			}

			done, err := processLoadTestSpec(lastLoadTest, "Repeat")
			if done {
				if err != nil {
					return fmt.Errorf("failed to process repeat load test spec: %w", err)
				}
				return nil
			}
			if err != nil {
				m.logger.Error("failed to process repeat load test spec", zap.Error(err))
			}

			m.logger.Info("Repeat spec completed",
				zap.Int("iteration", repeatCount),
				zap.String("name", lastLoadTest.Name),
			)

			repeatCount++
		}
	}

	return nil
}
