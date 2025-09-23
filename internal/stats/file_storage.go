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
	isStdout      bool
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

	var isStdout bool
	if stat, err := os.Stat(filepath); err == nil {
		if stdoutStat, err := os.Stdout.Stat(); err == nil {
			isStdout = os.SameFile(stat, stdoutStat)
		}
	}

	fs := &FileStorage{
		filepath:      filepath,
		statsCache:    make(map[string][]StatsEntry),
		failuresCache: make(map[string][]StatsEntry),
		logger:        logger,
		maxEntries:    10,
		isStdout:      isStdout,
	}

	if isStdout {
		fs.logger.Info("using stdout for stats storage", zap.String("file", filepath))
		fs.file = os.Stdout
	} else {
		fs.logger.Info("using file for stats storage", zap.String("file", filepath))
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

	if f.isStdout {
		return f.writeEntryToStdout("stats", configHash, entry)
	}
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

	if f.isStdout {
		return f.writeEntryToStdout("failure", configHash, entry)
	}
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

func (f *FileStorage) GetFailures(loadTestSpec *config.LoadTestSpec, limit int, since *time.Time) ([]StatsEntry, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	configHash := loadTestSpec.Hash()
	entries, exists := f.failuresCache[configHash]
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

	if f.isStdout {
		f.file = nil
		return nil
	}

	if f.file == nil {
		return nil
	}

	defer func() {
		f.file = nil
	}()

	f.logger.Info("closing file storage")

	if err := f.file.Sync(); err != nil {
		f.logger.Error("failed to sync file before close", zap.Error(err))
	}

	if err := f.file.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	return nil
}

func (f *FileStorage) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.statsCache = make(map[string][]StatsEntry)
	f.failuresCache = make(map[string][]StatsEntry)

	if f.isStdout {
		return nil
	}

	if f.file != nil {
		if err := f.file.Close(); err != nil {
			f.logger.Error("failed to close file during clear", zap.Error(err))
		}
	}

	file, err := os.OpenFile(f.filepath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to truncate file: %w", err)
	}
	f.file = file

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
	if f.isStdout {
		f.logger.Warn("rewriteEntireFile called in stdout mode; skipping rewrite")
		return nil
	}

	if f.file == nil {
		return fmt.Errorf("file handle is not initialized")
	}

	tempFile, err := os.CreateTemp("", "stats_*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	tempFileName := tempFile.Name()
	var tempClosed, tempRenamed bool

	defer func() {
		if !tempClosed {
			if err := tempFile.Close(); err != nil {
				f.logger.Warn("failed to close temp file", zap.String("tempFile", tempFileName), zap.Error(err))
			}
		}
		if !tempRenamed {
			if err := os.Remove(tempFileName); err != nil {
				f.logger.Warn("failed to remove temp file", zap.String("tempFile", tempFileName), zap.Error(err))
			}
		}
	}()

	if err := f.writeAllDataToFile(tempFile); err != nil {
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
	tempRenamed = true

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

func (f *FileStorage) writeAllDataToFile(file *os.File) error {
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

func (f *FileStorage) writeEntryToStdout(entryType, configHash string, entry StatsEntry) error {
	persistedEntry := persistedEntry{
		Type:       entryType,
		ConfigHash: configHash,
		Timestamp:  entry.Timestamp,
		Stats:      entry.Stats,
	}

	data, err := json.Marshal(persistedEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal %s entry: %w", entryType, err)
	}

	if _, err := f.file.WriteString(string(data) + "\n"); err != nil {
		return fmt.Errorf("failed to write %s entry to stdout: %w", entryType, err)
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
