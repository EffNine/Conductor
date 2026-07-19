package retry

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// Config holds retry configuration
type Config struct {
	MaxRetries           int
	InitialBackoff       time.Duration
	MaxBackoff           time.Duration
	BackoffMultiplier    float64
	RetryableStatusCodes []int
}

// DefaultConfig returns a default retry configuration
func DefaultConfig() Config {
	return Config{
		MaxRetries:           3,
		InitialBackoff:       100 * time.Millisecond,
		MaxBackoff:           5 * time.Second,
		BackoffMultiplier:    2.0,
		RetryableStatusCodes: []int{429, 500, 502, 503},
	}
}

// IsRetryableStatusCode checks if a status code is retryable
func (c *Config) IsRetryableStatusCode(code int) bool {
	for _, retryable := range c.RetryableStatusCodes {
		if code == retryable {
			return true
		}
	}
	return false
}

// Do executes a function with retry logic
func Do(ctx context.Context, cfg Config, fn func(context.Context) error) error {
	var lastErr error

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// Calculate backoff with jitter
			backoff := calculateBackoff(cfg, attempt)

			select {
			case <-ctx.Done():
				return fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(backoff):
			}
		}

		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if we should retry based on error type
		if !isRetryableError(err) {
			return err
		}
	}

	return fmt.Errorf("all %d retries failed: %w", cfg.MaxRetries, lastErr)
}

// DoWithResult executes a function with retry logic and returns a result
func DoWithResult[T any](ctx context.Context, cfg Config, fn func(context.Context) (T, error)) (T, error) {
	var result T
	var lastErr error

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := calculateBackoff(cfg, attempt)

			select {
			case <-ctx.Done():
				return result, fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(backoff):
			}
		}

		res, err := fn(ctx)
		if err == nil {
			return res, nil
		}

		lastErr = err

		if !isRetryableError(err) {
			return res, err
		}
	}

	return result, fmt.Errorf("all %d retries failed: %w", cfg.MaxRetries, lastErr)
}

// calculateBackoff calculates the backoff duration with jitter
func calculateBackoff(cfg Config, attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	backoff := float64(cfg.InitialBackoff) * math.Pow(cfg.BackoffMultiplier, float64(attempt-1))
	backoff = math.Min(backoff, float64(cfg.MaxBackoff))

	// Add jitter (±25%)
	jitter := (rand.Float64() - 0.5) * 0.5 * backoff
	backoff += jitter

	return time.Duration(backoff)
}

// isRetryableError checks if an error should be retried
func isRetryableError(err error) bool {
	// By default, all errors are retryable
	// Specific error types can be excluded here
	return true
}

// RetryableError is an error that indicates whether it should be retried
type RetryableError struct {
	Err       error
	Retryable bool
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}
