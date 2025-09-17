package controlplane

import (
	"context"
	"sync"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.bytebuilders.dev/nats-load-tester/internal/stats"
)

type Server interface {
	Start(ctx context.Context) error
	Stop() error
	ConfigChannel() <-chan config.Config
}

type BaseServer struct {
	mu            sync.RWMutex
	currentConfig *config.Config
	configChannel chan config.Config
	configHash    string
	collector     *stats.Collector
}

func NewBaseServer() *BaseServer {
	return &BaseServer{
		configChannel: make(chan config.Config, 1),
	}
}

func (b *BaseServer) ConfigChannel() <-chan config.Config {
	return b.configChannel
}

func (b *BaseServer) SetConfig(cfg config.Config) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	newHash := cfg.Hash()
	if b.configHash == newHash {
		return false
	}

	b.currentConfig = &cfg
	b.configHash = newHash

	select {
	case b.configChannel <- cfg:
	default:
	}

	return true
}

func (b *BaseServer) GetConfig() *config.Config {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.currentConfig
}

func (b *BaseServer) SetCollector(collector *stats.Collector) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.collector = collector
}

func (b *BaseServer) GetCollector() *stats.Collector {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.collector
}
