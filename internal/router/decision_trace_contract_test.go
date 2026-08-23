package router_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.14: canonical DecisionTrace contract.
//
// Every routing decision must be explainable from one immutable trace:
// WHAT was requested, WHICH mode resolved, WHAT capabilities were required,
// WHICH candidates were considered, WHY each was accepted/rejected, HOW each
// was scored, WHICH provider won, and WHAT runtime snapshot justified it.

// canonicalMode maps a public mode name to its canonical profile mode.
func canonicalMode(t *testing.T, mode string) router.Mode {
	t.Helper()
	m := router.Mode(mode)
	if m == "auto" {
		return router.ModeDefault
	}
	if _, ok := router.DefaultModeProfiles()[m]; !ok {
		t.Fatalf("unknown mode %q", mode)
	}
	return m
}

// TestSuccessfulDecisionTraceContract (matrix A + M): for all eight
// public modes, a successful decision trace carries the full canonical
// contract: request identity, mode resolution, mode policy, intent,
// capability requirements, effective weights, mode bonuses, runtime hash,
// candidate scores with components, winner, and stage results.
func TestSuccessfulDecisionTraceContract(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 2000, healthState: runtime.StateDegraded, costPerUnit: 0.0009, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateDegraded, 2000)

	for _, mode := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		req := hintReq(mode)
		res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("%s Execute: %v", mode, err)
		}
		tr := rec.get()
		if tr == nil {
			t.Fatalf("%s: no trace captured", mode)
		}

		// Request identity.
		if tr.DecisionID == "" {
			t.Fatalf("%s: empty decision id", mode)
		}
		if tr.Timestamp.IsZero() {
			t.Fatalf("%s: zero timestamp", mode)
		}

		// Mode resolution identity.
		canonical := canonicalMode(t, mode)
		if tr.ResolvedMode != canonical {
			t.Fatalf("%s: ResolvedMode = %q, want %q", mode, tr.ResolvedMode, canonical)
		}
		if tr.RequestedMode != mode {
			t.Fatalf("%s: RequestedMode = %q, want %q", mode, tr.RequestedMode, mode)
		}
		if tr.ModeSource != "explicit" {
			t.Fatalf("%s: ModeSource = %q, want explicit", mode, tr.ModeSource)
		}
		mp := router.DefaultModeProfiles()[canonical]
		if tr.ModeDescription != mp.Description {
			t.Fatalf("%s: ModeDescription = %q, want %q", mode, tr.ModeDescription, mp.Description)
		}
		if len(tr.ModeTraits) != len(mp.Traits) {
			t.Fatalf("%s: ModeTraits = %v, want %v", mode, tr.ModeTraits, mp.Traits)
		}
		for i := range mp.Traits {
			if tr.ModeTraits[i] != mp.Traits[i] {
				t.Fatalf("%s: ModeTraits[%d] = %q, want %q", mode, i, tr.ModeTraits[i], mp.Traits[i])
			}
		}

		// Intent.
		if tr.Intent == nil {
			t.Fatalf("%s: missing intent", mode)
		}
		if tr.Intent.TaskType == "" {
			t.Fatalf("%s: empty task type", mode)
		}

		// Capability requirements.
		if tr.CapabilityRequirements == nil {
			t.Fatalf("%s: missing capability requirements", mode)
		}
		if tr.CapabilityRequirements.NeedsToolCalling != true {
			t.Fatalf("%s: hintReq must require tool calling", mode)
		}

		// Effective weights equal the profile-derived weights selection used.
		want := modeWeights(t, mode)
		if tr.EffectiveWeights != want {
			t.Fatalf("%s: EffectiveWeights = %+v, want %+v", mode, tr.EffectiveWeights, want)
		}
		if tr.ModeBonuses != mp.CapabilityBonuses {
			t.Fatalf("%s: ModeBonuses = %+v, want %+v", mode, tr.ModeBonuses, mp.CapabilityBonuses)
		}

		// Runtime identity.
		if len(tr.RuntimeHash) != 64 {
			t.Fatalf("%s: RuntimeHash = %q, want 64-hex", mode, tr.RuntimeHash)
		}

		// Candidate set + scores + selected identity.
		if len(tr.CandidateScores) != 2 {
			t.Fatalf("%s: CandidateScores = %d, want 2", mode, len(tr.CandidateScores))
		}
		if len(tr.CandidateScores) != len(res.Decision.CandidateScores) {
			t.Fatalf("%s: trace/decision score count mismatch", mode)
		}
		for i := range tr.CandidateScores {
			a, b := tr.CandidateScores[i], res.Decision.CandidateScores[i]
			if a.Provider != b.Provider || a.TotalScore != b.TotalScore || a.Selected != b.Selected {
				t.Fatalf("%s: trace/decision score %d mismatch: %+v vs %+v", mode, i, a, b)
			}
		}
		if tr.Winner == nil || tr.Winner.ProviderName != res.Decision.SelectedProvider {
			t.Fatalf("%s: trace winner %+v != decision selected %q", mode, tr.Winner, res.Decision.SelectedProvider)
		}

		// Stage results: all four stages completed.
		if len(tr.StageResults) != 4 {
			t.Fatalf("%s: StageResults = %d, want 4", mode, len(tr.StageResults))
		}
		for _, sr := range tr.StageResults {
			if sr.Status != router.StageStatusCompleted {
				t.Fatalf("%s: stage %s status = %v", mode, sr.Name, sr.Status)
			}
		}
	}
}

// TestTraceDecisionIdentity (matrix C + D): the selected candidate in the
// trace is exactly the SelectionResult candidate, its score is exactly the
// decision score, and it is the maximum among eligible candidates.
func TestTraceDecisionIdentity(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 300, healthState: runtime.StateHealthy, costPerUnit: 0.0009, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 300)

	res, err := pipeline.Execute(context.Background(), hintReq("coding"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}

	// Winner identity.
	if res.Candidate == nil {
		t.Fatal("expected a winning candidate")
	}
	if tr.Winner == nil {
		t.Fatal("expected trace winner")
	}
	if tr.Winner.ProviderName != res.Candidate.ProviderName {
		t.Fatalf("Winner.ProviderName = %q, want %q", tr.Winner.ProviderName, res.Candidate.ProviderName)
	}
	if tr.Winner.ProviderModelID != res.Candidate.ProviderModelID {
		t.Fatalf("Winner.ProviderModelID = %q, want %q", tr.Winner.ProviderModelID, res.Candidate.ProviderModelID)
	}

	// Selected flag + score identity.
	selected := -1
	for i, cs := range tr.CandidateScores {
		if cs.Selected {
			selected = i
			break
		}
	}
	if selected < 0 {
		t.Fatal("no selected candidate in trace")
	}
	sel := tr.CandidateScores[selected]
	if sel.Provider != res.Candidate.ProviderName {
		t.Fatalf("selected provider %q != result candidate %q", sel.Provider, res.Candidate.ProviderName)
	}
	if sel.TotalScore != res.Decision.CandidateScores[selected].TotalScore {
		t.Fatalf("selected TotalScore %f != decision score %f", sel.TotalScore, res.Decision.CandidateScores[selected].TotalScore)
	}
	// The selected candidate is the maximum among non-rejected candidates.
	for _, cs := range tr.CandidateScores {
		if cs.Rejected {
			continue
		}
		if cs.TotalScore > sel.TotalScore+1e-9 {
			t.Fatalf("candidate %s scored %f > selected %f", cs.Provider, cs.TotalScore, sel.TotalScore)
		}
	}
}

// TestCandidateScoreComponentIdentity (matrix C): for every mode and every
// candidate, TotalScore decomposes exactly into
//
//	wH*Health + wL*Latency + wC*Cost + wK*Cap + ModeBonus + ContextBonus + TelemetryPref
//
// The component fields describe the score the scorer actually computed.
func TestCandidateScoreComponentIdentity(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 2000, healthState: runtime.StateDegraded, costPerUnit: 0.0009, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateDegraded, 2000)

	for _, mode := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		res, err := pipeline.Execute(context.Background(), hintReq(mode), router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("%s Execute: %v", mode, err)
		}
		tr := rec.get()
		w := tr.EffectiveWeights
		for _, cs := range tr.CandidateScores {
			expected := w.Health*cs.HealthScore +
				w.Latency*cs.LatencyScore +
				w.Cost*cs.CostScore +
				w.Capability*cs.CapScore +
				cs.ModeBonus + cs.ContextBonus + cs.TelemetryPref
			if diff := abs(cs.TotalScore - expected); diff > 1e-9 {
				t.Fatalf("mode=%s %s: TotalScore %f != component sum %f (diff %e): health=%f lat=%f cost=%f cap=%f mb=%f cb=%f tp=%f",
					mode, cs.Provider, cs.TotalScore, expected, diff,
					cs.HealthScore, cs.LatencyScore, cs.CostScore, cs.CapScore, cs.ModeBonus, cs.ContextBonus, cs.TelemetryPref)
			}
		}
		_ = res
	}
}

// TestCustomCostCeilingConsistency: with a non-default engine cost ceiling,
// the recorded CostScore components still describe the composite score exactly.
// Regression: the scorer's cost factor hardcoded 0.001 while CostScore used the
// engine ceiling, breaking the identity contract for custom ceilings.
func TestCustomCostCeilingConsistency(t *testing.T) {
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	aaa := &calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.005, maxContext: 128000}
	zzz := &calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 2000, healthState: runtime.StateDegraded, costPerUnit: 0.0001, maxContext: 128000}
	for _, p := range []*calibStubProvider{aaa, zzz} {
		reg.Register(p)
		_ = store.Register(runtime.NewProviderRuntime(p.name, p))
	}
	manager := runtime.NewManager(store)
	bus := eventbus.NewEventBus()
	rec := &traceRecorder{}
	bus.Subscribe(eventbus.DecisionTraceCreated, rec.onEvent)

	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:    reg,
		Runtime:     manager,
		Weights:     config.DefaultRoutingWeights(),
		CostCeiling: 0.01,
	})
	for _, p := range []*calibStubProvider{aaa, zzz} {
		eng.SetModelCapabilities(p.name, "m", router.Capabilities{
			Streaming:   true,
			Vision:      p.vision,
			Reasoning:   p.reasoning,
			ToolCalling: p.toolCalling,
			Structured:  p.structured,
			MaxContext:  p.maxContext,
		})
	}
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RoutingEngine:  eng,
		RuntimeManager: manager,
		EventBus:       bus,
		Weights:        config.DefaultRoutingWeights(),
		CostCeiling:    0.01,
	})

	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateDegraded, 2000)

	_, err := pipeline.Execute(context.Background(), hintReq("auto"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}
	w := tr.EffectiveWeights
	for _, cs := range tr.CandidateScores {
		if cs.Rejected {
			continue
		}
		expected := w.Health*cs.HealthScore +
			w.Latency*cs.LatencyScore +
			w.Cost*cs.CostScore +
			w.Capability*cs.CapScore +
			cs.ModeBonus + cs.ContextBonus + cs.TelemetryPref
		if diff := abs(cs.TotalScore - expected); diff > 1e-9 {
			t.Fatalf("%s: TotalScore %f != component sum %f (diff %e), cost=%f", cs.Provider, cs.TotalScore, expected, diff, cs.CostScore)
		}
	}
	// CostScore must reflect the configured 0.01 ceiling, not the 0.001 default.
	for _, cs := range tr.CandidateScores {
		if cs.Provider == "aaa" {
			if diff := abs(cs.CostScore - (1.0 - 0.005/0.01)); diff > 1e-9 {
				t.Fatalf("aaa CostScore = %f, want %f (ceiling 0.01)", cs.CostScore, 1.0-0.005/0.01)
			}
		}
		if cs.Provider == "zzz" {
			if diff := abs(cs.CostScore - (1.0 - 0.0001/0.01)); diff > 1e-9 {
				t.Fatalf("zzz CostScore = %f, want %f (ceiling 0.01)", cs.CostScore, 1.0-0.0001/0.01)
			}
		}
	}
}

// TestPlanningAgenticTelemetryPrefIdentity: planning/agentic candidates
// with measured execution telemetry carry the exact telemetry contribution in
// TelemetryPref, and the total identity still holds.
func TestPlanningAgenticTelemetryPrefIdentity(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 300, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 300)
	// aaa: perfect execution history (10/10), zzz: mediocre (5/10).
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		return nil
	})
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcomeModel("m", false, 0)
		}
		return nil
	})

	for _, mode := range []string{"planning", "agentic"} {
		res, err := pipeline.Execute(context.Background(), execReq(mode, "do it"), router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("%s Execute: %v", mode, err)
		}
		tr := rec.get()
		w := tr.EffectiveWeights
		seenPref := false
		for _, cs := range tr.CandidateScores {
			if cs.Rejected {
				continue
			}
			expected := w.Health*cs.HealthScore +
				w.Latency*cs.LatencyScore +
				w.Cost*cs.CostScore +
				w.Capability*cs.CapScore +
				cs.ModeBonus + cs.ContextBonus + cs.TelemetryPref
			if diff := abs(cs.TotalScore - expected); diff > 1e-9 {
				t.Fatalf("mode=%s %s: TotalScore %f != component sum %f", mode, cs.Provider, cs.TotalScore, expected)
			}
			if cs.Provider == "aaa" && cs.TelemetryPref <= 0 {
				t.Fatalf("mode=%s: aaa (perfect telemetry) must carry a positive TelemetryPref, got %f", mode, cs.TelemetryPref)
			}
			if cs.TelemetryPref != 0 {
				seenPref = true
			}
		}
		if !seenPref {
			t.Fatalf("mode=%s: no telemetry preference recorded", mode)
		}
		_ = res
	}
}

// TestRejectionReasonIdentity (matrix G + P/Q/R/S): every hard rejection
// path is traceable as candidate -> reason. UNKNOWN (MaxContext=0) is never
// converted into a rejection.
func TestRejectionReasonIdentity(t *testing.T) {
	cases := []struct {
		name         string
		setup        func(t *testing.T) (*router.DecisionPipeline, *runtime.RuntimeStore, *traceRecorder)
		req          *apitypes.ChatCompletionRequest
		wantReason   string
		wantRejected string
	}{
		{
			name: "vision_hard",
			setup: func(t *testing.T) (*router.DecisionPipeline, *runtime.RuntimeStore, *traceRecorder) {
				p, s, r := setupCalibPipelineTraced(t,
					&calibStubProvider{name: "aaa", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
					&calibStubProvider{name: "zzz", supportsAll: true, vision: true, latencyMs: 2000, healthState: runtime.StateHealthy, costPerUnit: 0.0009, maxContext: 128000},
				)
				setHealth(t, s, "aaa", runtime.StateHealthy, 50)
				setHealth(t, s, "zzz", runtime.StateHealthy, 2000)
				return p, s, r
			},
			req:          imageReq("auto"),
			wantReason:   "vision required: request contains image content",
			wantRejected: "aaa",
		},
		{
			name: "long_horizon_context",
			setup: func(t *testing.T) (*router.DecisionPipeline, *runtime.RuntimeStore, *traceRecorder) {
				p, s, r := setupCalibPipelineTraced(t,
					&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 32768},
					&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, latencyMs: 2000, healthState: runtime.StateHealthy, costPerUnit: 0.0009, maxContext: 128000},
				)
				setHealth(t, s, "aaa", runtime.StateHealthy, 50)
				setHealth(t, s, "zzz", runtime.StateHealthy, 2000)
				return p, s, r
			},
			req:          reqForContext(t, "long_horizon", 65536),
			wantReason:   "insufficient context (32768 < 65536)",
			wantRejected: "aaa",
		},
		{
			name: "planning_capability",
			setup: func(t *testing.T) (*router.DecisionPipeline, *runtime.RuntimeStore, *traceRecorder) {
				p, s, r := setupCalibPipelineTraced(t,
					&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: false, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
					&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, latencyMs: 2000, healthState: runtime.StateHealthy, costPerUnit: 0.0009, maxContext: 128000},
				)
				setHealth(t, s, "aaa", runtime.StateHealthy, 50)
				setHealth(t, s, "zzz", runtime.StateHealthy, 2000)
				return p, s, r
			},
			req:          execReq("planning", "do it"),
			wantReason:   "planning requires reasoning+tool_calling capabilities",
			wantRejected: "aaa",
		},
		{
			name: "agentic_capability",
			setup: func(t *testing.T) (*router.DecisionPipeline, *runtime.RuntimeStore, *traceRecorder) {
				p, s, r := setupCalibPipelineTraced(t,
					&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: false, toolCalling: true, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
					&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, latencyMs: 2000, healthState: runtime.StateHealthy, costPerUnit: 0.0009, maxContext: 128000},
				)
				setHealth(t, s, "aaa", runtime.StateHealthy, 50)
				setHealth(t, s, "zzz", runtime.StateHealthy, 2000)
				return p, s, r
			},
			req:          execReq("agentic", "do it"),
			wantReason:   "agentic requires reasoning+tool_calling capabilities",
			wantRejected: "aaa",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pipeline, _, rec := tc.setup(t)
			res, err := pipeline.Execute(context.Background(), tc.req, router.Environment{}, router.ConfigSnapshot{}, nil)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			tr := rec.get()
			if tr == nil {
				t.Fatal("no trace captured")
			}
			found := false
			for _, cs := range tr.CandidateScores {
				if cs.Provider == tc.wantRejected {
					found = true
					if !cs.Rejected {
						t.Fatalf("%s: expected rejected, got %+v", tc.wantRejected, cs)
					}
					if cs.RejectionReason != tc.wantReason {
						t.Fatalf("rejection reason = %q, want %q", cs.RejectionReason, tc.wantReason)
					}
				}
			}
			if !found {
				t.Fatalf("candidate %s missing from trace scores", tc.wantRejected)
			}
			// The rejection also appears in RejectionReasons.
			inReasons := false
			for _, rr := range tr.RejectionReasons {
				if rr.Provider == tc.wantRejected && rr.Reason == tc.wantReason {
					inReasons = true
				}
			}
			if !inReasons {
				t.Fatalf("rejection reason for %s missing from RejectionReasons", tc.wantRejected)
			}
			// The non-rejected candidate still wins.
			if res.Candidate == nil || res.Candidate.ProviderName == tc.wantRejected {
				t.Fatalf("unexpected winner: %+v", res.Candidate)
			}
		})
	}
}

// TestUnknownContextIsNotRejection: UNKNOWN (MaxContext=0) must remain
// distinct from FALSE — an unknown-context candidate is never hard-rejected
// and the trace must not claim a rejection for it.
func TestUnknownContextIsNotRejection(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 32768},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 0},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	_, err := pipeline.Execute(context.Background(), reqForContext(t, "long_horizon", 65536), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tr := rec.get()
	for _, cs := range tr.CandidateScores {
		if cs.Provider == "zzz" && cs.Rejected {
			t.Fatal("unknown-context candidate must never be hard-rejected")
		}
		if cs.Provider == "aaa" && !cs.Rejected {
			t.Fatal("known-insufficient candidate must be rejected at 65536")
		}
	}
}

// TestAllCandidatesRejectedTrace (matrix B + Phase 14): every candidate
// rejected -> trace explains without logs: no winner, rejection reasons on
// every candidate.
func TestAllCandidatesRejectedTrace(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 32768},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 32768},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 50)
	setHealth(t, store, "zzz", runtime.StateHealthy, 50)

	res, err := pipeline.Execute(context.Background(), imageReq("auto"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Candidate != nil {
		t.Fatalf("expected no winner, got %+v", res.Candidate)
	}
	tr := rec.get()
	if tr.Winner != nil {
		t.Fatalf("trace winner must be nil when all candidates are rejected, got %+v", tr.Winner)
	}
	if len(tr.CandidateScores) != 2 {
		t.Fatalf("expected 2 scored candidates, got %d", len(tr.CandidateScores))
	}
	for _, cs := range tr.CandidateScores {
		if !cs.Rejected || cs.RejectionReason == "" {
			t.Fatalf("candidate %s must carry rejection, got %+v", cs.Provider, cs)
		}
	}
	if len(tr.RejectionReasons) != 2 {
		t.Fatalf("expected 2 rejection reasons, got %d", len(tr.RejectionReasons))
	}
}

// TestCircuitBreakerOpenRejection: an open breaker produces a traceable
// "circuit breaker open" rejection and no winner.
func TestCircuitBreakerOpenRejection(t *testing.T) {
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	p := &calibStubProvider{name: "aaa", supportsAll: true, vision: true, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000}
	reg.Register(p)
	_ = store.Register(runtime.NewProviderRuntime(p.name, p))
	manager := runtime.NewManager(store)

	pool := router.NewBreakerPool(breaker.DefaultConfig())
	b := pool.Get("aaa")
	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}

	bus := eventbus.NewEventBus()
	rec := &traceRecorder{}
	bus.Subscribe(eventbus.DecisionTraceCreated, rec.onEvent)

	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:    reg,
		Runtime:     manager,
		BreakerPool: pool,
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RoutingEngine:  eng,
		RuntimeManager: manager,
		EventBus:       bus,
	})

	res, err := pipeline.Execute(context.Background(), execReq("auto", "do it"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Candidate != nil {
		t.Fatalf("expected no winner with open breaker, got %+v", res.Candidate)
	}
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}
	if tr.Winner != nil {
		t.Fatal("trace winner must be nil")
	}
	found := false
	for _, cs := range tr.CandidateScores {
		if cs.Rejected && cs.RejectionReason == "circuit breaker open" {
			found = true
		}
	}
	if !found {
		t.Fatalf("trace must explain the open breaker: %+v", tr.CandidateScores)
	}
}

// TestInactiveModeFailureTrace: a decision that fails at the intent stage
// (classifier resolves the inactive "elite" mode) still publishes an
// explainable trace: resolved mode, source, and the failed stage.
func TestInactiveModeFailureTrace(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 50)

	req := execReq("", "implement a distributed microservice backend")
	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err == nil {
		t.Fatal("expected intent stage failure for inactive elite classification")
	}
	tr := rec.get()
	if tr == nil {
		t.Fatal("no failure trace published")
	}
	if tr.ResolvedMode != router.ModeElite {
		t.Fatalf("ResolvedMode = %q, want elite", tr.ResolvedMode)
	}
	if tr.ModeSource != "classifier" {
		t.Fatalf("ModeSource = %q, want classifier", tr.ModeSource)
	}
	failed := false
	for _, sr := range tr.StageResults {
		if sr.Name == "intent" && sr.Status == router.StageStatusFailed {
			failed = true
			if sr.Metadata == nil || sr.Metadata["error"] == nil {
				t.Fatal("failed stage must carry the error metadata")
			}
		}
	}
	if !failed {
		t.Fatalf("intent stage must be marked failed: %+v", tr.StageResults)
	}
	// No winner and no scores exist for a decision that never reached
	// candidate generation.
	if tr.Winner != nil || len(tr.CandidateScores) != 0 {
		t.Fatalf("failure trace must not carry a winner or scores: %+v", tr)
	}
}

// TestInvalidModeFailureTrace: an invalid explicit mode publishes a trace
// with the raw requested mode preserved.
func TestInvalidModeFailureTrace(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 50)

	req := execReq("banana", "do it")
	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err == nil {
		t.Fatal("expected invalid-mode error")
	}
	tr := rec.get()
	if tr == nil {
		t.Fatal("no failure trace published")
	}
	if tr.RequestedMode != "banana" {
		t.Fatalf("RequestedMode = %q, want banana", tr.RequestedMode)
	}
	failed := false
	for _, sr := range tr.StageResults {
		if sr.Name == "intent" && sr.Status == router.StageStatusFailed {
			failed = true
		}
	}
	if !failed {
		t.Fatal("intent stage must be marked failed")
	}
}

// TestExplicitRouteTrace (matrix N): a pre-resolved candidate set produces
// a trace whose winner is within the set, in the given order.
func TestExplicitRouteTrace(t *testing.T) {
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	aaa := &calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000}
	zzz := &calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 300, healthState: runtime.StateHealthy, costPerUnit: 0.0009, maxContext: 128000}
	reg.Register(aaa)
	reg.Register(zzz)
	_ = store.Register(runtime.NewProviderRuntime(aaa.name, aaa))
	_ = store.Register(runtime.NewProviderRuntime(zzz.name, zzz))
	manager := runtime.NewManager(store)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 300)

	bus := eventbus.NewEventBus()
	rec := &traceRecorder{}
	bus.Subscribe(eventbus.DecisionTraceCreated, rec.onEvent)
	eng := router.NewRouterEngine(router.RouterEngineConfig{Registry: reg, Runtime: manager})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RoutingEngine:  eng,
		RuntimeManager: manager,
		EventBus:       bus,
	})

	routes := []router.ResolvedRoute{
		{Provider: zzz, ProviderName: "zzz", ProviderModelID: "m", ModelID: "m"},
		{Provider: aaa, ProviderName: "aaa", ProviderModelID: "m", ModelID: "m"},
	}
	res, err := pipeline.Execute(context.Background(), hintReq("coding"), router.Environment{}, router.ConfigSnapshot{}, routes)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}
	if len(tr.CandidateScores) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(tr.CandidateScores))
	}
	if tr.CandidateScores[0].Provider != "zzz" || tr.CandidateScores[1].Provider != "aaa" {
		t.Fatalf("candidate order not preserved: %+v", tr.CandidateScores)
	}
	if res.Candidate == nil {
		t.Fatal("expected a winner for the explicit route set")
	}
	if tr.Winner == nil || tr.Winner.ProviderName != res.Candidate.ProviderName {
		t.Fatalf("trace winner %+v != result candidate %+v", tr.Winner, res.Candidate)
	}
}

// TestAutoModeTraceIdentity (matrix O): explicit auto resolves to the
// default profile with source "explicit" and default policy metadata.
func TestAutoModeTraceIdentity(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 50)

	_, err := pipeline.Execute(context.Background(), execReq("auto", "analyze why this fails"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tr := rec.get()
	if tr.ResolvedMode != router.ModeDefault {
		t.Fatalf("ResolvedMode = %q, want default", tr.ResolvedMode)
	}
	if tr.ModeSource != "explicit" {
		t.Fatalf("ModeSource = %q, want explicit", tr.ModeSource)
	}
	if tr.RequestedMode != "auto" {
		t.Fatalf("RequestedMode = %q, want auto", tr.RequestedMode)
	}
	if tr.ModeDescription != router.DefaultModeProfiles()[router.ModeDefault].Description {
		t.Fatalf("auto trace must carry the default mode policy description")
	}
	if tr.EffectiveWeights != modeWeights(t, "auto") {
		t.Fatalf("auto EffectiveWeights = %+v, want default weights", tr.EffectiveWeights)
	}
}

// countingManager counts Snapshot calls and serves a fixed snapshot.
type countingManager struct {
	mu    sync.Mutex
	count int
	snap  runtime.RuntimeSnapshot
}

func (m *countingManager) Snapshot(_ context.Context) runtime.RuntimeSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count++
	return m.snap
}
func (m *countingManager) Register(runtime.ProviderRuntime) error { return nil }
func (m *countingManager) Deregister(string) error                { return nil }
func (m *countingManager) Get(string) (runtime.ProviderRuntime, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *countingManager) GetAll() ([]runtime.ProviderRuntime, error)               { return nil, nil }
func (m *countingManager) Watch(string, func(runtime.ProviderStateSnapshot)) uint64 { return 0 }
func (m *countingManager) Unwatch(uint64)                                           {}
func (m *countingManager) Update(string, func(runtime.ProviderRuntime) error) error {
	return nil
}
func (m *countingManager) Count() int { return 0 }

// TestNoSecondSnapshot (matrix J + Phase 15): the pipeline acquires
// exactly ONE RuntimeSnapshot per decision, and the trace's RuntimeHash is
// computed from that exact snapshot.
func TestNoSecondSnapshot(t *testing.T) {
	snap := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{
		"aaa": {State: runtime.StateHealthy, LatencyMs: 100},
	}}
	cm := &countingManager{snap: snap}

	reg := provider.NewRegistry()
	aaa := &calibStubProvider{name: "aaa", supportsAll: true, vision: true, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000}
	reg.Register(aaa)

	bus := eventbus.NewEventBus()
	rec := &traceRecorder{}
	bus.Subscribe(eventbus.DecisionTraceCreated, rec.onEvent)

	eng := router.NewRouterEngine(router.RouterEngineConfig{Registry: reg, Runtime: cm})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RoutingEngine:  eng,
		RuntimeManager: cm,
		EventBus:       bus,
	})

	_, err := pipeline.Execute(context.Background(), execReq("auto", "do it"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.count != 1 {
		t.Fatalf("Snapshot called %d times, want exactly 1", cm.count)
	}
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}
	if tr.RuntimeHash != router.RuntimeHash(snap) {
		t.Fatal("trace RuntimeHash does not match the exact decision snapshot")
	}
}

// countingProvider wraps a stub provider and counts pricing calls.
type countingProvider struct {
	*calibStubProvider
	mu      sync.Mutex
	pricing int
}

func (p *countingProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	p.mu.Lock()
	p.pricing++
	p.mu.Unlock()
	return p.calibStubProvider.GetPricing(ctx)
}

func (p *countingProvider) pricingCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pricing
}

// TestNoProviderApiCalls (matrix K): trace construction performs no
// provider API calls — pricing calls happen only during the two scoring
// passes (candidate stage + selection stage), never for tracing.
func TestNoProviderApiCalls(t *testing.T) {
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	aaa := &countingProvider{calibStubProvider: &calibStubProvider{name: "aaa", supportsAll: true, vision: true, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000}}
	zzz := &countingProvider{calibStubProvider: &calibStubProvider{name: "zzz", supportsAll: true, vision: true, latencyMs: 300, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000}}
	reg.Register(aaa)
	reg.Register(zzz)
	_ = store.Register(runtime.NewProviderRuntime(aaa.name, aaa))
	_ = store.Register(runtime.NewProviderRuntime(zzz.name, zzz))
	manager := runtime.NewManager(store)
	setHealth(t, store, "aaa", runtime.StateHealthy, 50)
	setHealth(t, store, "zzz", runtime.StateHealthy, 300)

	bus := eventbus.NewEventBus()
	rec := &traceRecorder{}
	bus.Subscribe(eventbus.DecisionTraceCreated, rec.onEvent)
	eng := router.NewRouterEngine(router.RouterEngineConfig{Registry: reg, Runtime: manager})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RoutingEngine:  eng,
		RuntimeManager: manager,
		EventBus:       bus,
	})

	_, err := pipeline.Execute(context.Background(), execReq("auto", "do it"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if rec.get() == nil {
		t.Fatal("no trace captured")
	}
	// Candidate stage scores 2 providers, selection stage scores 2 candidates:
	// exactly 4 pricing calls. Any additional call would come from tracing.
	if got := aaa.pricingCalls() + zzz.pricingCalls(); got != 4 {
		t.Fatalf("pricing calls = %d, want exactly 4 (2 scoring passes x 2 candidates)", got)
	}
}

// traceCollector keeps every trace published on the bus.
type traceCollector struct {
	mu     sync.Mutex
	traces []*router.DecisionTrace
}

func (c *traceCollector) onEvent(evt eventbus.Event) {
	if evt.Type != eventbus.DecisionTraceCreated {
		return
	}
	if tr, ok := evt.Payload.(*router.DecisionTrace); ok {
		c.mu.Lock()
		c.traces = append(c.traces, tr)
		c.mu.Unlock()
	}
}

func (c *traceCollector) all() []*router.DecisionTrace {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*router.DecisionTrace, len(c.traces))
	copy(out, c.traces)
	return out
}

// TestConcurrentTraceConstruction (matrix L): concurrent decisions each
// produce an immutable, self-consistent trace with a unique decision id; all
// traces agree on winner and scores for identical inputs.
func TestConcurrentTraceConstruction(t *testing.T) {
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	aaa := &calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000}
	zzz := &calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 300, healthState: runtime.StateHealthy, costPerUnit: 0.0009, maxContext: 128000}
	reg.Register(aaa)
	reg.Register(zzz)
	_ = store.Register(runtime.NewProviderRuntime(aaa.name, aaa))
	_ = store.Register(runtime.NewProviderRuntime(zzz.name, zzz))
	manager := runtime.NewManager(store)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 300)

	bus := eventbus.NewEventBus()
	collector := &traceCollector{}
	bus.Subscribe(eventbus.DecisionTraceCreated, collector.onEvent)
	eng := router.NewRouterEngine(router.RouterEngineConfig{Registry: reg, Runtime: manager})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RoutingEngine:  eng,
		RuntimeManager: manager,
		EventBus:       bus,
	})

	const n = 20
	var wg sync.WaitGroup
	winners := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := pipeline.Execute(context.Background(), hintReq("coding"), router.Environment{}, router.ConfigSnapshot{}, nil)
			if err != nil {
				errs[i] = err
				return
			}
			winners[i] = res.Decision.SelectedProvider
		}(i)
	}
	wg.Wait()

	traces := collector.all()
	if len(traces) != n {
		t.Fatalf("traces = %d, want %d", len(traces), n)
	}
	ids := map[router.DecisionID]bool{}
	for i, tr := range traces {
		if errs[i] != nil {
			t.Fatalf("execute %d: %v", i, errs[i])
		}
		if ids[tr.DecisionID] {
			t.Fatalf("duplicate decision id %q", tr.DecisionID)
		}
		ids[tr.DecisionID] = true
		if tr.Winner == nil {
			t.Fatalf("trace %d: no winner", i)
		}
		if tr.Winner.ProviderName != winners[i] {
			t.Fatalf("trace %d winner %q != result winner %q", i, tr.Winner.ProviderName, winners[i])
		}
	}
	// All traces must be internally identical (deterministic decision).
	first := traces[0]
	for i, tr := range traces {
		if tr.RuntimeHash != first.RuntimeHash {
			t.Fatalf("trace %d runtime hash differs", i)
		}
		if tr.ResolvedMode != first.ResolvedMode || tr.EffectiveWeights != first.EffectiveWeights {
			t.Fatalf("trace %d mode/weights differ", i)
		}
		if len(tr.CandidateScores) != len(first.CandidateScores) {
			t.Fatalf("trace %d score count differs", i)
		}
		for j := range tr.CandidateScores {
			a, b := tr.CandidateScores[j], first.CandidateScores[j]
			if a.Provider != b.Provider || a.TotalScore != b.TotalScore {
				t.Fatalf("trace %d candidate %d scores differ", i, j)
			}
		}
	}
}

// TestTraceSchemaVersionDocumented (matrix I): the schema version is a
// deliberate, documented constant distinct from RuntimeHash.
func TestTraceSchemaVersionDocumented(t *testing.T) {
	ver := router.TraceSchemaVersion()
	if ver != 2 {
		t.Fatalf("TraceSchemaVersion() = %d, want 2 (P3.14 canonical contract)", ver)
	}
	// A fresh trace carries the current version.
	builder := router.NewDecisionTraceBuilder(router.NewDecisionID(), runtime.RuntimeSnapshot{})
	if got := builder.Build().TraceSchemaVer; got != ver {
		t.Fatalf("builder trace schema ver = %d, want %d", got, ver)
	}
	// Schema version is NOT the runtime hash: the hash identifies snapshot
	// state, the version identifies the trace structure.
	tr := builder.Build()
	if tr.TraceSchemaVer == int64(len(tr.RuntimeHash)) {
		t.Fatal("schema version must never be conflated with the runtime hash")
	}
}

// TestTraceJSONSerialization (matrix H): DecisionTrace round-trips through
// JSON deterministically, handles nil/empty/rejection-only/large cases, and
// never leaks request content.
func TestTraceJSONSerialization(t *testing.T) {
	// Successful trace round trip.
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	_, err := pipeline.Execute(context.Background(), hintReq("coding"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}

	b1, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Deterministic serialization.
	b2, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal 2: %v", err)
	}
	if string(b1) != string(b2) {
		t.Fatal("JSON serialization is not deterministic")
	}

	var rt router.DecisionTrace
	if unmarshalErr := json.Unmarshal(b1, &rt); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if rt.DecisionID != tr.DecisionID {
		t.Fatalf("round trip DecisionID = %q, want %q", rt.DecisionID, tr.DecisionID)
	}
	if rt.RuntimeHash != tr.RuntimeHash {
		t.Fatalf("round trip RuntimeHash = %q, want %q", rt.RuntimeHash, tr.RuntimeHash)
	}
	if rt.ResolvedMode != tr.ResolvedMode || rt.ModeSource != tr.ModeSource {
		t.Fatalf("round trip mode fields differ: %+v", rt)
	}
	if len(rt.CandidateScores) != len(tr.CandidateScores) {
		t.Fatalf("round trip score count differs")
	}
	if rt.EffectiveWeights != tr.EffectiveWeights {
		t.Fatalf("round trip weights differ: %+v vs %+v", rt.EffectiveWeights, tr.EffectiveWeights)
	}
	if rt.Winner == nil || rt.Winner.ProviderName != tr.Winner.ProviderName {
		t.Fatalf("round trip winner differs")
	}

	// Rejection-only + nil/empty candidates.
	empty := &router.DecisionTrace{
		DecisionID:     router.NewDecisionID(),
		TraceSchemaVer: router.TraceSchemaVersion(),
	}
	if _, marshalErr := json.Marshal(empty); marshalErr != nil {
		t.Fatalf("empty trace marshal: %v", marshalErr)
	}
	rejectedOnly := &router.DecisionTrace{
		DecisionID:     router.NewDecisionID(),
		TraceSchemaVer: router.TraceSchemaVersion(),
		CandidateScores: []router.CandidateScore{
			{Provider: "p", TotalScore: 0, Rejected: true, RejectionReason: "circuit breaker open"},
		},
		RejectionReasons: []router.RejectionReason{{Provider: "p", Reason: "circuit breaker open"}},
	}
	if _, marshalErr := json.Marshal(rejectedOnly); marshalErr != nil {
		t.Fatalf("rejection-only marshal: %v", marshalErr)
	}

	// Very large candidate set.
	big := &router.DecisionTrace{
		DecisionID:     router.NewDecisionID(),
		TraceSchemaVer: router.TraceSchemaVersion(),
	}
	for i := 0; i < 50; i++ {
		big.CandidateScores = append(big.CandidateScores, router.CandidateScore{
			Provider: fmt.Sprintf("p%d", i), TotalScore: float64(i) / 50,
		})
	}
	bigJSON, err := json.Marshal(big)
	if err != nil {
		t.Fatalf("large trace marshal: %v", err)
	}
	var bigRT router.DecisionTrace
	if unmarshalErr := json.Unmarshal(bigJSON, &bigRT); unmarshalErr != nil {
		t.Fatalf("large trace unmarshal: %v", unmarshalErr)
	}
	if len(bigRT.CandidateScores) != 50 {
		t.Fatalf("large trace round trip lost candidates: %d", len(bigRT.CandidateScores))
	}

	// Failure trace (inactive mode) serializes too.
	pipeline2, store2, rec2 := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store2, "aaa", runtime.StateHealthy, 50)
	_, err = pipeline2.Execute(context.Background(), execReq("", "implement a distributed microservice backend"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err == nil {
		t.Fatal("expected failure")
	}
	failTr := rec2.get()
	if failTr == nil {
		t.Fatal("no failure trace")
	}
	if _, err := json.Marshal(failTr); err != nil {
		t.Fatalf("failure trace marshal: %v", err)
	}
}

// TestTraceNeverLeaksRequestContent: serialized traces must not contain
// prompt text, image URLs, or request payload material.
func TestTraceNeverLeaksRequestContent(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)

	secret := "SECRET_PROMPT_CONTENT_XYZ_98765"
	req := hintReq("coding")
	req.Messages = []apitypes.Message{{Role: "user", Content: secret}}
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	_ = res
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), secret) {
		t.Fatal("trace leaks prompt content")
	}
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("trace contains secret-looking content")
	}

	// Image URL must not leak either.
	imgReq := imageReq("vision")
	_, err = pipeline.Execute(context.Background(), imgReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute image: %v", err)
	}
	tr2 := rec.get()
	b2, _ := json.Marshal(tr2)
	if strings.Contains(string(b2), "example.com") || strings.Contains(string(b2), "img.png") {
		t.Fatal("trace leaks image URL content")
	}
}
