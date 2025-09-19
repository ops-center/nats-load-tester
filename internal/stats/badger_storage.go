package stats

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	badger "github.com/dgraph-io/badger/v4"
	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
)

const (
	statsDataTTL   = 24 * time.Hour
	configDataTTL  = 7 * 24 * time.Hour
	failureDataTTL = 72 * time.Hour
)

type BadgerStorage struct {
	mu     sync.RWMutex
	db     *badger.DB
	logger *zap.Logger
}

var (
	badgerInstances = make(map[string]*BadgerStorage)
	badgerMutex     sync.RWMutex
)

func NewBadgerStorage(path string, logger *zap.Logger) (*BadgerStorage, error) {
	badgerMutex.Lock()
	defer badgerMutex.Unlock()

	if existing, exists := badgerInstances[path]; exists {
		return existing, nil
	}

	var db *badger.DB
	var err error
	opts := badger.DefaultOptions(path).WithBypassLockGuard(false)

	if path == "" {
		if logger != nil {
			logger.Info("using in-memory badger storage")
		}
		opts = opts.WithInMemory(true)
	}

	db, err = badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize badger storage: %w", err)
	}

	storage := &BadgerStorage{
		db:     db,
		logger: logger,
	}

	badgerInstances[path] = storage

	go func() {
		// TODO: make this configurable and/or smarter
		// https://docs.hypermode.com/badger/quickstart#garbage-collection
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
		again:
			err := storage.db.RunValueLogGC(0.5)
			if err == nil {
				goto again
			}
		}
	}()

	return storage, nil
}

func (b *BadgerStorage) WriteConfigInfo(cfg *config.Config, hash string) error {
	key := fmt.Sprintf("config:%s:%d", hash, time.Now().Unix())
	value, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return b.db.Update(func(txn *badger.Txn) error {
		entry := badger.NewEntry([]byte(key), value).WithTTL(configDataTTL)
		if err := txn.SetEntry(entry); err != nil {
			return fmt.Errorf("failed to set config entry: %w", err)
		}
		return nil
	})
}

func (b *BadgerStorage) WriteStats(stats Stats, configHash string) error {
	key := fmt.Sprintf("stats:%s:%s:%d", configHash, stats.Timestamp.Format(time.RFC3339), stats.Timestamp.Unix())
	value, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("failed to marshal stats: %w", err)
	}

	return b.db.Update(func(txn *badger.Txn) error {
		entry := badger.NewEntry([]byte(key), value).WithTTL(statsDataTTL)
		if err := txn.SetEntry(entry); err != nil {
			return fmt.Errorf("failed to set stats entry: %w", err)
		}
		return nil
	})
}

func (b *BadgerStorage) WriteFailure(stats Stats, configHash string) error {
	key := fmt.Sprintf("failure:%s:%s:%d", configHash, stats.Timestamp.Format(time.RFC3339), stats.Timestamp.Unix())
	value, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("failed to marshal failure stats: %w", err)
	}

	return b.db.Update(func(txn *badger.Txn) error {
		entry := badger.NewEntry([]byte(key), value).WithTTL(failureDataTTL)
		if err := txn.SetEntry(entry); err != nil {
			return fmt.Errorf("failed to set failure entry: %w", err)
		}
		return nil
	})
}

func (b *BadgerStorage) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.logger != nil {
		b.logger.Info("closing badger storage")
	}

	badgerMutex.Lock()
	for path, instance := range badgerInstances {
		if instance == b {
			delete(badgerInstances, path)
			break
		}
	}
	badgerMutex.Unlock()

	return b.db.Close()
}

func (b *BadgerStorage) WriteStatsHistory(statsHistory []Stats, configHash string) error {
	// Store the rolling history for the configuration
	key := fmt.Sprintf("history:%s", configHash)
	value, err := json.Marshal(statsHistory)
	if err != nil {
		return fmt.Errorf("failed to marshal stats history: %w", err)
	}

	return b.db.Update(func(txn *badger.Txn) error {
		entry := badger.NewEntry([]byte(key), value).WithTTL(statsDataTTL)
		if err := txn.SetEntry(entry); err != nil {
			return fmt.Errorf("failed to set stats history entry: %w", err)
		}
		return nil
	})
}

func (b *BadgerStorage) GetConfigs(hashFilter string) ([]ConfigEntry, error) {
	var configs []ConfigEntry

	err := b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte("config:")
		if hashFilter != "" {
			prefix = fmt.Appendf(nil, "config:%s:", hashFilter)
		}

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Parse key: config:{hash}:{timestamp}
			parts := strings.Split(key, ":")
			if len(parts) < 3 {
				continue
			}

			hash := parts[1]
			timestampStr := parts[2]

			// Try parsing as Unix timestamp first, then RFC3339
			var timestamp time.Time
			if unixTime, err := strconv.ParseInt(timestampStr, 10, 64); err == nil {
				timestamp = time.Unix(unixTime, 0)
			} else if parsed, err := time.Parse(time.RFC3339, timestampStr); err == nil {
				timestamp = parsed
			} else {
				continue
			}

			err := item.Value(func(val []byte) error {
				var cfg config.Config
				if err := json.Unmarshal(val, &cfg); err != nil {
					return err
				}

				configs = append(configs, ConfigEntry{
					Hash:      hash,
					Timestamp: timestamp,
					Config:    &cfg,
				})
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	return configs, err
}

func (b *BadgerStorage) GetStats(configHashFilter string, limit int, since *time.Time) ([]StatsEntry, error) {
	var stats []StatsEntry

	err := b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte("stats:")
		if configHashFilter != "" {
			prefix = fmt.Appendf(nil, "stats:%s:", configHashFilter)
		}

		count := 0
		for it.Seek(prefix); it.ValidForPrefix(prefix) && (limit == 0 || count < limit); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Parse key: stats:{configHash}:{timestamp}:{unix}
			parts := strings.Split(key, ":")
			if len(parts) < 4 {
				continue
			}

			configHash := parts[1]
			timestampStr := parts[2]

			timestamp, err := time.Parse(time.RFC3339, timestampStr)
			if err != nil {
				continue
			}

			// Filter by since timestamp if provided
			if since != nil && timestamp.Before(*since) {
				continue
			}

			err = item.Value(func(val []byte) error {
				var s Stats
				if err := json.Unmarshal(val, &s); err != nil {
					return err
				}

				stats = append(stats, StatsEntry{
					ConfigHash: configHash,
					Timestamp:  timestamp,
					Stats:      s,
				})
				return nil
			})
			if err != nil {
				return err
			}
			count++
		}
		return nil
	})

	return stats, err
}

func (b *BadgerStorage) GetFailures(configHashFilter string, limit int, since *time.Time) ([]StatsEntry, error) {
	var failures []StatsEntry

	err := b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte("failure:")
		if configHashFilter != "" {
			prefix = fmt.Appendf(nil, "failure:%s:", configHashFilter)
		}

		count := 0
		for it.Seek(prefix); it.ValidForPrefix(prefix) && (limit == 0 || count < limit); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Parse key: failure:{configHash}:{timestamp}:{unix}
			parts := strings.Split(key, ":")
			if len(parts) < 4 {
				continue
			}

			configHash := parts[1]
			timestampStr := parts[2]

			timestamp, err := time.Parse(time.RFC3339, timestampStr)
			if err != nil {
				continue
			}

			// Filter by since timestamp if provided
			if since != nil && timestamp.Before(*since) {
				continue
			}

			err = item.Value(func(val []byte) error {
				var s Stats
				if err := json.Unmarshal(val, &s); err != nil {
					return err
				}

				failures = append(failures, StatsEntry{
					ConfigHash: configHash,
					Timestamp:  timestamp,
					Stats:      s,
				})
				return nil
			})
			if err != nil {
				return err
			}
			count++
		}
		return nil
	})

	return failures, err
}
