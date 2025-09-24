package loadtest

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.bytebuilders.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
)

func loadDefaultConfig(configChannel chan<- *config.Config, logger *zap.Logger, configFilePath string) error {
	possiblePaths := []string{}
	if configFilePath != "" {
		possiblePaths = append(possiblePaths, configFilePath)
	}

	possiblePaths = append(possiblePaths, []string{
		"/config/config.default.json", // Container path
		"./config.default.json",       // Local development
		"/app/config.default.json",    // Alternative container path
	}...)

	var configPath string
	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			configPath = path
			break
		}
	}

	if configPath == "" {
		return fmt.Errorf("default config file not found in any of the expected locations")
	}

	logger.Info("Loading default config", zap.String("path", configPath))

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read default config file: %w", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse default config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("default config validation failed: %w", err)
	}

	select {
	case configChannel <- &cfg:
		logger.Info("Default config loaded successfully", zap.String("hash", cfg.Hash()))
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout sending default config to channel")
	}

	return nil
}

func createStorage(cfg config.Storage, logger *zap.Logger) (stats.Storage, error) {
	switch cfg.Type {
	case config.BadgerDBStorage:
		return stats.NewBadgerStorage(cfg.Path, logger)
	case config.FileStorage:
		return stats.NewFileStorage(cfg.Path, logger)
	case config.StdoutStorage:
		return stats.NewFileStorage("/dev/stdout", logger)
	default:
		return nil, fmt.Errorf("unknown storage type: %s", cfg.Type)
	}
}

func SetupLogger(level string) (*zap.Logger, error) {
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
