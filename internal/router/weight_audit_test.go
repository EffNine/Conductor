package router_test

import (
	"testing"

	"github.com/EffNine/conductor/internal/router"
)

// TestNormalizeSumsToOne verifies weight normalization invariants.
func TestNormalizeSumsToOne(t *testing.T) {
	raws := []router.RawWeights{
		{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		{Health: 55, Latency: 40, Cost: 3, Capability: 2},
		{Health: 25, Latency: 10, Cost: 5, Capability: 60},
		{Health: 20, Latency: 10, Cost: 5, Capability: 65},
		{Health: 1, Latency: 1, Cost: 1, Capability: 1},
	}
	for _, raw := range raws {
		w := router.Normalize(raw)
		total := w.Health + w.Latency + w.Cost + w.Capability
		if total < 0.999999 || total > 1.000001 {
			t.Errorf("Normalize(%+v) sums to %f, want 1.0", raw, total)
		}
		for name, v := range map[string]float64{"health": w.Health, "latency": w.Latency, "cost": w.Cost, "capability": w.Capability} {
			if v < 0 || v > 1 {
				t.Errorf("Normalize(%+v) %s = %f, want in [0,1]", raw, name, v)
			}
		}
	}
}

// TestNormalizeZeroFallsBackToEqual verifies degenerate raw weights fall
// back to equal weights instead of panicking or producing NaN.
func TestNormalizeZeroFallsBackToEqual(t *testing.T) {
	w := router.Normalize(router.RawWeights{})
	if w.Health != 0.25 || w.Latency != 0.25 || w.Cost != 0.25 || w.Capability != 0.25 {
		t.Fatalf("expected equal weights, got %+v", w)
	}
	w2 := router.Normalize(router.RawWeights{Health: -5, Latency: 0, Cost: 0, Capability: 0})
	if w2.Health != 0.25 || w2.Latency != 0.25 || w2.Cost != 0.25 || w2.Capability != 0.25 {
		t.Fatalf("expected equal weights for non-positive total, got %+v", w2)
	}
}

// TestWeightsInfluenceScores verifies each weight genuinely changes the
// composite score in the intended direction.
func TestWeightsInfluenceScores(t *testing.T) {
	scorer := router.NewScorer(router.RawWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20})
	lowLat := router.Candidate{
		ProviderName: "a", ProviderModelID: "m",
		HealthScore:  1.0,
		LatencyMs:    50,
		CostPerToken: nil,
		Capabilities: router.Capabilities{Streaming: true},
		IsAvailable:  true,
	}
	highLat := lowLat
	highLat.ProviderName = "b"
	highLat.LatencyMs = 4900

	hint := router.CapabilityHint{}

	// Latency-dominant weights: low-latency must score higher.
	latW := router.Normalize(router.RawWeights{Health: 10, Latency: 80, Cost: 5, Capability: 5})
	sLow := scorer.CompositeScoreWithWeights(lowLat, hint, latW)
	sHigh := scorer.CompositeScoreWithWeights(highLat, hint, latW)
	if sLow <= sHigh {
		t.Fatalf("latency weight ineffective: low-latency %f <= high-latency %f", sLow, sHigh)
	}

	// Health-dominant weights: healthy must beat degraded.
	degraded := lowLat
	degraded.HealthScore = 0.6
	healthW := router.Normalize(router.RawWeights{Health: 80, Latency: 10, Cost: 5, Capability: 5})
	if scorer.CompositeScoreWithWeights(lowLat, hint, healthW) <= scorer.CompositeScoreWithWeights(degraded, hint, healthW) {
		t.Fatal("health weight ineffective")
	}

	// Cost-dominant weights: cheap must beat expensive.
	cheap := lowLat
	cheap.CostPerToken = ptr(0.0001)
	expensive := lowLat
	expensive.CostPerToken = ptr(0.0009)
	costW := router.Normalize(router.RawWeights{Health: 5, Latency: 5, Cost: 85, Capability: 5})
	if scorer.CompositeScoreWithWeights(cheap, hint, costW) <= scorer.CompositeScoreWithWeights(expensive, hint, costW) {
		t.Fatal("cost weight ineffective")
	}

	// Capability-dominant weights: capability match must beat mismatch.
	capW := router.Normalize(router.RawWeights{Health: 5, Latency: 5, Cost: 5, Capability: 85})
	reasonHint := router.CapabilityHint{Reasoning: true}
	noReason := lowLat
	noReason.Capabilities = router.Capabilities{Streaming: true}
	withReason := lowLat
	withReason.Capabilities = router.Capabilities{Streaming: true, Reasoning: true}
	if scorer.CompositeScoreWithWeights(withReason, reasonHint, capW) <= scorer.CompositeScoreWithWeights(noReason, reasonHint, capW) {
		t.Fatal("capability weight ineffective")
	}
}

// TestSingleWeightChangeFlipsWinner verifies the Phase 4 acceptance case:
// changing ONE weight (latency) flips the winner for identical candidates with
// a health-vs-latency tradeoff.
func TestSingleWeightChangeFlipsWinner(t *testing.T) {
	scorer := router.NewScorer(router.RawWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20})
	// a: strong health, poor latency. b: degraded health, excellent latency.
	a := router.Candidate{
		ProviderName: "a", ProviderModelID: "m",
		HealthScore: 1.0, LatencyMs: 4000, CostPerToken: nil,
		Capabilities: router.Capabilities{Streaming: true}, IsAvailable: true,
	}
	b := a
	b.ProviderName = "b"
	b.HealthScore = 0.6
	b.LatencyMs = 50

	hint := router.CapabilityHint{}

	// Latency weight 10 vs 80 (normalized) — same candidates, same snapshot data.
	latLow := router.Normalize(router.RawWeights{Health: 40, Latency: 10, Cost: 25, Capability: 25})
	latHigh := router.Normalize(router.RawWeights{Health: 10, Latency: 80, Cost: 5, Capability: 5})

	sa := scorer.CompositeScoreWithWeights(a, hint, latLow)
	sb := scorer.CompositeScoreWithWeights(b, hint, latLow)
	if sa <= sb {
		t.Fatalf("expected a (healthier) to win with low latency weight, got a=%f b=%f", sa, sb)
	}

	sa2 := scorer.CompositeScoreWithWeights(a, hint, latHigh)
	sb2 := scorer.CompositeScoreWithWeights(b, hint, latHigh)
	if sb2 <= sa2 {
		t.Fatalf("expected b (faster) to win with high latency weight, got a=%f b=%f", sa2, sb2)
	}
}

// TestBonusesAffectScores verifies capability bonuses change scores in the
// intended direction and are bounded (never dominate the weighted base).
func TestBonusesAffectScores(t *testing.T) {
	scorer := router.NewScorer(router.RawWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20})
	w := router.Normalize(router.RawWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20})

	plain := router.Candidate{
		ProviderName: "plain", ProviderModelID: "m",
		HealthScore: 1.0, LatencyMs: 50, CostPerToken: nil,
		Capabilities: router.Capabilities{Streaming: true}, IsAvailable: true,
	}
	rich := plain
	rich.ProviderName = "rich"
	rich.Capabilities = router.Capabilities{Streaming: true, ToolCalling: true, Reasoning: true, Structured: true, MaxContext: 128000}

	hint := router.CapabilityHint{}
	bonuses := router.CapabilityBonuses{ToolCalling: 0.30, Reasoning: 0.30, Structured: 0.10, ContextCapacity: 0.10}

	sPlain := scorer.CompositeScoreWithBonuses(plain, hint, w, bonuses)
	sRich := scorer.CompositeScoreWithBonuses(rich, hint, w, bonuses)
	if sRich <= sPlain {
		t.Fatalf("bonuses ineffective: rich %f <= plain %f", sRich, sPlain)
	}
	// Bonus contribution is bounded: 0.05 * (0.30+0.30+0.10+0.10) = 0.04 max.
	if delta := sRich - sPlain; delta > 0.05 {
		t.Fatalf("bonus %f exceeds intended 5%% preference weight", delta)
	}

	// Without bonuses the rich candidate must NOT outscore the plain one here:
	// both score identically on the base dimensions (capabilities not demanded).
	sPlainNo := scorer.CompositeScoreWithWeights(plain, hint, w)
	sRichNo := scorer.CompositeScoreWithWeights(rich, hint, w)
	if sPlainNo != sRichNo {
		t.Fatalf("expected identical base scores, got %f vs %f", sPlainNo, sRichNo)
	}
}

// TestBonusCannotBeatLargeBaseGap verifies bonuses cannot overturn a large
// legitimate base-score difference.
func TestBonusCannotBeatLargeBaseGap(t *testing.T) {
	scorer := router.NewScorer(router.RawWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20})
	w := router.Normalize(router.RawWeights{Health: 55, Latency: 40, Cost: 3, Capability: 2}) // fast profile

	winner := router.Candidate{
		ProviderName: "winner", ProviderModelID: "m",
		HealthScore: 1.0, LatencyMs: 50, CostPerToken: ptr(0.0001),
		Capabilities: router.Capabilities{Streaming: true}, IsAvailable: true,
	}
	loser := router.Candidate{
		ProviderName: "loser", ProviderModelID: "m",
		HealthScore: 0.1, LatencyMs: 4900, CostPerToken: ptr(0.0009),
		Capabilities: router.Capabilities{Streaming: true, ToolCalling: true, Reasoning: true, Structured: true, MaxContext: 128000},
		IsAvailable:  true,
	}
	hint := router.CapabilityHint{}
	maxBonuses := router.CapabilityBonuses{ToolCalling: 0.30, Reasoning: 0.30, Structured: 0.10, ContextCapacity: 0.10}

	sWinner := scorer.CompositeScoreWithBonuses(winner, hint, w, maxBonuses)
	sLoser := scorer.CompositeScoreWithBonuses(loser, hint, w, maxBonuses)
	if sLoser >= sWinner {
		t.Fatalf("bonuses dominated the base score: loser %f >= winner %f", sLoser, sWinner)
	}
}

// TestModeProfilesHaveDistinctNormalizedWeights verifies mode profiles do
// not silently alias weight vectors, and documents the one intentional
// exception: planning and long_horizon share the same weight profile and are
// differentiated instead by capability bonuses (planning: tool-calling +
// reasoning; long_horizon: context-capacity) and hard filters (long_horizon:
// context requirement).
func TestModeProfilesHaveDistinctNormalizedWeights(t *testing.T) {
	profiles := router.DefaultModeProfiles()
	seen := make(map[router.Weights]string)
	for mode, mp := range profiles {
		if !mp.Active {
			continue
		}
		w := router.Normalize(router.RawWeights{
			Health:     mp.WeightPreferences.Health,
			Latency:    mp.WeightPreferences.Latency,
			Cost:       mp.WeightPreferences.Cost,
			Capability: mp.WeightPreferences.Capability,
		})
		if other, dup := seen[w]; dup {
			t.Logf("mode %s and %s share identical normalized weights %+v (differentiated via bonuses/filters)", mode, other, w)
		}
		seen[w] = string(mode)
	}
	if len(seen) != 7 {
		t.Fatalf("expected 7 distinct weight vectors among 8 active modes (planning == long_horizon by design), got %d", len(seen))
	}
}

func ptr(f float64) *float64 { return &f }

func intPtr(i int) *int { return &i }
