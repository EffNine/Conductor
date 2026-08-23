package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 10: DecisionTrace explainability.
//
// The trace must make the winning decision fully auditable:
//   - CandidateScores: per-candidate factor scores + totals + rejection info
//   - Winner: the selected route
//   - RejectionReasons: every rejected candidate with cause
//   - Events: decision lifecycle including mode resolution metadata

func runTraced(t *testing.T, pipeline *router.DecisionPipeline, rec *traceRecorder, mode string) *router.SelectionResult {
	t.Helper()
	req := execReq(mode, "do it")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

// TestTraceCarriesCandidateScores: the trace exposes the same candidate
// scores as the decision result (the Phase 10 gap fix: previously the trace
// carried only the Winner and no scores).
func TestTraceCarriesCandidateScores(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 200, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 2000, healthState: runtime.StateDegraded, costPerUnit: 0.0009, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 200)
	setHealth(t, store, "zzz", runtime.StateDegraded, 2000)

	res := runTraced(t, pipeline, rec, "auto")
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}
	if len(tr.CandidateScores) != 2 {
		t.Fatalf("trace CandidateScores: expected 2, got %d", len(tr.CandidateScores))
	}
	// Trace scores must be identical to the decision result scores.
	if len(tr.CandidateScores) != len(res.Decision.CandidateScores) {
		t.Fatal("trace/decision candidate score count mismatch")
	}
	for i := range tr.CandidateScores {
		a, b := tr.CandidateScores[i], res.Decision.CandidateScores[i]
		if a.Provider != b.Provider || a.TotalScore != b.TotalScore {
			t.Fatalf("trace candidate %d mismatch: %s/%f vs %s/%f", i, a.Provider, a.TotalScore, b.Provider, b.TotalScore)
		}
	}
	if tr.Winner == nil || tr.Winner.ProviderName != res.Decision.SelectedProvider {
		t.Fatalf("trace Winner = %+v, decision selected = %q", tr.Winner, res.Decision.SelectedProvider)
	}
}

// TestTraceCarriesRejections: rejected candidates appear in the trace with
// their causes. Mode=fast with image content rejects the non-vision provider
// everywhere (see Phase 7), so the trace must carry that cause.
func TestTraceCarriesRejections(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: false, reasoning: true, toolCalling: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 20)
	setHealth(t, store, "zzz", runtime.StateHealthy, 500)

	req := imageReq("fast")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("expected zzz, got %q", res.Decision.SelectedProvider)
	}
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}
	found := false
	for _, rr := range tr.RejectionReasons {
		if rr.Provider == "aaa" {
			found = true
			if rr.Reason == "" {
				t.Fatal("rejection reason must be non-empty")
			}
		}
	}
	if !found {
		t.Fatal("trace RejectionReasons missing aaa")
	}
	for _, cs := range tr.CandidateScores {
		if cs.Provider == "aaa" && (!cs.Rejected || cs.RejectionReason == "") {
			t.Fatal("trace CandidateScores missing rejection flags for aaa")
		}
	}
}

// TestTraceLifecycleEvents: the trace contains the full decision lifecycle
// (decision.started, stage events, intent.resolved with mode metadata).
func TestTraceLifecycleEvents(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)

	runTraced(t, pipeline, rec, "reasoning")
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}
	seen := map[string]bool{}
	var intentPayload map[string]any
	for _, evt := range tr.Events {
		seen[evt.Type] = true
		if evt.Type == string(eventbus.IntentResolved) {
			intentPayload, _ = evt.Payload.(map[string]any)
		}
	}
	for _, want := range []string{
		string(eventbus.DecisionStarted),
		"intent.started",
		"capability.started",
		"candidate.started",
		"selection.started",
		string(eventbus.IntentResolved),
	} {
		if !seen[want] {
			t.Fatalf("trace missing event %q", want)
		}
	}
	// Completed stages are recorded as StageResults (all 4 stages, completed).
	if len(tr.StageResults) != 4 {
		t.Fatalf("StageResults: expected 4, got %d", len(tr.StageResults))
	}
	for _, sr := range tr.StageResults {
		if sr.Status != router.StageStatusCompleted {
			t.Fatalf("stage %s status = %v, want completed", sr.Name, sr.Status)
		}
	}
	if intentPayload == nil {
		t.Fatal("intent.resolved payload missing")
	}
	if intentPayload["resolved_mode"] != "reasoning" {
		t.Fatalf("resolved_mode = %v", intentPayload["resolved_mode"])
	}
	if intentPayload["mode_source"] != "explicit" {
		t.Fatalf("mode_source = %v", intentPayload["mode_source"])
	}
}
