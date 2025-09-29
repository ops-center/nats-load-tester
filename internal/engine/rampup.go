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
	"fmt"
	"time"

	"go.opscenter.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
)

// RampUpManager manages the ramp-up process for publishers
type RampUpManager struct {
	logger         *zap.Logger
	statsCollector *stats.Collector
}

// NewRampUpManager creates a new RampUpController
func NewRampUpManager(logger *zap.Logger, statsCollector *stats.Collector) RampUpControllerInterface {
	return &RampUpManager{
		logger:         logger,
		statsCollector: statsCollector,
	}
}

// Start begins the ramp-up process
func (rum *RampUpManager) Start(ctx context.Context, publishers []PublisherInterface, duration time.Duration) error {
	rum.logger.Info("Starting ramp-up process", zap.Duration("duration", duration))
	if duration == 0 {
		rum.logger.Info("No ramp-up configured, starting at full rate")
		rum.setAllPublishersToFullRate(publishers)
		if rum.statsCollector != nil {
			rum.statsCollector.SetRampUpStatus(false, 0, 0)
		}
		return nil
	}

	rampUpStart := time.Now()
	rampUpTimer := time.NewTimer(duration)
	rampUpTicker := time.NewTicker(time.Second)

	currentProgress := func() float64 {
		// start at 1% minimum
		return max(0.01, min(float64(time.Since(rampUpStart))/float64(duration), 1.0))
	}

	remainingDuration := func() time.Duration {
		return max(duration-time.Since(rampUpStart), 0)
	}

	if rum.statsCollector != nil {
		rum.statsCollector.SetRampUpStatus(true, currentProgress()*100.0, remainingDuration())
	}

	for {
		select {
		case <-ctx.Done():
			rampUpTimer.Stop()
			rampUpTicker.Stop()
			return ctx.Err()

		case <-rampUpTimer.C:
			rum.logger.Info("Ramp-up complete")
			rum.setAllPublishersToFullRate(publishers)
			if rum.statsCollector != nil {
				rum.statsCollector.SetRampUpStatus(false, 100.0, 0)
			}
			rampUpTimer.Stop()
			rampUpTicker.Stop()
			return nil

		case <-rampUpTicker.C:
			progress := currentProgress()
			remaining := remainingDuration()
			rum.updatePublisherRates(publishers, progress, remaining)

			currentRate := int32(0)
			targetRate := int32(0)
			for _, pub := range publishers {
				targetRate += pub.GetTargetRate()
				currentRate += pub.GetCurrentRate()
			}
			if rum.statsCollector != nil {
				rum.statsCollector.SetRampUpStatus(true, progress*100.0, remaining)
			}
		}
	}
}

// setAllPublishersToFullRate sets all publishers to their target rate
func (rum *RampUpManager) setAllPublishersToFullRate(publishers []PublisherInterface) {
	for _, pub := range publishers {
		pub.SetRate(pub.GetTargetRate())
	}
	rum.logger.Info("All publishers set to full rate", zap.Int("publishers", len(publishers)))
}

// updatePublisherRates updates all publisher rates based on ramp-up progress
func (rum *RampUpManager) updatePublisherRates(publishers []PublisherInterface, progress float64, remainingDuration time.Duration) {
	for _, pub := range publishers {
		targetRate := pub.GetTargetRate()
		currentRate := max(1, min(targetRate, int32(progress*float64(targetRate))))
		pub.SetRate(currentRate)
	}

	rum.logger.Debug("Ramp-up progress",
		zap.String("progress", fmt.Sprintf("%.2f%%", progress*100.0)),
		zap.String("remaining_duration", remainingDuration.String()),
		zap.Int("num_publishers", len(publishers)),
	)
}
