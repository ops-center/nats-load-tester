package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
)

type PublisherConfig struct {
	ID               string
	StreamName       string
	Subject          string
	MessageSize      int32
	PublishRate      int32
	TrackLatency     bool // TODO: track latency between publishes/acks
	PublishPattern   string
	PublishBurstSize int32
	UseJetStream     bool
}

type Publisher struct {
	nc            *nats.Conn
	js            nats.JetStreamContext
	config        PublisherConfig
	statsRecorder statsCollector
	logger        *zap.Logger
	messageData   []byte
	currentRate   atomic.Int32
	targetRate    int32
}

type statsCollector interface {
	RecordPublish()
	RecordPublishError(error)
	RecordConsume()
	RecordConsumeError(error)
	RecordLatency(time.Duration)
	RecordError(error)
}

func NewPublisher(nc *nats.Conn, js nats.JetStreamContext, cfg PublisherConfig, statsCollector statsCollector, logger *zap.Logger) *Publisher {
	messageData := make([]byte, cfg.MessageSize)
	if cfg.MessageSize > 8 {
		for i := int32(8); i < cfg.MessageSize; i++ {
			messageData[i] = byte(rand.Intn(256))
		}
	}

	return &Publisher{
		nc:            nc,
		js:            js,
		config:        cfg,
		statsRecorder: statsCollector,
		logger:        logger,
		messageData:   messageData,
		currentRate:   atomic.Int32{},
		targetRate:    cfg.PublishRate,
	}
}

func (p *Publisher) Start(ctx context.Context) error {
	// Initial random delay to spread out publishers
	time.Sleep(time.Duration(rand.Int63n(int64(100 * time.Millisecond))))

	p.logger.Info("Starting publisher",
		zap.String("id", p.config.ID),
		zap.String("subject", p.config.Subject),
		zap.Int32("rate", p.config.PublishRate),
	)

	// Start with a rate of 1/s, will be updated during ramp-up
	p.currentRate.Store(1)

	initialInterval, err := p.calculatePublishInterval(p.GetCurrentRate())
	if err != nil {
		p.logger.Error("failed to calculate initial interval", zap.Error(err))
		return err
	}

	ticker := time.NewTicker(initialInterval)

	lastRate := p.GetCurrentRate()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("Publisher stopping", zap.String("id", p.config.ID))
			return nil

		case <-ticker.C:
			switch p.config.PublishPattern {
			case config.PublishPatternSteady, config.PublishPatternRandom:
				for range p.config.PublishBurstSize {
					if err := p.publishMessage(); err != nil {
						p.logger.Error("failed to publish", zap.Error(err))
						p.statsRecorder.RecordPublishError(err)
					}
				}
				currentRate := p.GetCurrentRate()
				if currentRate != lastRate {
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

func (p *Publisher) updateTicker(ticker *time.Ticker, currentRate int32) error {
	interval, err := p.calculatePublishInterval(currentRate)
	if err != nil {
		return err
	}
	ticker.Reset(interval)
	return nil
}

func (p *Publisher) calculatePublishInterval(currentRate int32) (time.Duration, error) {
	if currentRate <= 0 {
		return 0, fmt.Errorf("current rate is %d, cannot calculate interval", currentRate)
	}

	switch p.config.PublishPattern {
	case config.PublishPatternSteady:
		return time.Second / time.Duration(currentRate), nil
	case config.PublishPatternRandom:
		randomFactor := time.Duration(rand.Intn(1000)+500) * time.Millisecond
		return randomFactor / time.Duration(currentRate), nil
	default:
		return 0, fmt.Errorf("unknown publish pattern: %s", p.config.PublishPattern)
	}
}

func (p *Publisher) publishMessage() error {
	message := make([]byte, len(p.messageData))
	copy(message, p.messageData)

	if p.config.TrackLatency && len(message) >= 8 {
		timestamp := time.Now().UnixNano()
		binary.LittleEndian.PutUint64(message[:8], uint64(timestamp))
	}

	var err error
	if p.config.UseJetStream && p.js != nil {
		_, err = p.js.Publish(p.config.Subject, message)
	} else {
		err = p.nc.Publish(p.config.Subject, message)
	}

	if err != nil {
		return fmt.Errorf("publish failed: %w", err)
	}

	p.statsRecorder.RecordPublish()
	return nil
}

// SetRate updates the current publishing rate
func (p *Publisher) SetRate(rate int32) {
	rate = max(rate, 1)
	rate = min(rate, p.targetRate)
	p.currentRate.Store(int32(rate))
	p.logger.Debug("Publisher rate updated",
		zap.String("id", p.config.ID),
		zap.Int32("new_rate", rate),
		zap.Int32("target_rate", p.targetRate),
	)
}

// GetCurrentRate returns the current publishing rate
func (p *Publisher) GetCurrentRate() int32 {
	return p.currentRate.Load()
}

// GetTargetRate returns the target publishing rate
func (p *Publisher) GetTargetRate() int32 {
	return p.targetRate
}
