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

func (b *BadgerStorage) WriteStats(loadTestSpec *config.LoadTestSpec, stats Stats) error {
	hash := loadTestSpec.Hash()
	key := fmt.Sprintf("stats:%s:%d", hash, stats.Timestamp.UnixMilli())
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

func (b *BadgerStorage) WriteFailure(loadTestSpec *config.LoadTestSpec, stats Stats) error {
	hash := loadTestSpec.Hash()
	key := fmt.Sprintf("failure:%s:%d", hash, stats.Timestamp.UnixMilli())
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

// NOTE: NOT SAFE FROM CONCURRENT READS
func (b *BadgerStorage) Clear() error {
	return b.db.DropAll()
}

func (b *BadgerStorage) GetStats(loadTestSpec *config.LoadTestSpec, limit int, since *time.Time) ([]StatsEntry, error) {
	var stats []StatsEntry
	hash := loadTestSpec.Hash()

	err := b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte("stats:")
		if hash != "" {
			prefix = fmt.Appendf(nil, "stats:%s:", hash)
		}

		count := 0
		for it.Seek(prefix); it.ValidForPrefix(prefix) && (limit == 0 || count < limit); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Parse key: stats:{configHash}:{unix}
			parts := strings.Split(key, ":")
			if len(parts) < 3 {
				continue
			}

			configHash := parts[1]
			unixMilliStr := parts[2]

			unixMilli, err := strconv.ParseInt(unixMilliStr, 10, 64)
			if err != nil {
				continue
			}
			timestamp := time.UnixMilli(unixMilli)

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

func (b *BadgerStorage) GetFailures(loadTestSpec *config.LoadTestSpec, limit int, since *time.Time) ([]StatsEntry, error) {
	var failures []StatsEntry
	hash := loadTestSpec.Hash()

	err := b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte("failure:")
		if hash != "" {
			prefix = fmt.Appendf(nil, "failure:%s:", hash)
		}

		count := 0
		for it.Seek(prefix); it.ValidForPrefix(prefix) && (limit == 0 || count < limit); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Parse key: failure:{configHash}:{unix}
			parts := strings.Split(key, ":")
			if len(parts) < 3 {
				continue
			}

			configHash := parts[1]
			unixMilliStr := parts[2]

			unixMilli, err := strconv.ParseInt(unixMilliStr, 10, 64)
			if err != nil {
				continue
			}
			timestamp := time.UnixMilli(unixMilli)

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
