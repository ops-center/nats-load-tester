package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.bytebuilders.dev/nats-load-tester/internal/controlplane"
	"go.bytebuilders.dev/nats-load-tester/internal/engine"
	"go.bytebuilders.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
)

var (
	configFile string
	port       int
	logLevel   string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "nats-load-tester",
		Short: "NATS Load Testing Tool",
		Long:  "A dynamically configurable load testing tool for NATS messaging systems",
		RunE:  run,
	}

	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "Configuration file path")
	rootCmd.PersistentFlags().IntVar(&port, "port", 8080, "HTTP server port")
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

	var initialConfig *config.Config
	if configFile != "" {
		cfg, err := loadConfigFile(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config file: %w", err)
		}
		initialConfig = cfg
	}

	httpServer := controlplane.NewHTTPServer(port, logger)

	var wg sync.WaitGroup

	wg.Go(func() {
		if err := httpServer.Start(ctx); err != nil {
			logger.Error("http server failed", zap.Error(err))
		}
	})

	wg.Go(func() {
		runLoadTestManager(ctx, httpServer.ConfigChannel(), initialConfig, logger)
	})

	<-sigCh
	logger.Info("Shutting down...")
	cancel()
	wg.Wait()

	return nil
}

func runLoadTestManager(ctx context.Context, configCh <-chan config.Config, initialConfig *config.Config, logger *zap.Logger) {
	var currentEngine *engine.Engine
	var statsCollector *stats.Collector
	var storage stats.Storage
	var engineCancel context.CancelFunc
	var statsCancelMu sync.Mutex

	defer func() {
		if currentEngine != nil {
			if err := currentEngine.Stop(); err != nil {
				logger.Error("engine wait failed", zap.Error(err))
			}
		}
		if storage != nil {
			if err := storage.Close(); err != nil {
				logger.Error("failed to close storage", zap.Error(err))
			}
		}
	}()

	if initialConfig != nil {
		if err := processConfig(ctx, *initialConfig, &currentEngine, &statsCollector, &storage, &engineCancel, &statsCancelMu, logger); err != nil {
			logger.Error("failed to process initial config", zap.Error(err))
		}
	}

	for {
		select {
		case <-ctx.Done():
			return

		case cfg := <-configCh:
			logger.Info("Received new configuration")

			if currentEngine != nil && currentEngine.IsRunning() {
				logger.Info("Stopping current test")
				if err := currentEngine.Stop(); err != nil {
					logger.Error("engine wait failed", zap.Error(err))
				}
				if engineCancel != nil {
					engineCancel()
				}
			}

			if err := processConfig(ctx, cfg, &currentEngine, &statsCollector, &storage, &engineCancel, &statsCancelMu, logger); err != nil {
				logger.Error("failed to process config", zap.Error(err))
			}
		}
	}
}

func processConfig(ctx context.Context, cfg config.Config, currentEngine **engine.Engine, statsCollector **stats.Collector, storage *stats.Storage, engineCancel *context.CancelFunc, statsCancelMu *sync.Mutex, logger *zap.Logger) error {
	var err error
	*storage, err = createStorage(cfg.Storage)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	*statsCollector = stats.NewCollector(logger, *storage)
	*currentEngine = engine.NewEngine(logger, *statsCollector)

	for i, testCfg := range cfg.Configurations {
		logger.Info("Starting test configuration",
			zap.Int("index", i),
			zap.String("name", testCfg.Name),
		)

		engineCtx, cancel := context.WithCancel(ctx)
		*engineCancel = cancel

		statsCtx, statsCancel := context.WithCancel(ctx)
		statsCancelMu.Lock()
		go func() {
			defer statsCancelMu.Unlock()
			(*statsCollector).Start(statsCtx)
		}()
		statsCancelMu.Unlock()

		if err := (*currentEngine).Start(engineCtx, testCfg); err != nil {
			cancel()
			statsCancel()
			return fmt.Errorf("failed to start engine: %w", err)
		}

		select {
		case <-ctx.Done():
			cancel()
			statsCancel()
			return nil
		case <-time.After(testCfg.Duration()):
			logger.Info("Test configuration completed", zap.String("name", testCfg.Name))
		}

		if err := (*currentEngine).Stop(); err != nil {
			logger.Error("engine wait failed", zap.Error(err))
		}
		cancel()
		statsCancel()
		time.Sleep(5 * time.Second)
	}

	if cfg.RepeatPolicy.Enabled && len(cfg.Configurations) > 0 {
		lastConfig := cfg.Configurations[len(cfg.Configurations)-1]
		repeatCount := 1

		for {
			logger.Info("Starting repeat configuration",
				zap.Int("iteration", repeatCount),
				zap.String("name", lastConfig.Name),
			)

			lastConfig.ApplyMultipliers(cfg.RepeatPolicy)

			engineCtx, cancel := context.WithCancel(ctx)
			*engineCancel = cancel

			statsCtx, statsCancel := context.WithCancel(ctx)
			statsCancelMu.Lock()
			go func() {
				defer statsCancelMu.Unlock()
				(*statsCollector).Start(statsCtx)
			}()
			statsCancelMu.Unlock()

			if err := (*currentEngine).Start(engineCtx, lastConfig); err != nil {
				logger.Error("repeat configuration failed", zap.Error(err))
				(*statsCollector).WriteFailure(err)
				cancel()
				statsCancel()
				break
			}

			select {
			case <-ctx.Done():
				cancel()
				statsCancel()
				return nil
			case <-time.After(lastConfig.Duration()):
				logger.Info("Repeat configuration completed",
					zap.Int("iteration", repeatCount),
					zap.String("name", lastConfig.Name),
				)
			}

			if err := (*currentEngine).Stop(); err != nil {
				logger.Error("engine wait failed", zap.Error(err))
			}
			cancel()
			statsCancel()
			time.Sleep(5 * time.Second)
			repeatCount++
		}
	}

	return nil
}

func createStorage(cfg config.Storage) (stats.Storage, error) {
	switch cfg.Type {
	case "badger":
		return stats.NewBadgerStorage(cfg.Path)
	case "file", "":
		if cfg.Path == "" {
			cfg.Path = "./load_test_stats.log"
		}
		return stats.NewFileStorage(cfg.Path)
	case "stdout":
		return stats.NewFileStorage("/dev/stdout")
	default:
		return nil, fmt.Errorf("unknown storage type: %s", cfg.Type)
	}
}

func loadConfigFile(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
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
