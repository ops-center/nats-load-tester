package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

type PublisherConfig struct {
	ID             string
	StreamName     string
	Subject        string
	MessageSize    int
	PublishRate    int
	TrackLatency   bool
	PublishPattern string
	UseJetStream   bool
}

type Publisher struct {
	nc             *nats.Conn
	js             nats.JetStreamContext
	config         PublisherConfig
	statsCollector StatsRecorder
	logger         *zap.Logger
	messageData    []byte
	currentRate    int
	targetRate     int
}

type StatsRecorder interface {
	RecordPublish()
	RecordPublishError(error)
	RecordConsume()
	RecordConsumeError(error)
	RecordLatency(time.Duration)
	RecordError(error)
}

func NewPublisher(nc *nats.Conn, js nats.JetStreamContext, cfg PublisherConfig, stats StatsRecorder, logger *zap.Logger) *Publisher {
	messageData := make([]byte, cfg.MessageSize)
	if cfg.MessageSize > 8 {
		for i := 8; i < cfg.MessageSize; i++ {
			messageData[i] = byte(rand.Intn(256))
		}
	}

	return &Publisher{
		nc:             nc,
		js:             js,
		config:         cfg,
		statsCollector: stats,
		logger:         logger,
		messageData:    messageData,
		currentRate:    1, // Start with low rate for ramp-up
		targetRate:     cfg.PublishRate,
	}
}

func (p *Publisher) Start(ctx context.Context) error {
	p.logger.Info("Starting publisher",
		zap.String("id", p.config.ID),
		zap.String("subject", p.config.Subject),
		zap.Int("rate", p.config.PublishRate),
	)

	// Start with current rate, will be updated during ramp-up
	interval := time.Second / time.Duration(p.currentRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var burstSize int
	if p.config.PublishPattern == "burst" {
		burstSize = max(p.currentRate/10, 1)
	}
	randomDelay := time.Duration(rand.Intn(2000)) * time.Millisecond / time.Duration(p.currentRate)
	time.Sleep(randomDelay)

	lastRate := p.currentRate

	for {
		// Check if rate changed and update ticker if needed
		if p.currentRate != lastRate {
			lastRate = p.currentRate
			ticker.Stop()
			interval = time.Second / time.Duration(p.currentRate)
			ticker = time.NewTicker(interval)

			if p.config.PublishPattern == "burst" {
				burstSize = max(p.currentRate/10, 1)
			}
		}

		if p.currentRate > 0 && p.config.PublishPattern == "random" {
			ticker.Stop()
			ticker = time.NewTicker(time.Duration(rand.Intn(2000)) * time.Millisecond / time.Duration(p.currentRate))
		}

		select {
		case <-ctx.Done():
			p.logger.Info("Publisher stopping", zap.String("id", p.config.ID))
			return nil

		case <-ticker.C:
			switch p.config.PublishPattern {
			case "steady":
				if err := p.publishMessage(); err != nil {
					p.logger.Error("Failed to publish", zap.Error(err))
					p.statsCollector.RecordPublishError(err)
				}
			case "burst":
				for i := 0; i < burstSize; i++ {
					if err := p.publishMessage(); err != nil {
						p.logger.Error("Failed to publish", zap.Error(err))
						p.statsCollector.RecordPublishError(err)
					}
				}
			case "random":
				if err := p.publishMessage(); err != nil {
					p.logger.Error("Failed to publish", zap.Error(err))
					p.statsCollector.RecordPublishError(err)
				}
			default:
				return fmt.Errorf("unknown publish pattern: %s", p.config.PublishPattern)
			}
		}

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

	p.statsCollector.RecordPublish()
	return nil
}

// SetRate updates the current publishing rate
func (p *Publisher) SetRate(rate int) {
	if rate < 1 {
		rate = 1
	}
	if rate > p.targetRate {
		rate = p.targetRate
	}
	p.currentRate = rate
	p.logger.Debug("Publisher rate updated",
		zap.String("id", p.config.ID),
		zap.Int("new_rate", rate),
		zap.Int("target_rate", p.targetRate),
	)
}

// GetCurrentRate returns the current publishing rate
func (p *Publisher) GetCurrentRate() int {
	return p.currentRate
}

// GetTargetRate returns the target publishing rate
func (p *Publisher) GetTargetRate() int {
	return p.targetRate
}
