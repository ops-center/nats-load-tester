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

package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opscenter.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type PublisherConfig struct {
	ID               string
	StreamName       string
	Subject          string
	MessageSize      int32
	PublishRate      int64
	TrackLatency     bool
	PublishPattern   string
	PublishBurstSize int32
	UseJetStream     bool
}

type Publisher struct {
	nc             *nats.Conn
	js             jetstream.JetStream
	config         PublisherConfig
	statsRecorder  statsCollector
	logger         *zap.Logger
	messageData    []byte
	currentRate    atomic.Int64
	targetRate     int64
	stopped        atomic.Bool
	circuitBreaker circuitBreaker
}

type statsCollector interface {
	RecordPublish()
	RecordPublishError(error)
	RecordConsume()
	RecordConsumeError(error)
	RecordLatency(time.Duration)
	RecordError(error)
}

func NewPublisher(nc *nats.Conn, js jetstream.JetStream, cfg PublisherConfig, statsCollector statsCollector, logger *zap.Logger, cb circuitBreaker) PublisherInterface {
	messageData := make([]byte, cfg.MessageSize)
	if cfg.MessageSize > 8 {
		for i := int32(8); i < cfg.MessageSize; i++ {
			messageData[i] = byte(rand.Intn(256))
		}
	}

	return &Publisher{
		nc:             nc,
		js:             js,
		config:         cfg,
		statsRecorder:  statsCollector,
		logger:         logger,
		messageData:    messageData,
		currentRate:    atomic.Int64{},
		targetRate:     cfg.PublishRate,
		circuitBreaker: cb,
	}
}

func (p *Publisher) Start(ctx context.Context) error {
	// Start with a rate of 1/s, will be updated during ramp-up
	p.SetRate(1)

	initialInterval, err := p.calculatePublishInterval(p.GetCurrentRate())
	if err != nil {
		p.logger.Error("failed to calculate initial interval", zap.Error(err))
		return err
	}

	ticker := time.NewTicker(initialInterval)
	defer ticker.Stop()

	lastRate := p.GetCurrentRate()

	for {
		select {
		case <-ctx.Done():
			p.Cleanup()
			return nil

		case <-ticker.C:
			switch p.config.PublishPattern {
			case config.PublishPatternSteady, config.PublishPatternRandom:
				for range p.config.PublishBurstSize {
					if p.stopped.Load() {
						return nil
					}
					if err := p.circuitBreaker.call(func() error {
						return p.publishMessage(ctx)
					}); err != nil {
						if errors.Is(err, errCircuitOpen) {
							p.logger.Debug("circuit breaker open - skipping publish", zap.String("id", p.config.ID))
							p.statsRecorder.RecordPublishError(err)
							continue
						}
						if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
							return nil
						}

						if errors.Is(err, nats.ErrTimeout) {
							p.logger.Warn("publish timeout - NATS may be overloaded", zap.String("id", p.config.ID))
						} else if errors.Is(err, nats.ErrConnectionClosed) {
							p.logger.Warn("publish failed - connection closed", zap.String("id", p.config.ID))
						} else if errors.Is(err, nats.ErrSlowConsumer) {
							p.logger.Warn("slow consumer detected - NATS backpressure", zap.String("id", p.config.ID))
						} else {
							p.logger.Error("failed to publish", zap.Error(err))
						}
						p.statsRecorder.RecordPublishError(err)
					} else {
						p.statsRecorder.RecordPublish()
					}
				}

				if p.stopped.Load() {
					return nil
				}
				if currentRate := p.GetCurrentRate(); currentRate != lastRate {
					lastRate = currentRate
					if err := p.updateTicker(ticker, currentRate); err != nil {
						p.logger.Error("failed to update ticker", zap.Error(err))
					}
				}
			default:
				return fmt.Errorf("unknown publish pattern: %s", p.config.PublishPattern)
			}
		}
	}
}

func (p *Publisher) updateTicker(ticker *time.Ticker, currentRate int64) error {
	interval, err := p.calculatePublishInterval(currentRate)
	if err != nil {
		return err
	}
	ticker.Reset(interval)
	return nil
}

func (p *Publisher) calculatePublishInterval(currentRate int64) (time.Duration, error) {
	if currentRate <= 0 {
		return 0, fmt.Errorf("current rate is %d, cannot calculate interval", currentRate)
	}

	switch p.config.PublishPattern {
	case config.PublishPatternSteady:
		return max(1, time.Second/time.Duration(currentRate)), nil
	case config.PublishPatternRandom:
		baseInterval := time.Second / time.Duration(currentRate)
		jitterPercent := rand.Intn(100) - 50
		jitter := baseInterval * time.Duration(jitterPercent) / 100
		return max(1, baseInterval+jitter), nil
	default:
		return 0, fmt.Errorf("unknown publish pattern: %s", p.config.PublishPattern)
	}
}

var messageBufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0)
		return &b
	},
}

func (p *Publisher) publishMessage(ctx context.Context) error {
	if p.stopped.Load() {
		return nil
	}
	bufPtr := messageBufferPool.Get().(*[]byte)
	buf := *bufPtr
	if cap(buf) < len(p.messageData) {
		buf = make([]byte, len(p.messageData))
	}
	message := buf[:len(p.messageData)]
	copy(message, p.messageData)

	if p.config.TrackLatency && len(message) >= 8 {
		timestamp := time.Now().UnixNano()
		binary.LittleEndian.PutUint64(message[:8], uint64(timestamp))
	}

	var err error
	if p.config.UseJetStream && p.js != nil {
		_, err = p.js.Publish(ctx, p.config.Subject, message)
	} else {
		err = p.nc.Publish(p.config.Subject, message)
	}

	*bufPtr = buf[:0]
	messageBufferPool.Put(bufPtr)

	if err != nil {
		return fmt.Errorf("publish failed: %w", err)
	}

	return nil
}

// SetRate updates the current publishing rate
func (p *Publisher) SetRate(rate int64) {
	rate = max(rate, 1)
	rate = min(rate, p.targetRate)
	p.currentRate.Store(rate)
}

// GetCurrentRate returns the current publishing rate
func (p *Publisher) GetCurrentRate() int64 {
	return p.currentRate.Load()
}

// GetTargetRate returns the target publishing rate
func (p *Publisher) GetTargetRate() int64 {
	return p.targetRate
}

// GetID returns the publisher's ID
func (p *Publisher) GetID() string {
	return p.config.ID
}

// GetStreamName returns the publisher's stream name
func (p *Publisher) GetStreamName() string {
	return p.config.StreamName
}

// GetSubject returns the publisher's subject
func (p *Publisher) GetSubject() string {
	return p.config.Subject
}

// Cleanup releases all resources held by the publisher
func (p *Publisher) Cleanup() {
	if p.stopped.CompareAndSwap(false, true) {
		return
	}
	p.messageData = nil
	p.currentRate.Store(1)
}

// CreatePublishers creates and starts publishers based on the load test spec and stream configurations
func CreatePublishers(ctx context.Context, nc *nats.Conn, js jetstream.JetStream, loadTestSpec *config.LoadTestSpec, statsCollector statsCollector, logger *zap.Logger, eg *errgroup.Group, cb circuitBreaker) []PublisherInterface {
	publishers := make([]PublisherInterface, 0)
	totalTargetRate := int64(0)

	for _, streamSpec := range loadTestSpec.Streams {
		// Only create publishers for streams that match the publisher's stream name prefix
		if streamSpec.NamePrefix != loadTestSpec.Publishers.StreamNamePrefix {
			continue
		}

		streamNames := streamSpec.GetFormattedStreamNames()
		for streamIndex, streamName := range streamNames {
			subjects := streamSpec.GetFormattedSubjects(int32(streamIndex + 1))

			for subjectIndex, subject := range subjects {
				for j := int32(0); j < loadTestSpec.Publishers.CountPerStream; j++ {
					pubCfg := PublisherConfig{
						ID:               fmt.Sprintf("%s-pub-%d-%d-%d", loadTestSpec.ClientIDPrefix, streamIndex+1, subjectIndex, j+1),
						StreamName:       streamName,
						Subject:          subject,
						MessageSize:      loadTestSpec.Publishers.MessageSizeBytes,
						PublishRate:      loadTestSpec.Publishers.PublishRatePerSecond,
						TrackLatency:     loadTestSpec.Publishers.TrackLatency,
						PublishPattern:   loadTestSpec.Publishers.PublishPattern,
						PublishBurstSize: loadTestSpec.Publishers.PublishBurstSize,
						UseJetStream:     loadTestSpec.UseJetStream,
					}

					publisher := NewPublisher(nc, js, pubCfg, statsCollector, logger, cb)
					publishers = append(publishers, publisher)
					totalTargetRate += publisher.GetTargetRate()

					eg.Go(func() error {
						defer publisher.Cleanup()
						if err := publisher.Start(ctx); err != nil {
							logger.Error("Publisher failed",
								zap.String("id", publisher.GetID()),
								zap.String("stream", publisher.GetStreamName()),
								zap.String("subject", publisher.GetSubject()),
								zap.Error(err))
							statsCollector.RecordError(err)
							return fmt.Errorf("publisher %s failed: %w", publisher.GetID(), err)
						}
						return nil
					})
				}
			}
		}
	}

	logger.Info("Created publishers", zap.Int("count", len(publishers)), zap.Int64("total_target_rate", totalTargetRate))

	return publishers
}
