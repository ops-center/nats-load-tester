/*
Copyright AppsCode Inc. and Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package stats

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.opscenter.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
)

type Collector struct {
	mu            sync.RWMutex
	logger        *zap.Logger
	loadTestSpec  *config.LoadTestSpec
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

	// Configuration hash tracking for hot reloading
	currentLoadTestSpecHash string

	rampUpStats RampUpStats

	maxLatencySamples int
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
	RampUp          RampUpStats
}

type RampUpStats struct {
	IsActive      bool    `json:"is_active"`
	Progress      float64 `json:"progress"`       // 0.0 to 1.0
	TimeRemaining string  `json:"time_remaining"` // Formatted duration remaining
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
		logger:            logger,
		storage:           storage,
		latencies:         make([]time.Duration, 0, 10000),
		errors:            make([]error, 0),
		startTime:         time.Now(),
		lastStatsTime:     time.Now(),
		maxLatencySamples: 10000,
	}
}

func (c *Collector) SetConfig(loadTestSpec *config.LoadTestSpec) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if loadTestSpec == nil {
		return fmt.Errorf("load test spec is nil")
	}
	c.currentLoadTestSpecHash = loadTestSpec.Hash()
	c.loadTestSpec = loadTestSpec

	c.published.Store(0)
	c.consumed.Store(0)
	c.publishErrors.Store(0)
	c.consumeErrors.Store(0)
	c.latencies = c.latencies[:0]
	c.errors = c.errors[:0]
	c.startTime = time.Now()
	c.lastStatsTime = time.Now()
	c.maxLatencySamples = 10000

	if loadTestSpec.LogLimits.MaxLines > 0 {
		c.maxLatencySamples = min(int(loadTestSpec.LogLimits.MaxLines)*10, 10000)
	}

	return nil
}

func (c *Collector) RecordPublish() {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.published.Add(1)
}

func (c *Collector) RecordPublishError(err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.publishErrors.Add(1)
	c.recordError(err)
}

func (c *Collector) RecordConsume() {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.consumed.Add(1)
}

func (c *Collector) RecordConsumeError(err error) {
	c.mu.RLock()
	c.consumeErrors.Add(1)
	c.mu.RUnlock()

	c.recordError(err)
}

func (c *Collector) RecordError(err error) {
	c.recordError(err)
}

func (c *Collector) recordError(err error) {
	c.errorsMu.Lock()
	defer c.errorsMu.Unlock()

	if c.loadTestSpec == nil {
		panic("loadTestSpec is nil - SetConfig must be called before recording errors")
	}

	if len(c.errors) >= int(c.loadTestSpec.LogLimits.MaxLines) {
		c.errors = c.errors[1:]
	}

	currentSize := 0
	for _, e := range c.errors {
		currentSize += len(e.Error())
	}

	newErrorSize := len(err.Error())
	if currentSize+newErrorSize > int(c.loadTestSpec.LogLimits.MaxBytes) {
		for currentSize+newErrorSize > int(c.loadTestSpec.LogLimits.MaxBytes) && len(c.errors) > 0 {
			currentSize -= len(c.errors[0].Error())
			c.errors = c.errors[1:]
		}
	}

	c.errors = append(c.errors, err)
}

func (c *Collector) RecordLatency(latency time.Duration) {
	c.latencyMu.Lock()
	defer c.latencyMu.Unlock()

	c.latencies = append(c.latencies, latency)

	if len(c.latencies) > c.maxLatencySamples {
		overflow := len(c.latencies) - c.maxLatencySamples
		c.latencies = c.latencies[overflow:]
	}
}

func (c *Collector) CollectStats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

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

	if len(c.latencies) > 0 {
		stats.Latency = c.calculateLatencyStats(c.latencies)
		c.latencies = c.latencies[:0]
	}

	stats.Errors = make([]error, len(c.errors))
	copy(stats.Errors, c.errors)
	c.errors = c.errors[:0]

	stats.RampUp = c.rampUpStats

	c.lastStatsTime = now

	if c.currentLoadTestSpecHash != "" && c.storage != nil {
		if err := c.storage.WriteStats(c.loadTestSpec, stats); err != nil {
			c.logger.Error("Failed to write stats", zap.Error(err))
		}
	}

	return stats
}

func (c *Collector) calculateLatencyStats(latencies []time.Duration) LatencyStats {
	if len(latencies) == 0 {
		return LatencyStats{}
	}

	slices.Sort(latencies)

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

func (c *Collector) Start(ctx context.Context, statsInterval time.Duration) {
	c.logger.Info("Starting stats collector", zap.Duration("interval", statsInterval))
	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.CollectStats()
			return
		case <-ticker.C:
			c.CollectStats()
		}
	}
}

func (c *Collector) GetHistory(limit int) []Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	configHash := c.currentLoadTestSpecHash

	if configHash == "" || c.storage == nil {
		return []Stats{}
	}

	entries, err := c.storage.GetStats(c.loadTestSpec, limit, nil)
	if err != nil {
		c.logger.Error("Failed to get stats history from storage", zap.Error(err))
		return []Stats{}
	}

	result := make([]Stats, len(entries))
	for i, entry := range entries {
		result[i] = entry.Stats
	}
	return result
}

func (c *Collector) WriteFailure(err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	configHash := c.currentLoadTestSpecHash

	failureStats := Stats{
		Timestamp: time.Now(),
		Errors:    []error{err},
	}

	// Write failure directly to storage (no in-memory cache)
	if configHash != "" && c.storage != nil {
		if writeErr := c.storage.WriteFailure(c.loadTestSpec, failureStats); writeErr != nil {
			c.logger.Error("Failed to write failure stats", zap.Error(writeErr))
		}
	}
}

// SetRampUpStatus sets the current ramp-up status
func (c *Collector) SetRampUpStatus(active bool, currentProgress float64, timeRemaining time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.rampUpStats = RampUpStats{
		IsActive:      active,
		Progress:      currentProgress,
		TimeRemaining: FormatLatency(timeRemaining),
	}
}

func FormatLatency(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	return fmt.Sprintf("%.2f", ms)
}
