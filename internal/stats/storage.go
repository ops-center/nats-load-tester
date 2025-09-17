package stats

import (
	"go.bytebuilders.dev/nats-load-tester/internal/config"
)

type Storage interface {
	WriteConfigStart(cfg config.LoadTestConfig) error
	WriteStats(stats Stats) error
	WriteFailure(stats Stats) error
	Close() error
}
