package stats

import (
	"time"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
)

type Storage interface {
	WriteStats(loadTestSpec *config.LoadTestSpec, stats Stats) error
	WriteFailure(loadTestSpec *config.LoadTestSpec, stats Stats) error

	GetStats(loadTestSpec *config.LoadTestSpec, limit int, since *time.Time) ([]StatsEntry, error)
	// GetFailures(loadTestSpec *config.LoadTestSpec, limit int, since *time.Time) ([]StatsEntry, error)

	Close() error
}

type ConfigEntry struct {
	Hash      string         `json:"hash"`
	Timestamp time.Time      `json:"timestamp"`
	Config    *config.Config `json:"config"`
}

type StatsEntry struct {
	ConfigHash string    `json:"config_hash"`
	Timestamp  time.Time `json:"timestamp"`
	Stats      Stats     `json:"stats"`
}
