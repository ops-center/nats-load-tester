package stats

import (
	"encoding/json"
	"fmt"
	"time"

	badger "github.com/dgraph-io/badger/v3"
	"go.bytebuilders.dev/nats-load-tester/internal/config"
)

type BadgerStorage struct {
	db *badger.DB
}

func NewBadgerStorage(path string) (*BadgerStorage, error) {
	opts := badger.DefaultOptions(path)
	opts.Logger = nil

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	return &BadgerStorage{
		db: db,
	}, nil
}

func (b *BadgerStorage) WriteConfigStart(cfg config.LoadTestConfig) error {
	key := fmt.Sprintf("config:%s:%d", cfg.Hash(), time.Now().Unix())
	value, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	return b.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), value)
	})
}

func (b *BadgerStorage) WriteStats(stats Stats) error {
	key := fmt.Sprintf("stats:%s:%d", stats.Timestamp.Format("20060102"), stats.Timestamp.Unix())
	value, err := json.Marshal(stats)
	if err != nil {
		return err
	}

	return b.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), value)
	})
}

func (b *BadgerStorage) WriteFailure(stats Stats) error {
	key := fmt.Sprintf("failure:%s:%d", stats.Timestamp.Format("20060102"), stats.Timestamp.Unix())
	value, err := json.Marshal(stats)
	if err != nil {
		return err
	}

	return b.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), value)
	})
}

func (b *BadgerStorage) Close() error {
	return b.db.Close()
}
