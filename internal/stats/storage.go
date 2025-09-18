package stats

import (
	"go.bytebuilders.dev/nats-load-tester/internal/config"
)

type Storage interface {
	WriteConfigStart(cfg config.LoadTestSpec) error
	WriteStats(stats Stats) error
	WriteFailure(stats Stats) error
	Close() error
}
