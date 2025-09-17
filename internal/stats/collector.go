package stats

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
)

type Collector struct {
	mu            sync.RWMutex
	logger        *zap.Logger
	config        config.LoadTestConfig
	storage       Storage
	published     atomic.Uint64
	consumed      atomic.Uint64
	publishErrors atomic.Uint64
	consumeErrors atomic.Uint64
	latencies     []time.Duration
	latencyMu     sync.Mutex
	errors        []error
	errorsMu      sync.Mutex
	startTime     time.Time
	lastStatsTime time.Time
	statsHistory  []Stats
	maxHistory    int
}

type Stats struct {
	Timestamp       time.Time
	Published       uint64
	Consumed        uint64
	PublishRate     float64
	ConsumeRate     float64
	PublishErrors   uint64
	ConsumeErrors   uint64
	PendingMessages int64
	Latency         LatencyStats
	Errors          []error
}

type LatencyStats struct {
	Min   time.Duration
	Max   time.Duration
	Mean  time.Duration
	P99   time.Duration
	Count int
}

func NewCollector(logger *zap.Logger, storage Storage) *Collector {
	return &Collector{
		logger:        logger,
		storage:       storage,
		latencies:     make([]time.Duration, 0, 10000),
		errors:        make([]error, 0),
		statsHistory:  make([]Stats, 0, 10),
		maxHistory:    10,
		startTime:     time.Now(),
		lastStatsTime: time.Now(),
	}
}

func (c *Collector) SetConfig(cfg config.LoadTestConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.config = cfg
	c.Reset()

	if err := c.storage.WriteConfigStart(cfg); err != nil {
		c.logger.Error("Failed to write config start", zap.Error(err))
	}
}

func (c *Collector) Reset() {
	c.published.Store(0)
	c.consumed.Store(0)
	c.publishErrors.Store(0)
	c.consumeErrors.Store(0)
	c.latencies = c.latencies[:0]
	c.errors = c.errors[:0]
	c.statsHistory = c.statsHistory[:0]
	c.startTime = time.Now()
	c.lastStatsTime = time.Now()
}

func (c *Collector) RecordPublish() {
	c.published.Add(1)
}

func (c *Collector) RecordPublishError(err error) {
	c.publishErrors.Add(1)
	c.recordError(err)
}

func (c *Collector) RecordConsume() {
	c.consumed.Add(1)
}

func (c *Collector) RecordConsumeError(err error) {
	c.consumeErrors.Add(1)
	c.recordError(err)
}

func (c *Collector) RecordError(err error) {
	c.recordError(err)
}

func (c *Collector) recordError(err error) {
	c.errorsMu.Lock()
	defer c.errorsMu.Unlock()

	if len(c.errors) < c.config.LogLimits.MaxLines {
		c.errors = append(c.errors, err)
	}
}

func (c *Collector) RecordLatency(latency time.Duration) {
	c.latencyMu.Lock()
	defer c.latencyMu.Unlock()

	c.latencies = append(c.latencies, latency)
}

func (c *Collector) CollectStats() Stats {
	now := time.Now()
	duration := now.Sub(c.lastStatsTime).Seconds()

	stats := Stats{
		Timestamp:     now,
		Published:     c.published.Load(),
		Consumed:      c.consumed.Load(),
		PublishErrors: c.publishErrors.Load(),
		ConsumeErrors: c.consumeErrors.Load(),
	}

	if duration > 0 {
		stats.PublishRate = float64(stats.Published) / duration
		stats.ConsumeRate = float64(stats.Consumed) / duration
	}

	stats.PendingMessages = int64(stats.Published) - int64(stats.Consumed)

	c.latencyMu.Lock()
	if len(c.latencies) > 0 {
		stats.Latency = c.calculateLatencyStats(c.latencies)
		c.latencies = c.latencies[:0]
	}
	c.latencyMu.Unlock()

	c.errorsMu.Lock()
	stats.Errors = make([]error, len(c.errors))
	copy(stats.Errors, c.errors)
	c.errors = c.errors[:0]
	c.errorsMu.Unlock()

	c.lastStatsTime = now

	c.mu.Lock()
	c.statsHistory = append(c.statsHistory, stats)
	if len(c.statsHistory) > c.maxHistory {
		c.statsHistory = c.statsHistory[1:]
	}
	c.mu.Unlock()

	return stats
}

func (c *Collector) calculateLatencyStats(latencies []time.Duration) LatencyStats {
	if len(latencies) == 0 {
		return LatencyStats{}
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	stats := LatencyStats{
		Min:   latencies[0],
		Max:   latencies[len(latencies)-1],
		Count: len(latencies),
	}

	var sum time.Duration
	for _, l := range latencies {
		sum += l
	}
	stats.Mean = sum / time.Duration(len(latencies))

	p99Index := int(float64(len(latencies)) * 0.99)
	if p99Index >= len(latencies) {
		p99Index = len(latencies) - 1
	}
	stats.P99 = latencies[p99Index]

	return stats
}

func (c *Collector) Start(ctx context.Context) {
	ticker := time.NewTicker(c.config.StatsInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := c.CollectStats()
			if err := c.storage.WriteStats(stats); err != nil {
				c.logger.Error("Failed to write stats", zap.Error(err))
			}
		}
	}
}

func (c *Collector) GetHistory() []Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	history := make([]Stats, len(c.statsHistory))
	copy(history, c.statsHistory)
	return history
}

func (c *Collector) WriteFailure(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	failureStats := Stats{
		Timestamp: time.Now(),
		Errors:    []error{err},
	}

	if err := c.storage.WriteFailure(failureStats); err != nil {
		c.logger.Error("Failed to write failure stats", zap.Error(err))
	}
}

func FormatLatency(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	return fmt.Sprintf("%.2f", ms)
}
