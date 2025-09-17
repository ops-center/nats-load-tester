package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.bytebuilders.dev/nats-load-tester/internal/controlplane"
	loadtest_engine "go.bytebuilders.dev/nats-load-tester/internal/engine"
	"go.bytebuilders.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
)

var (
	configFile    string
	port          int
	logLevel      string
	loadTestMutex sync.Mutex
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "nats-load-tester",
		Short: "NATS Load Testing Tool",
		Long:  "A dynamically configurable load testing tool for NATS messaging systems",
		RunE:  run,
	}

	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "Configuration file path")
	rootCmd.PersistentFlags().IntVar(&port, "port", 9481, "HTTP server port")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	logger, err := setupLogger(logLevel)
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer func() {
		if err := logger.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to sync logger: %v\n", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	configChannel := make(chan *config.Config, 100)
	// TODO: Check for existing default config here and send to channel

	httpServer := controlplane.NewHTTPServer(port, logger, configChannel)

	var wg sync.WaitGroup
	wg.Go(func() {
		if err := httpServer.Start(ctx); err != nil {
			logger.Error("http server failed", zap.Error(err))
		}
	})

	wg.Go(func() {
		runLoadTestManager(ctx, httpServer, logger, configChannel)
	})

	<-sigCh
	logger.Info("Shutting down...")
	cancel()
	wg.Wait()

	return nil
}

func runLoadTestManager(ctx context.Context, httpServer *controlplane.HTTPServer, logger *zap.Logger, configChannel <-chan *config.Config) {
	var lastConfigCancel context.CancelFunc = nil

	for {
		select {
		case <-ctx.Done():
			if lastConfigCancel != nil {
				lastConfigCancel()
			}
			return

		case cfg := <-configChannel:
			logger.Info("Received new configuration")

			if lastConfigCancel != nil {
				lastConfigCancel()
			}

			loadTestMutex.Lock()

			// drain the channel and extract the last config
			func() {
				for {
					select {
					case newCfg := <-configChannel:
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

			httpServer.SetConfig(cfg)

			storage, err := createStorage(cfg.Storage, logger)
			if err != nil {
				logger.Error("failed to create storage", zap.Error(err))
				lastConfigCancel()
				loadTestMutex.Unlock()
				break
			}

			statsCollector := stats.NewCollector(logger, storage)
			httpServer.SetCollector(statsCollector)
			engine := loadtest_engine.NewEngine(logger, statsCollector)

			go func(ctx context.Context, cfg *config.Config, engine *loadtest_engine.Engine, storage stats.Storage) {
				defer loadTestMutex.Unlock()

				defer func() {
					if engine != nil {
						if err := engine.Stop(); err != nil {
							logger.Error("engine wait failed", zap.Error(err))
						}
					}
					if storage != nil {
						if err := storage.Close(); err != nil {
							logger.Error("failed to close storage", zap.Error(err))
						}
					}
				}()

				if err := processConfig(ctx, cfg, engine, logger); err != nil {
					logger.Error("failed to process config", zap.Error(err))
				}
			}(cfgCtx, cfg, engine, storage)

		}
	}
}

func processConfig(
	ctx context.Context,
	cfg *config.Config,
	engine *loadtest_engine.Engine,
	logger *zap.Logger,
) error {
	// returns true if no further call should be made. e.g; upon engine failure
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
			logger.Info(specName+" spec completed", zap.String("name", spec.Name))
		}

		if err := engine.Stop(); err != nil {
			logger.Error("engine wait failed", zap.Error(err))
		}
		return false, nil
	}

	for i, loadTestSpec := range cfg.LoadTestSpecs {
		logger.Info("Starting test configuration",
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
			logger.Error("failed to process load test spec", zap.Error(err))
		}
		time.Sleep(5 * time.Second)
	}

	if cfg.RepeatPolicy.Enabled && len(cfg.LoadTestSpecs) > 0 {
		lastLoadTest := cfg.LoadTestSpecs[len(cfg.LoadTestSpecs)-1]
		repeatCount := 1

		for {
			logger.Info("Starting repeat configuration",
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
				logger.Error("failed to process repeat load test spec", zap.Error(err))
			}

			logger.Info("Repeat spec completed",
				zap.Int("iteration", repeatCount),
				zap.String("name", lastLoadTest.Name),
			)

			repeatCount++
			time.Sleep(5 * time.Second)
		}

	}

	return nil
}

func createStorage(cfg config.Storage, logger *zap.Logger) (stats.Storage, error) {
	switch cfg.Type {
	case "badger":
		return stats.NewBadgerStorage(cfg.Path, logger)
	case "file", "":
		if cfg.Path == "" {
			cfg.Path = "./load_test_stats.log"
		}
		return stats.NewFileStorage(cfg.Path, logger)
	case "stdout":
		return stats.NewFileStorage("/dev/stdout", logger)
	default:
		return nil, fmt.Errorf("unknown storage type: %s", cfg.Type)
	}
}

func setupLogger(level string) (*zap.Logger, error) {
	config := zap.NewProductionConfig()

	switch level {
	case "debug":
		config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		config.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		config.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		return nil, fmt.Errorf("invalid log level: %s", level)
	}

	return config.Build()
}
