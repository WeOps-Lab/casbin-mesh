// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package utils

import (
	"context"
	"fmt"
	"time"
)

// RetryConfig holds configuration for retry policies
type RetryConfig struct {
	MaxAttempts   int           // Maximum number of retry attempts
	InitialDelay  time.Duration // Initial delay between retries
	MaxDelay      time.Duration // Maximum delay between retries
	BackoffFactor float64       // Factor to multiply delay by after each attempt
	EnableJitter  bool          // Whether to add random jitter to delays
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		BackoffFactor: 2.0,
		EnableJitter:  true,
	}
}

// RetryableFunc represents a function that can be retried
type RetryableFunc func() error

// RetryWithConfig executes a function with the specified retry configuration
func RetryWithConfig(ctx context.Context, config *RetryConfig, fn RetryableFunc) error {
	var lastErr error
	delay := config.InitialDelay

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		// Check if context was cancelled
		select {
		case <-ctx.Done():
			return fmt.Errorf("operation cancelled: %v", ctx.Err())
		default:
		}

		// Execute the function
		err := fn()
		if err == nil {
			return nil // Success!
		}

		lastErr = err

		// If this is the last attempt, don't wait
		if attempt == config.MaxAttempts-1 {
			break
		}

		// Calculate next delay with backoff
		nextDelay := time.Duration(float64(delay) * config.BackoffFactor)
		if nextDelay > config.MaxDelay {
			nextDelay = config.MaxDelay
		}

		// Add jitter if enabled (up to 25% of delay)
		if config.EnableJitter {
			jitter := time.Duration(float64(delay) * 0.25)
			nextDelay += time.Duration(int64(jitter) % int64(delay/4))
		}

		// Wait before next attempt
		select {
		case <-ctx.Done():
			return fmt.Errorf("operation cancelled during retry wait: %v", ctx.Err())
		case <-time.After(nextDelay):
		}

		delay = nextDelay
	}

	return fmt.Errorf("operation failed after %d attempts: %v", config.MaxAttempts, lastErr)
}

// Retry executes a function with default retry configuration
func Retry(ctx context.Context, fn RetryableFunc) error {
	return RetryWithConfig(ctx, DefaultRetryConfig(), fn)
}

// TimeoutConfig holds configuration for timeout policies
type TimeoutConfig struct {
	ConnectTimeout time.Duration // Timeout for establishing connections
	ReadTimeout    time.Duration // Timeout for reading responses
	WriteTimeout   time.Duration // Timeout for writing requests
	IdleTimeout    time.Duration // Timeout for idle connections
}

// DefaultTimeoutConfig returns sensible default timeout configuration
func DefaultTimeoutConfig() *TimeoutConfig {
	return &TimeoutConfig{
		ConnectTimeout: 10 * time.Second,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    90 * time.Second,
	}
}

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState int

const (
	CircuitClosed CircuitBreakerState = iota
	CircuitOpen
	CircuitHalfOpen
)

func (s CircuitBreakerState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreakerConfig holds configuration for circuit breaker
type CircuitBreakerConfig struct {
	FailureThreshold int           // Number of failures before opening circuit
	SuccessThreshold int           // Number of successes needed to close circuit in half-open state
	Timeout          time.Duration // Time to wait before transitioning from open to half-open
	MaxRequests      int           // Maximum requests allowed in half-open state
}

// DefaultCircuitBreakerConfig returns sensible defaults for circuit breaker
func DefaultCircuitBreakerConfig() *CircuitBreakerConfig {
	return &CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		Timeout:          60 * time.Second,
		MaxRequests:      10,
	}
}

// CircuitBreaker implements a circuit breaker pattern
type CircuitBreaker struct {
	config       *CircuitBreakerConfig
	state        CircuitBreakerState
	failures     int
	successes    int
	requests     int
	lastFailTime time.Time
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration
func NewCircuitBreaker(config *CircuitBreakerConfig) *CircuitBreaker {
	if config == nil {
		config = DefaultCircuitBreakerConfig()
	}
	return &CircuitBreaker{
		config: config,
		state:  CircuitClosed,
	}
}

// Execute runs a function through the circuit breaker
func (cb *CircuitBreaker) Execute(fn RetryableFunc) error {
	switch cb.state {
	case CircuitOpen:
		// Check if we should transition to half-open
		if time.Since(cb.lastFailTime) >= cb.config.Timeout {
			cb.state = CircuitHalfOpen
			cb.requests = 0
			cb.successes = 0
		} else {
			return fmt.Errorf("circuit breaker is open")
		}

	case CircuitHalfOpen:
		// Limit requests in half-open state
		if cb.requests >= cb.config.MaxRequests {
			return fmt.Errorf("circuit breaker is half-open and at request limit")
		}
		cb.requests++
	}

	// Execute the function
	err := fn()

	if err != nil {
		cb.onFailure()
		return err
	}

	cb.onSuccess()
	return nil
}

// onFailure handles a failed execution
func (cb *CircuitBreaker) onFailure() {
	cb.failures++
	cb.lastFailTime = time.Now()

	switch cb.state {
	case CircuitClosed:
		if cb.failures >= cb.config.FailureThreshold {
			cb.state = CircuitOpen
		}
	case CircuitHalfOpen:
		cb.state = CircuitOpen
		cb.requests = 0
		cb.successes = 0
	}
}

// onSuccess handles a successful execution
func (cb *CircuitBreaker) onSuccess() {
	switch cb.state {
	case CircuitClosed:
		cb.failures = 0 // Reset failure count on success

	case CircuitHalfOpen:
		cb.successes++
		if cb.successes >= cb.config.SuccessThreshold {
			cb.state = CircuitClosed
			cb.failures = 0
			cb.successes = 0
			cb.requests = 0
		}
	}
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	return cb.state
}

// GetStats returns current statistics of the circuit breaker
func (cb *CircuitBreaker) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"state":        cb.state.String(),
		"failures":     cb.failures,
		"successes":    cb.successes,
		"requests":     cb.requests,
		"last_failure": cb.lastFailTime.Format(time.RFC3339),
	}
}
