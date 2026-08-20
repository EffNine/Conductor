package router

import (
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/runtime"
)

// P3.14 Phase 8: RuntimeHash contract.
//
// 1. Deterministic: same snapshot -> same hash.
// 2. Same snapshot (any map iteration order) -> same hash.
// 3. Different scoring-relevant state -> different hash (state, latency,
//    error rate, execution telemetry, model executions, provider set).
// 4. Non-scoring fields (Timestamp, LastHealthCheck, Capacity, Tags,
//    GlobalState) do NOT affect the hash — the hash identifies scoring
//    inputs, not wall-clock time.
// 5. The hash is computed from the exact snapshot used by selection (the
//    pipeline passes its single snapshot to the builder — verified in the
//    black-box no-second-snapshot test).

func psnap(state runtime.ProviderState, latencyMs int64) runtime.ProviderStateSnapshot {
	return runtime.ProviderStateSnapshot{State: state, LatencyMs: latencyMs}
}

// TestRuntimeHashDeterministic: identical snapshots produce identical hashes.
func TestRuntimeHashDeterministic(t *testing.T) {
	snap := runtime.RuntimeSnapshot{
		Providers: map[string]runtime.ProviderStateSnapshot{
			"openai":    psnap(runtime.StateHealthy, 100),
			"anthropic": psnap(runtime.StateDegraded, 250),
		},
	}
	h1 := RuntimeHash(snap)
	h2 := RuntimeHash(snap)
	if h1 != h2 {
		t.Fatalf("same snapshot hashed differently: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("hash length = %d, want 64 (sha256 hex)", len(h1))
	}
}

// TestRuntimeHashMapOrderIndependent: the same content in different map
// insertion orders (and therefore different internal iteration orders) must
// produce the same hash.
func TestRuntimeHashMapOrderIndependent(t *testing.T) {
	a := runtime.RuntimeSnapshot{
		Providers: map[string]runtime.ProviderStateSnapshot{
			"openai":    psnap(runtime.StateHealthy, 100),
			"anthropic": psnap(runtime.StateDegraded, 250),
			"groq":      psnap(runtime.StateUnhealthy, 500),
		},
	}
	b := runtime.RuntimeSnapshot{
		Providers: map[string]runtime.ProviderStateSnapshot{
			"groq":      psnap(runtime.StateUnhealthy, 500),
			"openai":    psnap(runtime.StateHealthy, 100),
			"anthropic": psnap(runtime.StateDegraded, 250),
		},
	}
	if RuntimeHash(a) != RuntimeHash(b) {
		t.Fatal("map insertion order changed the hash")
	}
}

// TestRuntimeHashModelOrderIndependent: model execution entries hash
// deterministically regardless of map order.
func TestRuntimeHashModelOrderIndependent(t *testing.T) {
	base := func() runtime.RuntimeSnapshot {
		return runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{
			"openai": {
				State:     runtime.StateHealthy,
				LatencyMs: 100,
				ModelExecutions: map[string]runtime.ModelExecutionState{
					"gpt-4o":      {ExecutionCount: 10, ExecutionSuccessCount: 9},
					"gpt-4o-mini": {ExecutionCount: 5, ExecutionSuccessCount: 4},
				},
			},
		}}
	}
	a := base()
	b := base()
	// Re-insert in reverse order (new map, so iteration order differs).
	ps := b.Providers["openai"]
	ps.ModelExecutions = map[string]runtime.ModelExecutionState{
		"gpt-4o-mini": {ExecutionCount: 5, ExecutionSuccessCount: 4},
		"gpt-4o":      {ExecutionCount: 10, ExecutionSuccessCount: 9},
	}
	b.Providers["openai"] = ps
	if RuntimeHash(a) != RuntimeHash(b) {
		t.Fatal("model execution map order changed the hash")
	}
}

// TestRuntimeHashLatencySensitive: distinct latencies must hash differently.
// Regression: the pre-P3.14 encoding collapsed any latency to a single digit
// (100 and 1000 both hashed as '0').
func TestRuntimeHashLatencySensitive(t *testing.T) {
	snapA := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{"p": psnap(runtime.StateHealthy, 100)}}
	snapB := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{"p": psnap(runtime.StateHealthy, 1000)}}
	snapC := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{"p": psnap(runtime.StateHealthy, 123456789)}}
	hA, hB, hC := RuntimeHash(snapA), RuntimeHash(snapB), RuntimeHash(snapC)
	if hA == hB {
		t.Fatal("latency 100 and 1000 hashed identically (single-digit encoding regression)")
	}
	if hB == hC {
		t.Fatal("latency 1000 and 123456789 hashed identically")
	}
}

// TestRuntimeHashErrorRateSensitive: distinct error rates must hash
// differently. Regression: pre-P3.14 encoding truncated to one decimal digit.
func TestRuntimeHashErrorRateSensitive(t *testing.T) {
	mk := func(rate float64) string {
		return RuntimeHash(runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{
			"p": {State: runtime.StateHealthy, LatencyMs: 100, ErrorRate: rate},
		}})
	}
	hA, hB := mk(0.5), mk(0.5000001)
	if hA == hB {
		t.Fatal("error rates 0.5 and 0.5000001 hashed identically (truncation regression)")
	}
	if mk(0.0) == mk(1.0) {
		t.Fatal("error rates 0.0 and 1.0 hashed identically")
	}
}

// TestRuntimeHashStateSensitive: provider state participates in the hash.
func TestRuntimeHashStateSensitive(t *testing.T) {
	hHealthy := RuntimeHash(runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{"p": psnap(runtime.StateHealthy, 100)}})
	hUnhealthy := RuntimeHash(runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{"p": psnap(runtime.StateUnhealthy, 100)}})
	if hHealthy == hUnhealthy {
		t.Fatal("different provider states hashed identically")
	}
}

// TestRuntimeHashTelemetrySensitive: execution telemetry counters used by the
// planning/agentic preference must participate in the hash.
func TestRuntimeHashTelemetrySensitive(t *testing.T) {
	mk := func(exec, succ, fail int64) string {
		return RuntimeHash(runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{
			"p": {
				State:                 runtime.StateHealthy,
				LatencyMs:             100,
				ExecutionCount:        exec,
				ExecutionSuccessCount: succ,
				ExecutionFailureCount: fail,
			},
		}})
	}
	if mk(10, 9, 1) == mk(10, 1, 9) {
		t.Fatal("different execution success/failure counters hashed identically")
	}
	if mk(5, 5, 0) == mk(6, 5, 1) {
		t.Fatal("different execution counts hashed identically")
	}
}

// TestRuntimeHashModelExecutionsSensitive: model-level telemetry participates.
func TestRuntimeHashModelExecutionsSensitive(t *testing.T) {
	mk := func(exec int64) string {
		return RuntimeHash(runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{
			"p": {
				State:     runtime.StateHealthy,
				LatencyMs: 100,
				ModelExecutions: map[string]runtime.ModelExecutionState{
					"m": {ExecutionCount: exec, ExecutionSuccessCount: exec},
				},
			},
		}})
	}
	if mk(10) == mk(20) {
		t.Fatal("different model execution counters hashed identically")
	}
}

// TestRuntimeHashProviderSetSensitive: adding/removing a provider changes the
// hash.
func TestRuntimeHashProviderSetSensitive(t *testing.T) {
	one := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{"p": psnap(runtime.StateHealthy, 100)}}
	two := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{
		"p": psnap(runtime.StateHealthy, 100),
		"q": psnap(runtime.StateHealthy, 100),
	}}
	if RuntimeHash(one) == RuntimeHash(two) {
		t.Fatal("provider set change did not change the hash")
	}
}

// TestRuntimeHashIgnoresNonScoringFields: Timestamp, LastHealthCheck,
// Capacity, Tags, and GlobalState do not affect the hash. This is a
// documented part of the contract: the hash identifies the scoring inputs a
// decision used, not the wall-clock moment or derived aggregates.
func TestRuntimeHashIgnoresNonScoringFields(t *testing.T) {
	base := runtime.RuntimeSnapshot{
		Providers: map[string]runtime.ProviderStateSnapshot{
			"p": {
				State:           runtime.StateHealthy,
				LatencyMs:       100,
				ErrorRate:       0.02,
				LastHealthCheck: time.Now().Add(-time.Minute),
				Capacity:        0.5,
				Tags:            map[string]string{"region": "us-east"},
			},
		},
		GlobalState: runtime.GlobalRuntimeState{TotalProviders: 1, HealthyProviders: 1, AvgLatencyMs: 100},
	}
	other := base
	other.Timestamp = time.Now().Add(time.Hour)
	ps := other.Providers["p"]
	ps.LastHealthCheck = time.Now()
	ps.Capacity = 1.0
	ps.Tags = map[string]string{"region": "eu-west"}
	other.Providers["p"] = ps
	other.GlobalState = runtime.GlobalRuntimeState{TotalProviders: 7, HealthyProviders: 0, AvgLatencyMs: 999}
	if RuntimeHash(base) != RuntimeHash(other) {
		t.Fatal("non-scoring fields changed the hash (contract violation)")
	}
}

// BenchmarkRuntimeHash measures the cost of hashing a snapshot with 10
// providers (each with 5 model telemetry entries).
func BenchmarkRuntimeHash(b *testing.B) {
	snap := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{}}
	for i := 0; i < 10; i++ {
		name := "provider-" + string(rune('a'+i))
		models := map[string]runtime.ModelExecutionState{}
		for j := 0; j < 5; j++ {
			models[string(rune('a'+j))] = runtime.ModelExecutionState{ExecutionCount: 10, ExecutionSuccessCount: 9}
		}
		snap.Providers[name] = runtime.ProviderStateSnapshot{
			State:                 runtime.StateHealthy,
			LatencyMs:             100,
			ModelExecutions:       models,
			ExecutionCount:        10,
			ExecutionSuccessCount: 9,
		}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RuntimeHash(snap)
	}
}

// BenchmarkDecisionTraceBuild measures the pure trace-construction overhead:
// builder setup plus all canonical-field population from pre-computed data.
// This must stay negligible relative to provider execution (ms-scale).
func BenchmarkDecisionTraceBuild(b *testing.B) {
	snap := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{"p": psnap(runtime.StateHealthy, 100)}}
	scores := make([]CandidateScore, 8)
	for i := range scores {
		scores[i] = CandidateScore{
			Provider:     "p",
			ProviderID:   "m",
			TotalScore:   0.8,
			HealthScore:  1.0,
			LatencyScore: 0.9,
			CostScore:    0.8,
			CapScore:     1.0,
			ModeBonus:    0.0125,
			Selected:     i == 0,
		}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		builder := NewDecisionTraceBuilder(NewDecisionID(), snap)
		builder.SetModeResolution("coding", DefaultModeProfiles()[ModeCoding], "explicit")
		builder.SetCandidateScores(scores)
		builder.SetWinner(&ResolvedRoute{ProviderName: "p", ProviderModelID: "m", ModelID: "m"})
		builder.SetEffectiveWeights(Normalize(RawWeights{Health: 25, Latency: 10, Cost: 5, Capability: 60}))
		builder.SetModeBonuses(CapabilityBonuses{ToolCalling: 0.25, Reasoning: 0.15})
		builder.AddStageResult(&StageResult{Name: "intent", Status: StageStatusCompleted, DurationMs: 1})
		builder.AddEvent(EventRecord{Type: "decision.started"})
		_ = builder.Build()
	}
}
