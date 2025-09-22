package stats

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
)

type FileStorage struct {
	mu            sync.RWMutex
	filepath      string
	file          *os.File
	statsCache    map[string][]StatsEntry
	failuresCache map[string][]StatsEntry
	maxEntries    int
	logger        *zap.Logger
}

type persistedEntry struct {
	Type       string    `json:"type"`
	ConfigHash string    `json:"config_hash"`
	Timestamp  time.Time `json:"timestamp"`
	Stats      Stats     `json:"stats"`
}

func NewFileStorage(filepath string, logger *zap.Logger) (*FileStorage, error) {
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	fs := &FileStorage{
		filepath:      filepath,
		statsCache:    make(map[string][]StatsEntry),
		failuresCache: make(map[string][]StatsEntry),
		logger:        logger,
		maxEntries:    10,
	}

	// Open file handle first, then load existing data
	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	fs.file = file

	if err := fs.loadFromFile(); err != nil {
		if closeErr := fs.file.Close(); closeErr != nil {
			logger.Warn("failed to close file after load error", zap.Error(closeErr))
		}
		return nil, fmt.Errorf("failed to load existing data: %w", err)
	}

	return fs, nil
}

func (f *FileStorage) WriteStats(loadTestSpec *config.LoadTestSpec, stats Stats) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	configHash := loadTestSpec.Hash()
	entry := StatsEntry{
		ConfigHash: configHash,
		Timestamp:  stats.Timestamp,
		Stats:      stats,
	}

	f.addToCache(f.statsCache, configHash, entry)

	return f.rewriteEntireFile()
}

func (f *FileStorage) WriteFailure(loadTestSpec *config.LoadTestSpec, stats Stats) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	configHash := loadTestSpec.Hash()
	entry := StatsEntry{
		ConfigHash: configHash,
		Timestamp:  stats.Timestamp,
		Stats:      stats,
	}

	f.addToCache(f.failuresCache, configHash, entry)

	return f.rewriteEntireFile()
}

func (f *FileStorage) GetStats(loadTestSpec *config.LoadTestSpec, limit int, since *time.Time) ([]StatsEntry, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	configHash := loadTestSpec.Hash()
	entries, exists := f.statsCache[configHash]
	if !exists {
		return []StatsEntry{}, nil
	}

	var filtered []StatsEntry
	for _, entry := range entries {
		if since != nil && entry.Timestamp.Before(*since) {
			continue
		}
		filtered = append(filtered, entry)
	}

	if limit > 0 && len(filtered) > limit {
		start := len(filtered) - limit
		filtered = filtered[start:]
	}

	return filtered, nil
}

func (f *FileStorage) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.file == nil {
		return nil
	}
	f.logger.Info("closing file storage")

	syncErr := f.file.Sync()
	closeErr := f.file.Close()

	f.file = nil

	// TODO: consider handling this better instead of nested error wrapping
	if syncErr != nil && closeErr != nil {
		return fmt.Errorf("failed to sync file: %w", fmt.Errorf("failed to close file: %w", closeErr))
	}

	if syncErr != nil {
		return fmt.Errorf("failed to sync file: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close file: %w", closeErr)
	}

	return nil
}

func (f *FileStorage) addToCache(cache map[string][]StatsEntry, configHash string, entry StatsEntry) {
	entries := cache[configHash]

	entries = append(entries, entry)

	for len(entries) > f.maxEntries {
		entries = entries[1:]
	}

	cache[configHash] = entries
}

func (f *FileStorage) rewriteEntireFile() error {
	if f.file == nil {
		return fmt.Errorf("file handle is not initialized")
	}

	tempFile, err := os.CreateTemp("", "stats_*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	tempFileName := tempFile.Name()
	var tempClosed bool

	defer func() {
		if !tempClosed {
			if err := tempFile.Close(); err != nil {
				f.logger.Warn("failed to close temp file", zap.String("tempFile", tempFileName), zap.Error(err))
			}
		}
		if err := os.Remove(tempFileName); err != nil {
			f.logger.Warn("failed to remove temp file", zap.String("tempFile", tempFileName), zap.Error(err))
		}
	}()

	if err := f.writeAllData(tempFile); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	tempClosed = true

	if err := os.Rename(tempFileName, f.filepath); err != nil {
		return fmt.Errorf("failed to replace file: %w", err)
	}

	if err := f.file.Close(); err != nil {
		return fmt.Errorf("failed to close old file handle: %w", err)
	}

	file, err := os.OpenFile(f.filepath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		f.file = nil
		return fmt.Errorf("failed to reopen file: %w", err)
	}
	f.file = file

	return nil
}

func (f *FileStorage) writeAllData(file *os.File) error {
	for configHash, entries := range f.statsCache {
		for _, statsEntry := range entries {
			entry := persistedEntry{
				Type:       "stats",
				ConfigHash: configHash,
				Timestamp:  statsEntry.Timestamp,
				Stats:      statsEntry.Stats,
			}
			data, err := json.Marshal(entry)
			if err != nil {
				return fmt.Errorf("failed to marshal stats entry: %w", err)
			}
			if _, err := file.WriteString(string(data) + "\n"); err != nil {
				return fmt.Errorf("failed to write stats entry: %w", err)
			}
		}
	}

	for configHash, entries := range f.failuresCache {
		for _, statsEntry := range entries {
			entry := persistedEntry{
				Type:       "failure",
				ConfigHash: configHash,
				Timestamp:  statsEntry.Timestamp,
				Stats:      statsEntry.Stats,
			}
			data, err := json.Marshal(entry)
			if err != nil {
				return fmt.Errorf("failed to marshal failure entry: %w", err)
			}
			if _, err := file.WriteString(string(data) + "\n"); err != nil {
				return fmt.Errorf("failed to write failure entry: %w", err)
			}
		}
	}

	return nil
}

func (f *FileStorage) loadFromFile() error {
	if _, err := f.file.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek to beginning: %w", err)
	}

	scanner := bufio.NewScanner(f.file)
	for scanner.Scan() {
		var entry persistedEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // Skip malformed lines
		}

		statsEntry := StatsEntry{
			ConfigHash: entry.ConfigHash,
			Timestamp:  entry.Timestamp,
			Stats:      entry.Stats,
		}

		switch entry.Type {
		case "stats":
			f.addToCache(f.statsCache, entry.ConfigHash, statsEntry)
		case "failure":
			f.addToCache(f.failuresCache, entry.ConfigHash, statsEntry)
		}
	}

	return scanner.Err()
}
