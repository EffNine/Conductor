package health

import (
	"math"
	"math/rand"
	"time"

	"github.com/EffNine/conductor/internal/config"
)

// BackoffDefaults match the designed recovery schedule when config is empty.
var BackoffDefaults = config.ProbeBackoffConfig{
	Enabled:        true,
	InitialDelay:   30 * time.Second,
	MaxDelay:       12 * time.Hour,
	Multiplier:     3.5,
	JitterFraction: 0.2,
}

// NormalizeBackoff fills zero-value fields with defaults.
func NormalizeBackoff(cfg config.ProbeBackoffConfig) config.ProbeBackoffConfig {
	out := cfg
	if out.InitialDelay <= 0 {
		out.InitialDelay = BackoffDefaults.InitialDelay
	}
	if out.MaxDelay <= 0 {
		out.MaxDelay = BackoffDefaults.MaxDelay
	}
	if out.Multiplier <= 1 {
		out.Multiplier = BackoffDefaults.Multiplier
	}
	if out.JitterFraction <= 0 {
		out.JitterFraction = BackoffDefaults.JitterFraction
	}
	return out
}

// CalculateBackoff returns the delay after consecutiveFailureN failures (N>=1).
// Schedule with defaults: 30s → ~105s → ~6m → ~22m → capped at 12h, ± ±jitter.
func CalculateBackoff(cfg config.ProbeBackoffConfig, consecutiveFailures int) time.Duration {
	cfg = NormalizeBackoff(cfg)
	if consecutiveFailures < 1 {
		consecutiveFailures = 1
	}

	delay := float64(cfg.InitialDelay) * math.Pow(cfg.Multiplier, float64(consecutiveFailures-1))
	if delay > float64(cfg.MaxDelay) {
		delay = float64(cfg.MaxDelay)
	}

	if cfg.JitterFraction > 0 {
		jitterRange := delay * cfg.JitterFraction
		jitter := (rand.Float64()*2 - 1) * jitterRange
		delay += jitter
		if delay < 0 {
			delay = 0
		}
	}

	return time.Duration(delay)
}

// NextProbeAfter returns now+backoff for the given failure count.
func NextProbeAfter(cfg config.ProbeBackoffConfig, consecutiveFailures int, now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.Add(CalculateBackoff(cfg, consecutiveFailures))
}
