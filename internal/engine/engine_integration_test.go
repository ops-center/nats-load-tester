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
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opscenter.dev/nats-load-tester/internal/config"
	"go.opscenter.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
)

// TODO: sync/disable logs for clarity
// TestNATSStreamConfigurationIntegration tests the complete workflow with all new stream configuration options
func TestNATSStreamConfigurationIntegration(t *testing.T) {
	t.Log("Starting embedded NATS server...")
	natsServer, natsURL := startTestNATSServer(t)
	defer natsServer.Shutdown()
	t.Logf("NATS server started at %s", natsURL)

	t.Log("Creating temporary directory for stats...")
	tempDir, err := os.MkdirTemp("", "nats-load-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp directory: %v", err)
		}
	}()

	t.Log("Setting up logger...")
	logger, err := zap.NewDevelopment()
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer func() {
		if err := logger.Sync(); err != nil {
			t.Logf("Failed to sync logger: %v", err)
		}
	}()

	t.Log("Setting up file storage at: " + tempDir + "test_stats.log")
	statsPath := filepath.Join(tempDir, "test_stats.log")
	fileStorage, err := stats.NewFileStorage(statsPath, logger)
	if err != nil {
		t.Fatalf("Failed to create file storage: %v", err)
	}
	defer func() {
		if err := fileStorage.Close(); err != nil {
			t.Logf("Failed to close file storage: %v", err)
		}
	}()

	t.Log("Creating stats collector...")
	statsCollector := stats.NewCollector(logger, fileStorage)

	t.Log("Creating load test spec...")
	loadTestSpec := createTestLoadTestSpec(natsURL)
	if err := loadTestSpec.Validate(); err != nil {
		t.Fatalf("Load test spec validation failed: %v", err)
	}

	t.Log("Creating engine...")
	engine := NewEngine(logger, statsCollector, true, true)
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(loadTestSpec.Duration()+5*time.Second),
	)
	defer cancel()

	t.Log("\n============================================================================\n")
	err = engine.Start(ctx, loadTestSpec, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to start engine: %v", err)
	}

	go func() {
		select {
		case <-ctx.Done():
			t.Log("Context done, stopping engine...")
			cancel()
		case <-time.After(loadTestSpec.Duration()):
			t.Logf("Load test '%s' completed", loadTestSpec.Name)
		}
	}()

	t.Log("Waiting for publishers to ramp up and generate messages...")
	time.Sleep(12 * time.Second)

	t.Log("Verifying stream configuration...")
	err = verifyStreamConfiguration(t, natsURL, loadTestSpec)
	if err != nil {
		t.Errorf("Stream verification failed: %v", err)
	}

	t.Log("Verifying workflow (publishers and consumers)...")
	err = verifyWorkflow(t, natsURL, loadTestSpec)
	if err != nil {
		t.Errorf("Workflow verification failed: %v", err)
	}

	t.Log("Waiting for engine to complete...")
	if err = engine.errGroup.Wait(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Engine encountered an error: %v", err)
	}

	// Stop the engine
	err = engine.Stop(ctx)
	if err != nil {
		t.Errorf("Failed to stop engine: %v", err)
	}

	// Verify stats were collected
	err = verifyStatsCollection(t, statsPath)
	if err != nil {
		t.Errorf("Stats verification failed: %v", err)
	}

	t.Log("Integration test completed successfully")
}

// startTestNATSServer starts an embedded NATS server with JetStream enabled
func startTestNATSServer(t *testing.T) (*server.Server, string) {
	tempDir, err := os.MkdirTemp("", "nats-js-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory for NATS: %v", err)
	}

	opts := &server.Options{
		Port:      -1, // Random port
		Host:      "127.0.0.1",
		NoLog:     true,
		NoSigs:    true,
		JetStream: true,
		StoreDir:  tempDir,
	}

	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("Failed to create NATS server: %v", err)
	}

	go s.Start()

	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatalf("NATS server failed to start")
	}

	natsURL := fmt.Sprintf("nats://%s:%d", opts.Host, s.Addr().(*net.TCPAddr).Port)
	t.Cleanup(func() {
		s.Shutdown()
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove NATS temp directory: %v", err)
		}
	})

	return s, natsURL
}

// createTestLoadTestSpec creates a minimal load test spec for integration testing
func createTestLoadTestSpec(natsURL string) *config.LoadTestSpec {
	// Helper function to get pointer values for optional fields
	boolPtr := func(b bool) *bool { return &b }
	int64Ptr := func(i int64) *int64 { return &i }
	intPtr := func(i int) *int { return &i }

	return &config.LoadTestSpec{
		Name:           "integration-test",
		NATSURL:        natsURL,
		UseJetStream:   true,
		ClientIDPrefix: "integration-test",
		Streams: []config.StreamSpec{
			{
				NamePrefix:                 "test_stream",
				Count:                      2,
				Replicas:                   1,
				Subjects:                   []string{"test.stream.{}.subject", "test.stream.{}.other"},
				MessagesPerStreamPerSecond: 10,
				// Test all new configuration options
				Retention:            "limits",
				MaxAge:               "2m",
				Storage:              "memory",
				DiscardNewPerSubject: boolPtr(false),
				Discard:              "old",
				MaxMsgs:              int64Ptr(1000),
				MaxBytes:             int64Ptr(1048576), // 1MB
				MaxMsgsPerSubject:    int64Ptr(500),
				MaxConsumers:         intPtr(10),
			},
		},
		Publishers: config.PublisherConfig{
			StreamNamePrefix:     "test_stream",
			CountPerStream:       2,
			PublishRatePerSecond: 5,
			PublishPattern:       "steady",
			PublishBurstSize:     1,
			MessageSizeBytes:     256,
			TrackLatency:         true,
		},
		Consumers: config.ConsumerConfig{
			StreamNamePrefix:  "test_stream",
			Type:              "push",
			CountPerStream:    2,
			DurableNamePrefix: "test_consumer",
			AckWaitSeconds:    10,
			MaxAckPending:     100,
			ConsumeDelayMs:    50,
			AckPolicy:         "explicit",
		},
		Behavior: config.BehaviorConfig{
			DurationSeconds: 30,
			RampUpSeconds:   10,
		},
		LogLimits: config.LogLimits{
			MaxLines: 100,
			MaxBytes: 32768,
		},
	}
}

// verifyStreamConfiguration connects to NATS and verifies streams were created with correct configuration
func verifyStreamConfiguration(t *testing.T, natsURL string, loadTestSpec *config.LoadTestSpec) error {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	// Verify each stream exists and has correct configuration
	for _, streamSpec := range loadTestSpec.Streams {
		for i := int32(0); i < streamSpec.Count; i++ {
			streamName := fmt.Sprintf("%s_%d", streamSpec.NamePrefix, i+1)

			stream, err := js.Stream(context.Background(), streamName)
			if err != nil {
				return fmt.Errorf("stream %s not found: %w", streamName, err)
			}

			streamInfo, err := stream.Info(context.Background())
			if err != nil {
				return fmt.Errorf("failed to get stream info for %s: %w", streamName, err)
			}

			cfg := streamInfo.Config

			// Verify new configuration options
			if cfg.MaxMsgs != streamSpec.GetMaxMsgs() {
				t.Errorf("Stream %s: MaxMsgs mismatch: expected %d, got %d",
					streamName, streamSpec.GetMaxMsgs(), cfg.MaxMsgs)
			}

			if cfg.MaxBytes != streamSpec.GetMaxBytes() {
				t.Errorf("Stream %s: MaxBytes mismatch: expected %d, got %d",
					streamName, streamSpec.GetMaxBytes(), cfg.MaxBytes)
			}

			if cfg.MaxMsgsPerSubject != streamSpec.GetMaxMsgsPerSubject() {
				t.Errorf("Stream %s: MaxMsgsPerSubject mismatch: expected %d, got %d",
					streamName, streamSpec.GetMaxMsgsPerSubject(), cfg.MaxMsgsPerSubject)
			}

			if cfg.MaxConsumers != streamSpec.GetMaxConsumers() {
				t.Errorf("Stream %s: MaxConsumers mismatch: expected %d, got %d",
					streamName, streamSpec.GetMaxConsumers(), cfg.MaxConsumers)
			}

			// Verify existing configuration options still work
			if cfg.Retention != streamSpec.GetRetentionPolicy() {
				t.Errorf("Stream %s: Retention mismatch: expected %v, got %v",
					streamName, streamSpec.GetRetentionPolicy(), cfg.Retention)
			}

			if cfg.MaxAge != streamSpec.GetMaxAge() {
				t.Errorf("Stream %s: MaxAge mismatch: expected %v, got %v",
					streamName, streamSpec.GetMaxAge(), cfg.MaxAge)
			}

			if cfg.Storage != streamSpec.GetStorageType() {
				t.Errorf("Stream %s: Storage mismatch: expected %v, got %v",
					streamName, streamSpec.GetStorageType(), cfg.Storage)
			}

			t.Logf("Stream %s configuration verified successfully", streamName)
		}
	}

	return nil
}

// verifyWorkflow verifies that publishers and consumers are working by checking message flow
func verifyWorkflow(t *testing.T, natsURL string, loadTestSpec *config.LoadTestSpec) error {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	// Wait a bit more for messages to be published and consumed
	time.Sleep(3 * time.Second)

	// Verify each stream has messages
	totalMessages := int64(0)
	for _, streamSpec := range loadTestSpec.Streams {
		for i := int32(0); i < streamSpec.Count; i++ {
			streamName := fmt.Sprintf("%s_%d", streamSpec.NamePrefix, i+1)

			stream, err := js.Stream(context.Background(), streamName)
			if err != nil {
				return fmt.Errorf("failed to get stream for %s: %w", streamName, err)
			}

			streamInfo, err := stream.Info(context.Background())
			if err != nil {
				return fmt.Errorf("failed to get stream info for %s: %w", streamName, err)
			}

			state := streamInfo.State
			totalMessages += int64(state.Msgs)

			t.Logf("Stream %s: Messages=%d, Bytes=%d, FirstSeq=%d, LastSeq=%d",
				streamName, state.Msgs, state.Bytes, state.FirstSeq, state.LastSeq)
		}
	}

	// We expect some messages to have been published
	if totalMessages == 0 {
		return fmt.Errorf("no messages found in streams - publishers may not be working")
	}

	t.Logf("Workflow verification successful: %d total messages across all streams", totalMessages)
	return nil
}

// verifyStatsCollection verifies that the stats file is non-empty
func verifyStatsCollection(t *testing.T, statsPath string) error {
	// Check if stats file exists
	if _, err := os.Stat(statsPath); os.IsNotExist(err) {
		return fmt.Errorf("stats file not created: %s", statsPath)
	}

	// Read stats file content
	content, err := os.ReadFile(statsPath)
	if err != nil {
		return fmt.Errorf("failed to read stats file: %w", err)
	}

	if len(content) == 0 {
		return fmt.Errorf("stats file is empty")
	}

	t.Logf("Stats collection verified: file size = %d bytes", len(content))
	return nil
}
