package stats

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
)

// go test ./internal/stats -run TestStorage
// WARNING: THIS TEST DELETES ALL DATA IN THE GIVEN STORAGE PATHS (IF ANY)
func TestStorage(t *testing.T) {
	// disable log writes
	logger := zap.NewNop()

	tests := []struct {
		name    string
		storage func() Storage
	}{
		{
			name: "FileStorage",
			storage: func() Storage {
				fs, err := NewFileStorage(t.TempDir()+"/test.json", logger)
				if err != nil {
					t.Fatalf("failed to create FileStorage: %v", err)
				}
				return fs
			},
		},
		{
			name: "BadgerDB",
			storage: func() Storage {
				bs, err := NewBadgerStorage(t.TempDir(), logger)
				if err != nil {
					t.Fatalf("failed to create BadgerStorage: %v", err)
				}
				return bs
			},
		},
		// {
		// 	name: "FileStorage_Stdout",
		// 	storage: func() Storage {
		// 		fs, err := NewFileStorage("/dev/stdout", logger)
		// 		if err != nil {
		// 			t.Fatalf("failed to create FileStorage: %v", err)
		// 		}
		// 		return fs
		// 	},
		// },
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := tt.storage()
			if err := storage.Clear(); err != nil {
				t.Fatalf("failed to clear storage: %v", err)
			}
			defer func() {
				if err := storage.Close(); err != nil {
					t.Errorf("Failed to close storage: %v", err)
				}
			}()
			testAllStorageOperations(t, storage)
		})
	}
}

func testAllStorageOperations(t *testing.T, storage Storage) {
	t.Run("Basic Operations", func(t *testing.T) {
		defer func() {
			if err := storage.Clear(); err != nil {
				t.Logf("Failed to clear storage: %v", err)
			}
		}()

		spec := createTestLoadTestSpec()
		stats := createTestStats()

		if err := storage.WriteStats(spec, stats); err != nil {
			t.Fatalf("WriteStats failed: %v", err)
		}

		entries, err := storage.GetStats(spec, 10, nil)
		if err != nil {
			t.Fatalf("GetStats failed: %v", err)
		}

		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}

		entry := entries[0]
		if entry.ConfigHash != spec.Hash() {
			t.Errorf("expected config hash %s, got %s", spec.Hash(), entry.ConfigHash)
		}

		if entry.Stats.Published != stats.Published {
			t.Errorf("expected published %d, got %d", stats.Published, entry.Stats.Published)
		}

		if err := storage.WriteFailure(spec, stats); err != nil {
			t.Fatalf("WriteFailure failed: %v", err)
		}

		failures, err := storage.GetFailures(spec, 10, nil)
		if err != nil {
			t.Fatalf("GetFailures failed: %v", err)
		}

		if len(failures) != 1 {
			t.Fatalf("expected 1 failure entry, got %d", len(failures))
		}

		failureEntry := failures[0]
		if failureEntry.ConfigHash != spec.Hash() {
			t.Errorf("expected config hash %s, got %s", spec.Hash(), failureEntry.ConfigHash)
		}

		if failureEntry.Stats.Published != stats.Published {
			t.Errorf("expected published %d, got %d", stats.Published, failureEntry.Stats.Published)
		}
	})

	t.Run("Limits and Filters", func(t *testing.T) {
		defer func() {
			if err := storage.Clear(); err != nil {
				t.Logf("Failed to clear storage: %v", err)
			}
		}()

		spec := createTestLoadTestSpec()

		for i := range 5 {
			stats := createTestStats()
			stats.Published = uint64(i)
			if err := storage.WriteStats(spec, stats); err != nil {
				t.Fatalf("WriteStats failed: %v", err)
			}
		}

		entries, err := storage.GetStats(spec, 3, nil)
		if err != nil {
			t.Fatalf("GetStats failed: %v", err)
		}

		if len(entries) > 3 {
			t.Errorf("expected at most 3 entries, got %d", len(entries))
		}

		cutoffTime := time.Now()
		time.Sleep(10 * time.Millisecond)

		stats := createTestStats()
		stats.Published = 999
		if err := storage.WriteStats(spec, stats); err != nil {
			t.Fatalf("WriteStats failed: %v", err)
		}

		entries, err = storage.GetStats(spec, 10, &cutoffTime)
		if err != nil {
			t.Fatalf("GetStats failed: %v", err)
		}

		if len(entries) == 0 {
			t.Error("expected at least 1 entry after cutoff time")
		}

		for _, entry := range entries {
			if entry.Timestamp.Before(cutoffTime) {
				t.Errorf("entry timestamp %v is before cutoff %v", entry.Timestamp, cutoffTime)
			}
		}

		// Test GetFailures with limits and filters
		for i := range 3 {
			failureStats := createTestStats()
			failureStats.PublishErrors = uint64(i + 1)
			if err := storage.WriteFailure(spec, failureStats); err != nil {
				t.Fatalf("WriteFailure failed: %v", err)
			}
		}

		failures, err := storage.GetFailures(spec, 2, nil)
		if err != nil {
			t.Fatalf("GetFailures failed: %v", err)
		}

		if len(failures) > 2 {
			t.Errorf("expected at most 2 failure entries, got %d", len(failures))
		}

		cutoffTimeFailures := time.Now()
		time.Sleep(10 * time.Millisecond)

		lateFailure := createTestStats()
		lateFailure.PublishErrors = 999
		if err := storage.WriteFailure(spec, lateFailure); err != nil {
			t.Fatalf("WriteFailure failed: %v", err)
		}

		failures, err = storage.GetFailures(spec, 10, &cutoffTimeFailures)
		if err != nil {
			t.Fatalf("GetFailures failed: %v", err)
		}

		if len(failures) == 0 {
			t.Error("expected at least 1 failure entry after cutoff time")
		}

		for _, failureEntry := range failures {
			if failureEntry.Timestamp.Before(cutoffTimeFailures) {
				t.Errorf("failure entry timestamp %v is before cutoff %v", failureEntry.Timestamp, cutoffTimeFailures)
			}
		}
	})

	t.Run("Concurrent Write/Read Stress", func(t *testing.T) {
		defer func() {
			if err := storage.Clear(); err != nil {
				t.Logf("Failed to clear storage: %v", err)
			}
		}()

		spec := createTestLoadTestSpec()
		numGoroutines := 50
		writesPerGoroutine := 20

		var wg sync.WaitGroup
		var writeErrors sync.Map
		var readErrors sync.Map

		for i := range numGoroutines {
			wg.Add(1)
			go func(writerID int) {
				defer wg.Done()
				for j := range writesPerGoroutine {
					stats := createTestStats()
					stats.Published = uint64(writerID*1000 + j)
					if err := storage.WriteStats(spec, stats); err != nil {
						writeErrors.Store(fmt.Sprintf("writer_%d_%d", writerID, j), err)
					}
				}
			}(i)
		}

		for i := range numGoroutines {
			wg.Add(1)
			go func(readerID int) {
				defer wg.Done()
				for j := range writesPerGoroutine {
					_, err := storage.GetStats(spec, 100, nil)
					if err != nil {
						readErrors.Store(fmt.Sprintf("reader_%d_%d", readerID, j), err)
					}
					time.Sleep(1 * time.Millisecond)
				}
			}(i)
		}

		wg.Wait()

		writeErrors.Range(func(key, value any) bool {
			t.Errorf("Write error %s: %v", key, value)
			return true
		})
		readErrors.Range(func(key, value any) bool {
			t.Errorf("Read error %s: %v", key, value)
			return true
		})

		entries, err := storage.GetStats(spec, 0, nil)
		if err != nil {
			t.Fatalf("Final GetStats failed: %v", err)
		}

		if len(entries) == 0 {
			t.Error("Expected some entries after concurrent writes")
		}

		t.Logf("Concurrent test completed: %d writers, %d readers, final entries: %d",
			numGoroutines, numGoroutines, len(entries))
	})

	t.Run("Large Volume Performance", func(t *testing.T) {
		defer func() {
			if err := storage.Clear(); err != nil {
				t.Logf("Failed to clear storage: %v", err)
			}
		}()

		spec := createTestLoadTestSpec()
		numEntries := 1000

		start := time.Now()
		for i := range numEntries {
			stats := createTestStats()
			stats.Published = uint64(i)
			stats.Timestamp = time.Now().Add(time.Duration(i) * time.Millisecond)

			if err := storage.WriteStats(spec, stats); err != nil {
				t.Fatalf("WriteStats %d failed: %v", i, err)
			}
		}
		writeTime := time.Since(start)

		start = time.Now()
		entries, err := storage.GetStats(spec, 0, nil)
		if err != nil {
			t.Fatalf("GetStats failed: %v", err)
		}
		readTime := time.Since(start)

		t.Logf("Wrote %d entries in %v, read in %v", numEntries, writeTime, readTime)

		if len(entries) == 0 {
			t.Error("Expected entries after large volume write")
		}

		if writeTime > 5*time.Second {
			t.Errorf("Write performance too slow: %v for %d entries", writeTime, numEntries)
		}
	})

	t.Run("Multiple Config Isolation", func(t *testing.T) {
		defer func() {
			if err := storage.Clear(); err != nil {
				t.Logf("Failed to clear storage: %v", err)
			}
		}()

		specs := make([]*config.LoadTestSpec, 10)
		for i := range specs {
			spec := createTestLoadTestSpec()
			spec.Name = fmt.Sprintf("test-spec-%d", i)
			spec.Publishers.CountPerStream = int32(i + 1)
			specs[i] = spec
		}

		for i, spec := range specs {
			for j := range 5 {
				stats := createTestStats()
				stats.Published = uint64(i*100 + j)
				if err := storage.WriteStats(spec, stats); err != nil {
					t.Fatalf("WriteStats for spec %d failed: %v", i, err)
				}
			}
		}

		for i, spec := range specs {
			entries, err := storage.GetStats(spec, 0, nil)
			if err != nil {
				t.Fatalf("GetStats for spec %d failed: %v", i, err)
			}

			if len(entries) == 0 {
				t.Errorf("Expected entries for spec %d", i)
			}

			for _, entry := range entries {
				if entry.ConfigHash != spec.Hash() {
					t.Errorf("Entry has wrong config hash: expected %s, got %s", spec.Hash(), entry.ConfigHash)
				}
			}
		}

		t.Logf("Multi-config test completed: %d configs with 5 entries each", len(specs))
	})

	t.Run("Edge Cases", func(t *testing.T) {
		t.Run("Empty Storage Read", func(t *testing.T) {
			defer func() {
				if err := storage.Clear(); err != nil {
					t.Logf("Failed to clear storage: %v", err)
				}
			}()

			spec := createTestLoadTestSpec()
			entries, err := storage.GetStats(spec, 10, nil)
			if err != nil {
				t.Fatalf("GetStats on empty storage failed: %v", err)
			}

			if len(entries) != 0 {
				t.Errorf("Expected 0 entries from empty storage, got %d", len(entries))
			}
		})

		t.Run("Zero Limit", func(t *testing.T) {
			defer func() {
				if err := storage.Clear(); err != nil {
					t.Logf("Failed to clear storage: %v", err)
				}
			}()

			spec := createTestLoadTestSpec()
			stats := createTestStats()
			if err := storage.WriteStats(spec, stats); err != nil {
				t.Fatalf("WriteStats failed: %v", err)
			}

			entries, err := storage.GetStats(spec, 0, nil)
			if err != nil {
				t.Fatalf("GetStats with zero limit failed: %v", err)
			}

			if len(entries) == 0 {
				t.Error("Expected entries with zero limit (should return all)")
			}
		})

		t.Run("Rapid Sequential Writes", func(t *testing.T) {
			defer func() {
				if err := storage.Clear(); err != nil {
					t.Logf("Failed to clear storage: %v", err)
				}
			}()

			spec := createTestLoadTestSpec()

			start := time.Now()
			for i := range 100 {
				stats := createTestStats()
				stats.Published = uint64(i)
				if err := storage.WriteStats(spec, stats); err != nil {
					t.Fatalf("Rapid write %d failed: %v", i, err)
				}
			}
			duration := time.Since(start)

			entries, err := storage.GetStats(spec, 0, nil)
			if err != nil {
				t.Fatalf("GetStats after rapid writes failed: %v", err)
			}

			t.Logf("Rapid writes: 100 entries in %v, final count: %d", duration, len(entries))

			if len(entries) == 0 {
				t.Error("Expected entries after rapid writes")
			}
		})

		t.Run("Cache Limit Boundary", func(t *testing.T) {
			defer func() {
				if err := storage.Clear(); err != nil {
					t.Logf("Failed to clear storage: %v", err)
				}
			}()

			spec := createTestLoadTestSpec()
			maxEntries := 15

			for i := range maxEntries {
				stats := createTestStats()
				stats.Published = uint64(i)
				stats.Timestamp = time.Now().Add(time.Duration(i) * time.Second)

				if err := storage.WriteStats(spec, stats); err != nil {
					t.Fatalf("WriteStats %d failed: %v", i, err)
				}
			}

			entries, err := storage.GetStats(spec, 0, nil)
			if err != nil {
				t.Fatalf("GetStats failed: %v", err)
			}

			if len(entries) == 0 {
				t.Error("Expected some entries after writing")
			}

			t.Logf("Cache boundary test: wrote %d entries, retained %d", maxEntries, len(entries))
		})
	})
}

func createTestLoadTestSpec() *config.LoadTestSpec {
	return &config.LoadTestSpec{
		Name:           "test-spec",
		NATSURL:        "nats://localhost:4222",
		UseJetStream:   false,
		ClientIDPrefix: "test",
		Publishers: config.PublisherConfig{
			CountPerStream:       1,
			PublishRatePerSecond: 10,
		},
		Consumers: config.ConsumerConfig{
			CountPerStream: 1,
		},
		Behavior: config.BehaviorConfig{
			DurationSeconds: 60,
		},
	}
}

func createTestStats() Stats {
	return Stats{
		Timestamp:     time.Now(),
		Published:     100,
		Consumed:      95,
		PublishRate:   10.5,
		ConsumeRate:   9.8,
		PublishErrors: 1,
		ConsumeErrors: 0,
		Latency: LatencyStats{
			Min:   1 * time.Millisecond,
			Max:   50 * time.Millisecond,
			Mean:  10 * time.Millisecond,
			P99:   45 * time.Millisecond,
			Count: 95,
		},
	}
}
