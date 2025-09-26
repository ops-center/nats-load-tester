package stats

import (
	"time"

	"go.opscenter.dev/nats-load-tester/internal/config"
)

type Storage interface {
	WriteStats(loadTestSpec *config.LoadTestSpec, stats Stats) error
	WriteFailure(loadTestSpec *config.LoadTestSpec, stats Stats) error

	GetStats(loadTestSpec *config.LoadTestSpec, limit int, since *time.Time) ([]StatsEntry, error)
	GetFailures(loadTestSpec *config.LoadTestSpec, limit int, since *time.Time) ([]StatsEntry, error)

	Clear() error
	Close() error
}

type StatsEntry struct {
	ConfigHash string    `json:"config_hash"`
	Timestamp  time.Time `json:"timestamp"`
	Stats      Stats     `json:"stats"`
}
