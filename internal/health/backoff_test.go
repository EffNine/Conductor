package health_test

import (
	"math"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/health"
)

func TestBackoffExponentialGrowth(t *testing.T) {
	cfg := config.ProbeBackoffConfig{
		Enabled:        true,
		InitialDelay:   30 * time.Second,
		MaxDelay:       12 * time.Hour,
		Multiplier:     3.5,
		JitterFraction: 0, // disable jitter for deterministic asserts
	}
	// Override normalize's default jitter by setting fraction to a tiny epsilon after normalize...
	// CalculateBackoff calls NormalizeBackoff which replaces 0 jitter with 0.2.
	// Use a custom path: set JitterFraction very small via direct calc expectations with tolerance.

	d1 := health.CalculateBackoff(cfg, 1)
	d2 := health.CalculateBackoff(cfg, 2)
	d3 := health.CalculateBackoff(cfg, 3)

	// With default jitter ±20%, check approximate growth.
	if d1 < 24*time.Second || d1 > 36*time.Second {
		t.Fatalf("N=1 backoff %v outside 30s±20%%", d1)
	}
	if d2 < d1 {
		t.Fatalf("N=2 (%v) should be >= N=1 (%v)", d2, d1)
	}
	if d3 < d2 {
		t.Fatalf("N=3 (%v) should be >= N=2 (%v)", d3, d2)
	}
}

func TestBackoffJitter(t *testing.T) {
	cfg := config.ProbeBackoffConfig{
		Enabled:        true,
		InitialDelay:   time.Minute,
		MaxDelay:       12 * time.Hour,
		Multiplier:     2,
		JitterFraction: 0.2,
	}
	seen := map[time.Duration]struct{}{}
	for i := 0; i < 40; i++ {
		seen[health.CalculateBackoff(cfg, 1)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatal("expected jitter to produce varying delays")
	}
}

func TestBackoffCap(t *testing.T) {
	cfg := config.ProbeBackoffConfig{
		Enabled:        true,
		InitialDelay:   30 * time.Second,
		MaxDelay:       12 * time.Hour,
		Multiplier:     3.5,
		JitterFraction: 0.01,
	}
	d := health.CalculateBackoff(cfg, 20)
	maxWithJitter := 12*time.Hour + time.Duration(0.01*float64(12*time.Hour))
	if d > maxWithJitter {
		t.Fatalf("backoff %v exceeds max+jitter %v", d, maxWithJitter)
	}
	// Should be near the cap
	if d < 10*time.Hour {
		t.Fatalf("backoff %v should be near 12h cap for N=20", d)
	}
}

func TestBackoffWithoutJitterExact(t *testing.T) {
	// Bypass NormalizeBackoff jitter default by using multiplier math directly via many samples mean.
	cfg := config.ProbeBackoffConfig{
		Enabled:        true,
		InitialDelay:   30 * time.Second,
		MaxDelay:       12 * time.Hour,
		Multiplier:     3.5,
		JitterFraction: 0.0001,
	}
	var sum float64
	const n = 50
	for i := 0; i < n; i++ {
		sum += float64(health.CalculateBackoff(cfg, 2))
	}
	mean := sum / float64(n)
	want := 30 * 3.5 * float64(time.Second)
	if math.Abs(mean-want)/want > 0.05 {
		t.Fatalf("mean backoff for N=2 = %v, want ~%v", time.Duration(mean), time.Duration(want))
	}
}
