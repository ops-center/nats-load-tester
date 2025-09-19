package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
)

type FileStorage struct {
	mu       sync.Mutex
	file     *os.File
	filepath string
}

func NewFileStorage(filepath string) (*FileStorage, error) {
	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	return &FileStorage{
		file:     file,
		filepath: filepath,
	}, nil
}

func (f *FileStorage) WriteConfigInfo(cfg *config.Config, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	configJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	output := fmt.Sprintf(`--------------------------------------------------------------------------------
Configuration Info: %s
Hash: %s
Config: %s
--------------------------------------------------------------------------------

`, time.Now().Format(time.RFC3339), hash, string(configJSON))

	_, err = f.file.WriteString(output)
	return err
}

func (f *FileStorage) WriteStats(stats Stats, configHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// TODO: refactor output for file_storage
	var output strings.Builder
	output.WriteString(fmt.Sprintf("Stats %d: %s\n", stats.Published/1000, stats.Timestamp.Format(time.RFC3339)))
	output.WriteString(" Publishers:\n")
	output.WriteString(fmt.Sprintf("   - Total Published: %d\n", stats.Published))
	output.WriteString(fmt.Sprintf("   - Publish Rate (msg/s): %.1f\n", stats.PublishRate))
	output.WriteString(fmt.Sprintf("   - Errors: %d\n", stats.PublishErrors))
	output.WriteString(" Consumers:\n")
	output.WriteString(fmt.Sprintf("   - Total Consumed: %d\n", stats.Consumed))
	output.WriteString(fmt.Sprintf("   - Ack Rate (msg/s): %.1f\n", stats.ConsumeRate))
	output.WriteString(fmt.Sprintf("   - Pending Messages: %d\n", stats.PendingMessages))
	output.WriteString(fmt.Sprintf("   - Errors: %d\n", stats.ConsumeErrors))

	if stats.Latency.Count > 0 {
		output.WriteString(" Latency (ms):\n")
		output.WriteString(fmt.Sprintf("   - Min: %s\n", FormatLatency(stats.Latency.Min)))
		output.WriteString(fmt.Sprintf("   - Mean: %s\n", FormatLatency(stats.Latency.Mean)))
		output.WriteString(fmt.Sprintf("   - Max: %s\n", FormatLatency(stats.Latency.Max)))
		output.WriteString(fmt.Sprintf("   - 99th Percentile: %s\n", FormatLatency(stats.Latency.P99)))
	}

	output.WriteString("\n")

	_, err := f.file.WriteString(output.String())
	return err
}

func (f *FileStorage) WriteFailure(stats Stats, configHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// TODO: refactor output for file_storage
	var output strings.Builder
	output.WriteString(fmt.Sprintf("Stats Failure: %s\n", stats.Timestamp.Format(time.RFC3339)))

	if len(stats.Errors) > 0 {
		output.WriteString(fmt.Sprintf(" Error: %v\n", stats.Errors[0]))
		output.WriteString(" Context: NATS connection error\n")
		output.WriteString(" Logs:\n")
		for i, err := range stats.Errors {
			if i >= 10 {
				break
			}
			output.WriteString(fmt.Sprintf("   - [ERROR] %v\n", err))
		}
	}

	output.WriteString("\n")

	_, err := f.file.WriteString(output.String())
	return err
}

func (f *FileStorage) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.file.Close()
}

func (f *FileStorage) GetConfigs(hashFilter string) ([]ConfigEntry, error) {
	// File storage doesn't support efficient read operations
	// Users should use BadgerDB for read-heavy workloads
	return nil, fmt.Errorf("GetConfigs not supported for file storage - use BadgerDB instead")
}

func (f *FileStorage) GetStats(configHashFilter string, limit int, since *time.Time) ([]StatsEntry, error) {
	// File storage doesn't support efficient read operations
	// Users should use BadgerDB for read-heavy workloads
	return nil, fmt.Errorf("GetStats not supported for file storage - use BadgerDB instead")
}

func (f *FileStorage) GetFailures(configHashFilter string, limit int, since *time.Time) ([]StatsEntry, error) {
	// File storage doesn't support efficient read operations
	// Users should use BadgerDB for read-heavy workloads
	return nil, fmt.Errorf("GetFailures not supported for file storage - use BadgerDB instead")
}

func (f *FileStorage) WriteStatsHistory(statsHistory []Stats, configHash string) error {
	// For file storage, we don't need to rewrite the entire history
	// The rolling window is maintained in memory and individual stats are written as they come
	// This method is a no-op for file storage to satisfy the interface
	return nil
}
