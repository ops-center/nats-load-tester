package engine

import (
	"context"
	"fmt"
	"time"
)

// a simplified and modified knockoff of 'ExponentialBackoff' from k8s.io/apimachinery/pkg/util/wait.
// retries fn() until it returns a nil error, or the max number of steps is reached
func exponentialBackoff(ctx context.Context, duration time.Duration, factor float64, steps int, cap time.Duration, fn func() error) error {
	if steps <= 0 {
		return fmt.Errorf("attempts cannot be negative")
	}
	if duration <= 0 {
		return fmt.Errorf("duration must be greater than zero")
	}
	if factor <= 0 {
		return fmt.Errorf("factor must be greater than zero")
	}

	currentDuration := duration

	for i := range steps {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := fn(); err != nil {
			if i == steps-1 {
				return err
			}

			currentDuration = min(time.Duration(float64(currentDuration)*factor), cap)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(currentDuration):
			}

			continue
		}
		return nil
	}

	return nil
}
