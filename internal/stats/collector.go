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
	"math/rand/v2"
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
	latencyIndex  int
	errors        []error
	startTime     time.Time
	lastStatsTime time.Time

	currentLoadTestSpecHash string

	rampUpStats RampUpStats

	maxLatencySamples int
	stopped           atomic.Bool

	// Reusable buffer for latency calculations to avoid allocations
	validLatenciesBuffer []time.Duration
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
	P50   time.Duration
	P90   time.Duration
	P99   time.Duration
	Count int
}

func NewCollector(logger *zap.Logger, storage Storage) *Collector {
	maxSamples := 5000
	return &Collector{
		logger:            logger,
		storage:           storage,
		latencyIndex:      0,
		errors:            make([]error, 0),
		startTime:         time.Now(),
		lastStatsTime:     time.Now(),
		maxLatencySamples: maxSamples,
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
	c.startTime = time.Now()
	c.lastStatsTime = time.Now()

	if loadTestSpec.LogLimits.MaxLatencySamples > 0 {
		c.maxLatencySamples = int(loadTestSpec.LogLimits.MaxLatencySamples)
	} else {
		c.maxLatencySamples = 10000
	}

	c.latencies = make([]time.Duration, c.maxLatencySamples)
	c.latencyIndex = 0
	c.errors = nil

	return nil
}

func (c *Collector) RecordPublish() {
	c.published.Add(1)
}

func (c *Collector) RecordPublishError(err error) {
	if c.stopped.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publishErrors.Add(1)
	c.recordError(err)
}

func (c *Collector) RecordConsume() {
	c.consumed.Add(1)
}

func (c *Collector) RecordConsumeError(err error) {
	if c.stopped.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consumeErrors.Add(1)
	c.recordError(err)
}

func (c *Collector) RecordError(err error) {
	if c.stopped.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recordError(err)
}

func (c *Collector) recordError(err error) {
	if c.loadTestSpec == nil {
		c.logger.Error("Cannot record error: loadTestSpec not set. Call SetConfig() first.", zap.Error(err))
		return
	}

	if len(c.errors) >= int(c.loadTestSpec.LogLimits.MaxLines) {
		c.errors = c.errors[1:]
	}

	currentSize := 0
	for _, e := range c.errors {
		if e != nil {
			currentSize += len(e.Error())
		}
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
	if c.stopped.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.latencies) == 0 {
		return
	}

	c.latencies[c.latencyIndex] = latency
	c.latencyIndex = (c.latencyIndex + 1) % len(c.latencies)
}

func (c *Collector) CollectStats() *Stats {
	if c.stopped.Load() {
		return nil
	}
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

	if c.latencyIndex > 0 {
		if cap(c.validLatenciesBuffer) < len(c.latencies) {
			c.validLatenciesBuffer = make([]time.Duration, 0, len(c.latencies))
		}
		c.validLatenciesBuffer = c.validLatenciesBuffer[:0]

		for i := 0; i < len(c.latencies); i++ {
			if c.latencies[i] != 0 {
				c.validLatenciesBuffer = append(c.validLatenciesBuffer, c.latencies[i])
			}
			c.latencies[i] = 0
		}
		if len(c.validLatenciesBuffer) > 0 {
			stats.Latency = c.calculateLatencyStats(c.validLatenciesBuffer)
		}
		c.latencyIndex = 0
	}

	stats.Errors = make([]error, len(c.errors))
	copy(stats.Errors, c.errors)
	c.errors = nil

	stats.RampUp = c.rampUpStats
	c.lastStatsTime = now

	if c.currentLoadTestSpecHash != "" && c.storage != nil {
		if err := c.storage.WriteStats(c.loadTestSpec, stats); err != nil {
			c.logger.Error("Failed to write stats to storage - stats will be lost",
				zap.Error(err),
				zap.String("config_hash", c.currentLoadTestSpecHash),
				zap.Uint64("published", stats.Published),
				zap.Uint64("consumed", stats.Consumed))
		}
	}

	return &stats
}

func (c *Collector) calculateLatencyStats(latencies []time.Duration) LatencyStats {
	if len(latencies) == 0 {
		return LatencyStats{}
	}

	stats := LatencyStats{
		Count: len(latencies),
	}

	min_l := latencies[0]
	max_l := latencies[0]
	var sum time.Duration

	for _, l := range latencies {
		min_l = min(min_l, l)
		max_l = max(max_l, l)
		sum += l
	}

	stats.Min = min_l
	stats.Max = max_l
	stats.Mean = sum / time.Duration(len(latencies))

	workingCopy := make([]time.Duration, len(latencies))
	copy(workingCopy, latencies)
	for i := len(workingCopy) - 1; i > 0; i-- {
		j := rand.Int32N(int32(i + 1))
		workingCopy[i], workingCopy[j] = workingCopy[j], workingCopy[i]
	}

	p99Index := int(float64(len(latencies)) * 0.99)
	if p99Index >= len(latencies) {
		p99Index = len(latencies) - 1
	}
	stats.P99 = quickSelect(workingCopy, p99Index)

	p90Index := int(float64(len(latencies)) * 0.90)
	if p90Index >= len(latencies) {
		p90Index = len(latencies) - 1
	}
	stats.P90 = quickSelect(workingCopy, p90Index)

	p50Index := int(float64(len(latencies)) * 0.50)
	if p50Index >= len(latencies) {
		p50Index = len(latencies) - 1
	}
	stats.P50 = quickSelect(workingCopy, p50Index)

	return stats
}

func quickSelect(data []time.Duration, k int) time.Duration {
	left := 0
	right := len(data) - 1

	for left < right {
		pivotValue := data[right]
		pivotIndex := left

		for j := left; j < right; j++ {
			if data[j] < pivotValue {
				data[pivotIndex], data[j] = data[j], data[pivotIndex]
				pivotIndex++
			}
		}
		data[pivotIndex], data[right] = data[right], data[pivotIndex]

		if k == pivotIndex {
			return data[k]
		}
		if k < pivotIndex {
			right = pivotIndex - 1
		} else {
			left = pivotIndex + 1
		}
	}

	return data[left]
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
	if c.stopped.Load() {
		return []Stats{}
	}
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
	if c.stopped.Load() {
		return
	}
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
			c.logger.Error("Failed to write failure stats to storage - failure will be lost",
				zap.Error(writeErr),
				zap.String("config_hash", configHash),
				zap.String("original_error", err.Error()))
		}
	}
}

// SetRampUpStatus sets the current ramp-up status
func (c *Collector) SetRampUpStatus(active bool, currentProgress float64, timeRemaining time.Duration) {
	if c.stopped.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.rampUpStats = RampUpStats{
		IsActive:      active,
		Progress:      currentProgress,
		TimeRemaining: fmt.Sprintf("%.2f", float64(timeRemaining)/float64(time.Millisecond)),
	}
}

// Cleanup releases all resources held by the collector
func (c *Collector) Cleanup() error {
	if c.stopped.Load() {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.stopped.Store(true)

	// Clear all slices to release memory
	c.latencies = nil
	c.errors = nil
	c.validLatenciesBuffer = nil

	// Reset counters
	c.published.Store(0)
	c.consumed.Store(0)
	c.publishErrors.Store(0)
	c.consumeErrors.Store(0)

	// Clear references
	c.loadTestSpec = nil
	c.storage = nil

	return nil
}
