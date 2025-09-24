package engine

import (
	"context"
	"time"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
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
