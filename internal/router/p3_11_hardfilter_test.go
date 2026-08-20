package router_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// imageReq returns a request with actual image content.
func imageReq(mode string) *apitypes.ChatCompletionRequest {
	return &apitypes.ChatCompletionRequest{
		Model: "m",
		Mode:  mode,
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
}

// TestP311VisionHardFilterRegression verifies a non-vision provider is hard-
// rejected for image content even when it is much faster/cheaper than the
// vision provider. This was previously a soft score penalty (capability
// factor 0) that could be outweighed by latency/cost (P3.11 bug #1).
func TestP311VisionHardFilterRegression(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "vision-slow", supportsAll: true, vision: true, latencyMs: 6000, healthState: runtime.StateHealthy, costPerUnit: 0.0005},
		&calibStubProvider{name: "text-fast", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001},
	)
	setHealth(t, store, "vision-slow", runtime.StateHealthy, 6000)
	setHealth(t, store, "text-fast", runtime.StateHealthy, 50)

	// Explicit vision mode.
	res, err := pipeline.Execute(context.Background(), imageReq("vision"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "vision-slow" {
		t.Fatalf("expected vision-slow (only vision-capable), got %s", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "text-fast" && !cs.Rejected {
			t.Fatal("text-fast must be hard-rejected for image content")
		}
	}

	// Same hard filter must apply without an explicit mode (classifier may
	// infer vision from the image content anyway; the filter is content-driven).
	res2, err := pipeline.Execute(context.Background(), imageReq(""), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute (no mode): %v", err)
	}
	if res2.Decision.SelectedProvider != "vision-slow" {
		t.Fatalf("no-mode: expected vision-slow, got %s", res2.Decision.SelectedProvider)
	}
}

// TestP311VisionHardFilterExplicitRoutes verifies the vision hard filter also
// applies on the pre-resolved-route path (selectFromRoutesWithMode).
func TestP311VisionHardFilterExplicitRoutes(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "vision-ok", supportsAll: true, vision: true, latencyMs: 200, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "text-only", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "vision-ok", runtime.StateHealthy, 200)
	setHealth(t, store, "text-only", runtime.StateHealthy, 50)

	candidates := []router.ResolvedRoute{
		{ProviderName: "text-only", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "vision-ok", ProviderModelID: "m", ModelID: "m"},
	}
	res, err := pipeline.Execute(context.Background(), imageReq("vision"), router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "vision-ok" {
		t.Fatalf("expected vision-ok (text-only hard rejected), got %s", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "text-only" && !cs.Rejected {
			t.Fatal("text-only must be hard-rejected on the explicit-route path")
		}
	}
}

// TestP311FPTieBreakRegression verifies that scores differing only by
// floating-point noise (below the selection epsilon) do not decide the winner:
// the documented provider-name tie-break must apply (P3.11 bug #2).
// zzz-fp's cost is ~8e-11 lower in score (below the 1e-9 epsilon but far above
// the composite's float resolution) — with a strict `>` this noise would win;
// with the epsilon comparison alpha wins alphabetically.
func TestP311FPTieBreakRegression(t *testing.T) {
	ulpCheaper := math.Float64frombits(math.Float64bits(0.0005) - 5000000)
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "alpha", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0005},
		&calibStubProvider{name: "zzz-fp", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: ulpCheaper},
	)
	setHealth(t, store, "alpha", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz-fp", runtime.StateHealthy, 100)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "alpha" {
		t.Fatalf("expected alpha (deterministic alphabetical tie-break, not FP noise), got %s", res.Decision.SelectedProvider)
	}
	var sAlpha, sZzz float64
	for _, cs := range res.Decision.CandidateScores {
		switch cs.Provider {
		case "alpha":
			sAlpha = cs.TotalScore
		case "zzz-fp":
			sZzz = cs.TotalScore
		}
	}
	diff := sAlpha - sZzz
	if diff < 0 {
		diff = -diff
	}
	if diff > 1e-6 {
		t.Fatalf("expected near-equal scores (tie scenario), got %f vs %f", sAlpha, sZzz)
	}
	if sZzz <= sAlpha {
		t.Fatalf("precondition broken: expected zzz-fp infinitesimally higher, got %f vs %f", sZzz, sAlpha)
	}
}

// TestP311HardFilterPrecedence verifies hard rejections (planning/agentic
// capability, context, vision, breaker) are recorded as rejected candidates
// that cannot win despite favorable soft signals (low latency, low cost).
func TestP311HardFilterPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		provider  *calibStubProvider
		req       *apitypes.ChatCompletionRequest
		wantRej   bool
		wantEmpty bool
	}{
		{
			name: "planning rejects no-tools despite fast+cheap",
			mode: "planning",
			provider: &calibStubProvider{name: "fast-no-tools", supportsAll: true,
				reasoning: true, toolCalling: false, latencyMs: 10, healthState: runtime.StateHealthy, costPerUnit: 0.0001},
			req:     execReq("planning", "plan"),
			wantRej: true,
		},
		{
			name: "agentic rejects no-reasoning despite fast+cheap",
			mode: "agentic",
			provider: &calibStubProvider{name: "fast-no-reason", supportsAll: true,
				reasoning: false, toolCalling: true, latencyMs: 10, healthState: runtime.StateHealthy, costPerUnit: 0.0001},
			req:     execReq("agentic", "agent"),
			wantRej: true,
		},
		{
			name: "long_horizon rejects small context despite fast+cheap",
			mode: "long_horizon",
			provider: &calibStubProvider{name: "fast-small-ctx", supportsAll: true,
				maxContext: 2048, latencyMs: 10, healthState: runtime.StateHealthy, costPerUnit: 0.0001},
			req:     execReq("long_horizon", "x"),
			wantRej: true,
		},
		{
			name: "vision rejects text-only despite fast+cheap",
			mode: "vision",
			provider: &calibStubProvider{name: "fast-text-only", supportsAll: true,
				vision: false, latencyMs: 10, healthState: runtime.StateHealthy, costPerUnit: 0.0001},
			req:     imageReq("vision"),
			wantRej: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pipeline, store, _ := setupCalibPipeline(t, tc.provider)
			setHealth(t, store, tc.provider.name, runtime.StateHealthy, 10)

			res, err := pipeline.Execute(context.Background(), tc.req, router.Environment{}, router.ConfigSnapshot{}, nil)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if res == nil {
				t.Fatal("expected non-nil result")
			}
			if res.Decision.SelectedProvider != "" {
				t.Fatalf("expected no selection (single candidate hard-rejected), got %s", res.Decision.SelectedProvider)
			}
			for _, cs := range res.Decision.CandidateScores {
				if !cs.Rejected {
					t.Fatalf("candidate %s must be rejected, got rejected=false score=%f", cs.Provider, cs.TotalScore)
				}
			}
		})
	}
}

// TestP311BreakerHardFilterThroughPipeline verifies an open circuit breaker
// rejects a candidate before soft scoring on the DecisionPipeline path.
func TestP311BreakerHardFilterThroughPipeline(t *testing.T) {
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	provs := []*calibStubProvider{
		{name: "open-breaker", supportsAll: true, latencyMs: 10, healthState: runtime.StateHealthy},
		{name: "fine", supportsAll: true, latencyMs: 200, healthState: runtime.StateHealthy},
	}
	for _, p := range provs {
		reg.Register(p)
		_ = store.Register(runtime.NewProviderRuntime(p.name, p))
		setHealth(t, store, p.name, p.healthState, p.latencyMs)
	}
	manager := runtime.NewManager(store)

	pool := router.NewBreakerPool(breaker.Config{FailureThreshold: 3, RecoveryTimeout: time.Minute, SuccessThreshold: 1})
	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:    reg,
		Runtime:     manager,
		BreakerPool: pool,
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  eng,
		RuntimeManager: manager,
		BreakerPool:    pool,
	})

	// Open the breaker for "open-breaker" so Allow() rejects it.
	for i := 0; i < 5; i++ {
		pool.Get("open-breaker").RecordFailure()
	}
	if pool.Get("open-breaker").Allow() == breaker.ResultAllowed {
		t.Fatal("expected breaker to be open")
	}

	req := execReq("auto", "hello")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{CircuitBreakerEnabled: true}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "fine" {
		t.Fatalf("expected fine (open-breaker rejected by circuit breaker), got %s", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "open-breaker" && !cs.Rejected {
			t.Fatal("open-breaker must be rejected")
		}
	}
}

// TestP311HardFilterCannotBeOutweighedByBonus verifies a candidate rejected by
// a hard filter cannot win through capability bonuses or telemetry: the
// rejected flag is checked before score comparison.
func TestP311HardFilterCannotBeOutweighedByBonus(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "weak-qualified", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 5000, healthState: runtime.StateDegraded},
		&calibStubProvider{name: "strong-rejected", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 10, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "weak-qualified", runtime.StateDegraded, 5000)
	setHealth(t, store, "strong-rejected", runtime.StateHealthy, 10)

	// Give strong-rejected excellent provider telemetry.
	_ = store.Update("strong-rejected", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 50; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})

	req := execReq("planning", "plan this")
	req.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "t"}}}
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "weak-qualified" {
		t.Fatalf("expected weak-qualified (only eligible), got %s", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "strong-rejected" && !cs.Rejected {
			t.Fatal("strong-rejected must remain rejected despite telemetry/bonus advantage")
		}
	}
}
