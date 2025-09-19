package stats

import (
	"time"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
)

type Storage interface {
	// Write methods
	WriteConfigInfo(cfg *config.Config, hash string) error
	WriteStats(stats Stats, configHash string) error
	WriteFailure(stats Stats, configHash string) error
	WriteStatsHistory(statsHistory []Stats, configHash string) error

	// Read methods - general purpose with optional filters
	GetConfigs(hashFilter string) ([]ConfigEntry, error)
	GetStats(configHashFilter string, limit int, since *time.Time) ([]StatsEntry, error)
	GetFailures(configHashFilter string, limit int, since *time.Time) ([]StatsEntry, error)

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
