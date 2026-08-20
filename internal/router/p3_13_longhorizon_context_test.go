package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 5: Long Horizon context semantics.
//
// Provider A: 32k context, excellent health (healthy), weaker capability.
// Provider B: 128k context, moderate health (degraded), stronger capability.
// Required context is varied across the sweep; selection and rejection are
// verified at every point, including the exact boundary (128000) and above.

// reqForContext builds a request whose estimated token requirement equals want.
// estimate = 4 (message overhead) + len/4 + 4096 (default output budget).
func reqForContext(t *testing.T, mode string, want int) *apitypes.ChatCompletionRequest {
	t.Helper()
	l := 4 * (want - 4100)
	if l < 0 {
		l = 0
	}
	req := execReq(mode, "x")
	req.Messages = []apitypes.Message{{Role: "user", Content: string(make([]byte, l))}}
	got := router.EstimateRequestTokens(req)
	if got != want {
		t.Fatalf("estimate mismatch: want %d, got %d (len %d)", want, got, l)
	}
	return req
}

func TestP313LongHorizonContextSweep(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: false, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 32768},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0005, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateDegraded, 100)

	// required: expected winner
	cases := []struct {
		required int
		want     string
	}{
		{16384, "aaa"},  // both eligible; A wins on health+cost
		{32768, "aaa"},  // exact boundary for A: 32768 >= 32768, still eligible
		{65536, "zzz"},  // A rejected (32768 < 65536)
		{128000, "zzz"}, // exact boundary for B: 128000 >= 128000, eligible
		{132096, ""},    // above both: no selection
	}
	for _, tc := range cases {
		req := reqForContext(t, "long_horizon", tc.required)
		res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("required=%d Execute: %v", tc.required, err)
		}
		if res.Decision.SelectedProvider != tc.want {
			t.Fatalf("required=%d: expected winner %q, got %q", tc.required, tc.want, res.Decision.SelectedProvider)
		}
		for _, cs := range res.Decision.CandidateScores {
			switch {
			case tc.required <= 32768 && cs.Provider == "aaa" && cs.Rejected:
				t.Fatalf("required=%d: aaa (32k) must be eligible at or below its boundary", tc.required)
			case tc.required > 32768 && cs.Provider == "aaa" && !cs.Rejected:
				t.Fatalf("required=%d: aaa (32k) must be rejected above its boundary", tc.required)
			case tc.required > 128000 && cs.Provider == "zzz" && !cs.Rejected:
				t.Fatalf("required=%d: zzz (128k) must be rejected above its boundary", tc.required)
			}
		}
	}
}

// TestP313LongHorizonUnknownContextNotPreferred locks the semantics of
// MaxContext=0 (unknown): the unknown candidate is NEVER rejected, but it is
// also never preferred merely for being unknown — with equal scores the
// deterministic tie-break decides, and a known-adequate candidate wins ties.
func TestP313LongHorizonUnknownContextNotPreferred(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 32768},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 0}, // unknown
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// 16k requirement: both eligible; identical scores -> deterministic aaa.
	req := reqForContext(t, "long_horizon", 16384)
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("unknown-context candidate must not be preferred over an equal known candidate: got %q", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "zzz" && cs.Rejected {
			t.Fatal("unknown-context candidate must never be hard-rejected")
		}
	}

	// 64k requirement: the known 32k candidate is rejected; the unknown
	// candidate remains eligible and becomes the legitimate winner.
	req = reqForContext(t, "long_horizon", 65536)
	res, err = pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("unknown-context candidate should win when all known candidates are insufficient: got %q", res.Decision.SelectedProvider)
	}
}

// TestP313LongHorizonToolOverheadFlipsEligibility verifies tool definitions
// count toward the required context: the same message can be eligible for the
// 32k provider without tools and ineligible with a large tool definition.
func TestP313LongHorizonToolOverheadFlipsEligibility(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 32768},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// Without tools: requirement ~30k, aaa eligible and wins (tie-break).
	req := execReq("long_horizon", "x")
	req.Messages = []apitypes.Message{{Role: "user", Content: string(make([]byte, 103600))}} // ~30k
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("without tools: expected aaa (eligible), got %q", res.Decision.SelectedProvider)
	}

	// With a 40k-char tool definition: requirement crosses 32k; aaa rejected.
	req.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "big", Description: string(make([]byte, 40000))}}}
	res, err = pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("with tool overhead: expected zzz (aaa rejected), got %q", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "aaa" && !cs.Rejected {
			t.Fatal("aaa must be rejected once tool overhead pushes the requirement over 32k")
		}
	}
}

// TestP313LongHorizonExplicitRouteNoExpansion verifies that with an explicit
// route the candidate set is constrained: an insufficient explicit candidate
// is rejected and NO other provider is added to replace it.
func TestP313LongHorizonExplicitRouteNoExpansion(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 32768},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	reg := pipeline.RoutingEngine()
	_ = reg
	stub := &calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 32768}
	explicit := []router.ResolvedRoute{{Provider: stub, ProviderName: "aaa", ProviderModelID: "m", ModelID: "m"}}

	req := reqForContext(t, "long_horizon", 65536)
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, explicit)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "" {
		t.Fatalf("explicit insufficient route must yield no selection, got %q", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "zzz" {
			t.Fatal("candidate set must NOT expand to zzz for an explicit route")
		}
	}
}
