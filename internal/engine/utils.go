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
	"sync/atomic"
	"time"
)

var errCircuitOpen = errors.New("circuit breaker is open")

type circuitBreaker interface {
	call(fn func() error) error
	isOpen() bool
	reset()
}

type simpleCircuitBreaker struct {
	failures     atomic.Int32
	lastFailure  atomic.Int64
	isOpenFlag   atomic.Bool
	maxFailures  int32
	resetTimeout time.Duration
}

func newCircuitBreaker(maxFailures int, resetTimeout time.Duration) circuitBreaker {
	return &simpleCircuitBreaker{
		maxFailures:  int32(maxFailures),
		resetTimeout: resetTimeout,
	}
}

// call executes the provided function if the circuit breaker is closed.
// If the circuit breaker is open, it returns an error without executing the function.
// If the function returns an error, it increments the failure count and may open the circuit.
// If the function succeeds, it resets the failure count and closes the circuit.
func (cb *simpleCircuitBreaker) call(fn func() error) error {
	if cb.isOpenFlag.Load() {
		if time.Since(time.Unix(0, cb.lastFailure.Load())) <= cb.resetTimeout {
			return errCircuitOpen
		}
		cb.reset()
	}

	err := fn()

	if err != nil {
		cb.failures.Add(1)
		cb.lastFailure.Store(time.Now().UnixNano())
		if cb.failures.Load() >= cb.maxFailures {
			cb.isOpenFlag.Store(true)
		}
	} else {
		cb.failures.Store(0)
		cb.isOpenFlag.Store(false)
	}

	return err
}

func (cb *simpleCircuitBreaker) isOpen() bool {
	return cb.isOpenFlag.Load()
}

func (cb *simpleCircuitBreaker) reset() {
	cb.failures.Store(0)
	cb.isOpenFlag.Store(false)
}

// a simplified and modified knockoff of 'ExponentialBackoff' from k8s.io/apimachinery/pkg/util/wait.
// retries fn() until it returns a nil error, or the max number of steps is reached
func exponentialBackoff(ctx context.Context, duration time.Duration, factor float64, steps int, cap time.Duration, fn func() error) error {
	var paramErrs []error
	if steps <= 0 {
		paramErrs = append(paramErrs, fmt.Errorf("steps must be greater than zero"))
	}
	if duration <= 0 {
		paramErrs = append(paramErrs, fmt.Errorf("duration must be greater than zero"))
	}
	if factor <= 0 {
		paramErrs = append(paramErrs, fmt.Errorf("factor must be greater than zero"))
	}
	if len(paramErrs) > 0 {
		return errors.Join(paramErrs...)
	}

	currentDuration := duration

	for i := range steps {
		select {
		case <-ctx.Done():
			return nil
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
