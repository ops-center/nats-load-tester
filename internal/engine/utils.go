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
