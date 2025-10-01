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

package loadtest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opscenter.dev/nats-load-tester/internal/config"
	"go.opscenter.dev/nats-load-tester/internal/controlplane"
	loadtest_engine "go.opscenter.dev/nats-load-tester/internal/engine"
	"go.opscenter.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type manager struct {
	mutex         sync.Mutex
	eg            *errgroup.Group
	doneCh        chan error
	httpServer    *controlplane.HTTPServer
	logger        *zap.Logger
	configChannel chan *config.Config
	mode          string
}

func NewManager(port int, mode string, enablePprof bool, logger *zap.Logger) *manager {
	configChannel := make(chan *config.Config, 100)
	httpServer := controlplane.NewHTTPServer(port, enablePprof, logger, configChannel)

	return &manager{
		eg:            nil,
		doneCh:        make(chan error, 1),
		httpServer:    httpServer,
		logger:        logger,
		configChannel: configChannel,
		mode:          mode,
	}
}

func (m *manager) Start(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.eg != nil {
		return fmt.Errorf("manager already started")
	}

	var mgrCtx context.Context
	m.eg, mgrCtx = errgroup.WithContext(ctx)

	m.eg.Go(func() error {
		if err := m.httpServer.Start(mgrCtx); err != nil {
			return fmt.Errorf("http server failed %w", err)
		}
		return nil
	})

	m.eg.Go(func() error {
		m.run(mgrCtx)
		return nil
	})

	go func() {
		err := m.eg.Wait()
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			m.doneCh <- err
			return
		}
		m.doneCh <- nil
	}()

	return nil
}

func (m *manager) DoneCh() <-chan error {
	return m.doneCh
}

func (m *manager) LoadDefaultConfig(configFilePath string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return loadDefaultConfig(m.configChannel, m.logger, configFilePath)
}

func (m *manager) run(ctx context.Context) {
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
					case <-time.After(5 * time.Second):
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
			engine := loadtest_engine.NewEngine(m.logger, statsCollector, m.mode == "both" || m.mode == "publish", m.mode == "both" || m.mode == "consume")

			m.eg.Go(func() error {
				var errs []error
				func(ctx context.Context, cfg *config.Config, engine *loadtest_engine.Engine, storage stats.Storage) {
					defer m.mutex.Unlock()

					defer func() {
						m.httpServer.SetCollector(nil)
						m.httpServer.SetConfig(nil)
						if engine != nil {
							if err := engine.Stop(ctx); err != nil {
								m.logger.Error("engine wait failed", zap.Error(err))
								errs = append(errs, err)
							}
						}
						if storage != nil {
							if err := storage.Close(); err != nil {
								m.logger.Error("failed to close storage", zap.Error(err))
								errs = append(errs, err)
							}
						}
					}()

					if err := m.processConfig(ctx, cfg, engine); err != nil {
						m.logger.Error("failed to process config", zap.Error(err))
						errs = append(errs, err)
					}
				}(cfgCtx, cfg, engine, storage)
				return errors.Join(errs...)
			})
		}
	}
}

func (m *manager) processConfig(ctx context.Context, cfg *config.Config, engine *loadtest_engine.Engine) error {
	processLoadTestSpec := func(spec *config.LoadTestSpec, specName string) (bool, error) {
		engineCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		if err := engine.Start(engineCtx, spec, cfg.StatsInterval()); err != nil {
			return false, fmt.Errorf("failed to start engine: %w", err)
		}

		select {
		case <-ctx.Done():
			return true, nil
		case <-time.After(spec.Duration()):
			m.logger.Info(specName+" spec completed", zap.String("name", spec.Name))
		}

		if err := engine.Stop(ctx); err != nil {
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
	}

	if cfg.RepeatPolicy.Enabled && len(cfg.LoadTestSpecs) > 0 {
		lastLoadTest := cfg.LoadTestSpecs[len(cfg.LoadTestSpecs)-1]
		repeatCount := 1

		for {
			m.logger.Info("========================================================================================")
			lastLoadTest.ApplyMultipliers(cfg.RepeatPolicy)

			m.logger.Info("Starting repeat load spec",
				zap.Int("iteration", repeatCount),
				zap.String("name", lastLoadTest.Name),
			)

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
