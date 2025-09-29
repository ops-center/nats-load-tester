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

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"go.opscenter.dev/nats-load-tester/internal/loadtest"
	"go.uber.org/zap"
)

type cliArgs struct {
	configFilePath   string
	port             int
	logLevel         string
	useDefaultConfig bool
	mode             string
}

func main() {
	args := &cliArgs{}

	rootCmd := &cobra.Command{
		Use:   "nats-load-tester",
		Short: "NATS Load Testing Tool",
		Long:  "A dynamically configurable load testing tool for NATS messaging systems",
		RunE:  func(cmd *cobra.Command, _ []string) error { return run(args) },
	}

	rootCmd.PersistentFlags().StringVar(&args.configFilePath, "config-file-path", "", "Configuration file path")
	rootCmd.PersistentFlags().IntVar(&args.port, "port", 9481, "HTTP server port")
	rootCmd.PersistentFlags().StringVar(&args.logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().BoolVar(&args.useDefaultConfig, "use-default-config", false, "Load default configuration on startup")
	rootCmd.PersistentFlags().StringVar(&args.mode, "mode", "both", "Operational mode (publish, consume, both)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args *cliArgs) error {
	if args == nil {
		return fmt.Errorf("args cannot be nil")
	}

	// Validate mode argument
	validModes := map[string]bool{
		"publish": true,
		"consume": true,
		"both":    true,
	}
	if !validModes[args.mode] {
		return fmt.Errorf("invalid mode '%s', must be one of: publish, consume, both", args.mode)
	}

	logger, err := loadtest.SetupLogger(args.logLevel)
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

	manager := loadtest.NewManager(args.port, args.mode, logger)

	if args.useDefaultConfig {
		if err := manager.LoadDefaultConfig(args.configFilePath); err != nil {
			logger.Warn("Failed to load default config", zap.Error(err))
		}
	}

	go func() {
		if err := manager.Start(ctx); err != nil {
			logger.Error("manager failed", zap.Error(err))
		}
	}()

	<-sigCh
	logger.Info("Shutting down...")
	cancel()

	return nil
}
