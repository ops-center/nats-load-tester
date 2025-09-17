package stats

import (
	"encoding/json"
	"fmt"
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

	// Store in global map for reuse
	badgerInstances[path] = storage

	go func() {
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

func (b *BadgerStorage) WriteConfigStart(cfg config.LoadTestConfig) error {
	key := fmt.Sprintf("config:%s:%d", cfg.Hash(), time.Now().Unix())
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

func (b *BadgerStorage) WriteStats(stats Stats) error {
	key := fmt.Sprintf("stats:%s:%d", stats.Timestamp.Format(time.RFC3339), stats.Timestamp.Unix())
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

func (b *BadgerStorage) WriteFailure(stats Stats) error {
	key := fmt.Sprintf("failure:%s:%d", stats.Timestamp.Format(time.RFC3339), stats.Timestamp.Unix())
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
