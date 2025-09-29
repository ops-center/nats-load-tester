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
	"time"

	"go.opscenter.dev/nats-load-tester/internal/config"
)

// PublisherInterface defines the interface for message publishers
type PublisherInterface interface {
	Start(ctx context.Context) error
	GetTargetRate() int32
	GetCurrentRate() int32
	SetRate(rate int32)
	GetID() string
	GetStreamName() string
	GetSubject() string
}

// ConsumerInterface defines the interface for message consumers
type ConsumerInterface interface {
	Start(ctx context.Context) error
	Cleanup() error
	GetID() string
	GetStreamName() string
	GetSubject() string
}

// StreamManagerInterface defines the interface for stream management
type StreamManagerInterface interface {
	SetupStreams(ctx context.Context, loadTestSpec *config.LoadTestSpec) error
	CleanupStreams(ctx context.Context, loadTestSpec *config.LoadTestSpec) error
}

// RampUpControllerInterface manages the ramp-up process
type RampUpControllerInterface interface {
	Start(ctx context.Context, publishers []PublisherInterface, duration time.Duration) error
}
