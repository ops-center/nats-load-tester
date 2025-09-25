package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
)

// StreamManager handles JetStream stream operations
type StreamManager struct {
	js     nats.JetStreamContext
	logger *zap.Logger
}

// NewStreamManager creates a new StreamManager
func NewStreamManager(js nats.JetStreamContext, logger *zap.Logger) *StreamManager {
	return &StreamManager{
		js:     js,
		logger: logger,
	}
}

// SetupStreams creates or updates streams based on the load test spec
func (sm *StreamManager) SetupStreams(ctx context.Context, loadTestSpec *config.LoadTestSpec) error {
	if ctx == nil {
		sm.logger.Error("context is nil")
		return fmt.Errorf("context is nil")
	}

	for _, streamSpec := range loadTestSpec.Streams {
		streamNames := streamSpec.GetFormattedStreamNames()
		for streamIndex, streamName := range streamNames {
			subjects := streamSpec.GetFormattedSubjects(int32(streamIndex + 1))

			streamConfig := &nats.StreamConfig{
				Name:     streamName,
				Subjects: subjects,
				Replicas: int(streamSpec.Replicas),

				Retention:            streamSpec.GetRetentionPolicy(),
				MaxAge:               streamSpec.GetMaxAge(),
				Storage:              streamSpec.GetStorageType(),
				DiscardNewPerSubject: streamSpec.GetDiscardNewPerSubject(),
				Discard:              streamSpec.GetDiscardPolicy(),
				MaxMsgs:              streamSpec.GetMaxMsgs(),
				MaxBytes:             streamSpec.GetMaxBytes(),
				MaxMsgsPerSubject:    streamSpec.GetMaxMsgsPerSubject(),
				MaxConsumers:         streamSpec.GetMaxConsumers(),
			}

			var stream *nats.StreamInfo
			var err error
			if err := exponentialBackoff(ctx, 1*time.Second, 1.5, 5, 5, func() error {
				stream, err = sm.js.StreamInfo(streamName)
				if err != nil && err != nats.ErrStreamNotFound {
					return fmt.Errorf("failed to get stream info: %w", err)
				}
				return nil
			}); err != nil {
				return err
			}

			if err = exponentialBackoff(ctx, 1*time.Second, 1.5, 5, 5, func() error {
				if stream == nil {
					_, err = sm.js.AddStream(streamConfig)
					if err != nil {
						return fmt.Errorf("failed to create stream %s: %w", streamName, err)
					}
					sm.logger.Info("Created stream", zap.String("name", streamName))
				} else {
					_, err = sm.js.UpdateStream(streamConfig)
					if err != nil {
						return fmt.Errorf("failed to update stream %s: %w", streamName, err)
					}
					sm.logger.Info("Updated stream", zap.String("name", streamName))
				}
				return nil
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

// CleanupStreams removes streams created during the test
func (sm *StreamManager) CleanupStreams(loadTestSpec *config.LoadTestSpec) error {
	for _, streamSpec := range loadTestSpec.Streams {
		streamNames := streamSpec.GetFormattedStreamNames()
		for _, streamName := range streamNames {

			err := sm.js.DeleteStream(streamName)
			if err != nil && err != nats.ErrStreamNotFound {
				sm.logger.Warn("Failed to delete stream", zap.String("name", streamName), zap.Error(err))
			} else if err == nil {
				sm.logger.Info("Deleted stream", zap.String("name", streamName))
			}
		}
	}

	return nil
}
