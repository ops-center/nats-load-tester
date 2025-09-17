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
	}
}

func (p *Publisher) Start(ctx context.Context) error {
	p.logger.Info("Starting publisher",
		zap.String("id", p.config.ID),
		zap.String("subject", p.config.Subject),
		zap.Int("rate", p.config.PublishRate),
	)

	interval := time.Second / time.Duration(p.config.PublishRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var burstSize int
	var burstTicker *time.Ticker

	if p.config.PublishPattern == "burst" {
		burstSize = p.config.PublishRate / 10
		if burstSize < 1 {
			burstSize = 1
		}
		burstTicker = time.NewTicker(100 * time.Millisecond)
		defer burstTicker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("Publisher stopping", zap.String("id", p.config.ID))
			return nil

		case <-ticker.C:
			if p.config.PublishPattern == "steady" {
				if err := p.publishMessage(); err != nil {
					p.logger.Error("Failed to publish", zap.Error(err))
					p.statsCollector.RecordPublishError(err)
				}
			}

		case <-func() <-chan time.Time {
			if burstTicker != nil {
				return burstTicker.C
			}
			return nil
		}():
			if p.config.PublishPattern == "burst" {
				for i := 0; i < burstSize; i++ {
					if err := p.publishMessage(); err != nil {
						p.logger.Error("Failed to publish", zap.Error(err))
						p.statsCollector.RecordPublishError(err)
					}
				}
			}
		}

		if p.config.PublishPattern == "random" {
			randomDelay := time.Duration(rand.Intn(2000)) * time.Millisecond / time.Duration(p.config.PublishRate)
			time.Sleep(randomDelay)
			if err := p.publishMessage(); err != nil {
				p.logger.Error("Failed to publish", zap.Error(err))
				p.statsCollector.RecordPublishError(err)
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
