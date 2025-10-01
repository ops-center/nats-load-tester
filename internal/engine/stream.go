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
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opscenter.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
)

// StreamManager handles JetStream stream operations
type StreamManager struct {
	js     jetstream.StreamManager
	logger *zap.Logger
}

// NewStreamManager creates a new StreamManager
func NewStreamManager(js jetstream.StreamManager, logger *zap.Logger) *StreamManager {
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

	streamCount := 0

	for _, streamSpec := range loadTestSpec.Streams {
		streamNames := streamSpec.GetFormattedStreamNames()
		for streamIndex, streamName := range streamNames {
			subjects := streamSpec.GetFormattedSubjects(int32(streamIndex + 1))

			streamConfig := jetstream.StreamConfig{
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

			if err := exponentialBackoff(ctx, 1*time.Second, 1.5, 5, 5*time.Second, func() error {
				_, err := sm.js.CreateOrUpdateStream(ctx, streamConfig)
				if err == nil {
					return nil
				}

				sm.logger.Warn("CreateOrUpdateStream failed, attempting delete and recreate",
					zap.String("name", streamName),
					zap.Error(err))

				if delErr := sm.js.DeleteStream(ctx, streamName); delErr != nil && !errors.Is(delErr, jetstream.ErrStreamNotFound) {
					return fmt.Errorf("failed to delete stream %s: %w", streamName, delErr)
				}

				_, createErr := sm.js.CreateStream(ctx, streamConfig)
				if createErr != nil {
					return fmt.Errorf("failed to recreate stream %s: %w", streamName, createErr)
				}

				sm.logger.Warn("Deleted and recreated stream", zap.String("name", streamName))
				return nil
			}); err != nil {
				return err
			}

			streamCount++
		}
	}

	sm.logger.Info("Setup streams completed", zap.Int("total_streams", streamCount))

	return nil
}

// CleanupStreams removes streams created during the test
func (sm *StreamManager) CleanupStreams(cleanupCtx context.Context, loadTestSpec *config.LoadTestSpec) error {
	for _, streamSpec := range loadTestSpec.Streams {
		streamNames := streamSpec.GetFormattedStreamNames()
		for _, streamName := range streamNames {
			err := sm.js.DeleteStream(cleanupCtx, streamName)
			if err != nil && err != jetstream.ErrStreamNotFound {
				sm.logger.Warn("Failed to delete stream", zap.String("name", streamName), zap.Error(err))
			} else if err == nil {
				sm.logger.Info("Deleted stream", zap.String("name", streamName))
			}
		}
	}

	return nil
}
