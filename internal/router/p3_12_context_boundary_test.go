package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.12 context boundary contract (Long Horizon / Agentic hard filter):
//
//   - The estimated requirement is input tokens + expected output tokens
//     (req.MaxTokens when positive, else DefaultMaxTokens = 4096).
//   - A candidate is rejected iff its known MaxContext is STRICTLY smaller
//     than the requirement: required == MaxContext is eligible, one token
//     more is not.
//   - Unknown MaxContext (0) is always eligible.
//   - Images (+5 tokens per image part), tool definitions, and reasoning
//     controls contribute to the input estimate.
//   - Only Long Horizon and Agentic enforce the filter.
//
// Exact arithmetic for these tests: one user message with empty content
// contributes 4 structural tokens. required = 4 + maxTokens.

// ctxBoundaryPipeline builds a pipeline with a single provider whose known
// MaxContext is `maxContext` (0 = unknown).
func ctxBoundaryPipeline(t *testing.T, maxContext int) (*router.DecisionPipeline, *runtime.RuntimeStore) {
	p := &calibStubProvider{name: "ctx-p", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy}
	if maxContext > 0 {
		p.maxContext = maxContext
	}
	pipeline, store, _ := setupCalibPipeline(t, p)
	setHealth(t, store, "ctx-p", runtime.StateHealthy, 100)
	return pipeline, store
}

// ctxReq builds a request for the boundary tests: empty-content message,
// MaxTokens `out` when positive.
func ctxReq(mode string, out int) *apitypes.ChatCompletionRequest {
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     mode,
		Messages: []apitypes.Message{{Role: "user", Content: ""}},
	}
	if out > 0 {
		req.MaxTokens = &out
	}
	return req
}

// runCtxRequest executes a request and reports whether the provider was
// hard-rejected.
func runCtxRequest(t *testing.T, pipeline *router.DecisionPipeline, req *apitypes.ChatCompletionRequest) (selected bool, rejected string) {
	t.Helper()
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider == "ctx-p" {
		return true, ""
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "ctx-p" && cs.Rejected {
			return false, cs.RejectionReason
		}
	}
	return false, "(not rejected, not selected)"
}

// TestP312ContextBoundaryExactAndOneAbove verifies the boundary is
// INCLUSIVE at equality and exclusive one token above:
// required 8196 == MaxContext 8196 -> eligible; 8195 -> rejected.
func TestP312ContextBoundaryExactAndOneAbove(t *testing.T) {
	// Empty-content message: input = 4. MaxTokens 8192 -> required 8196.
	pipeline, _ := ctxBoundaryPipeline(t, 8196)
	selected, rejected := runCtxRequest(t, pipeline, ctxReq("long_horizon", 8192))
	if !selected {
		t.Fatalf("required == MaxContext (8196 == 8196) must be eligible, got rejected: %s", rejected)
	}

	pipeline2, _ := ctxBoundaryPipeline(t, 8195)
	selected, rejected = runCtxRequest(t, pipeline2, ctxReq("long_horizon", 8192))
	if selected {
		t.Fatal("required 8196 > MaxContext 8195 must be rejected")
	}
	if rejected == "" {
		t.Fatal("expected an insufficient-context rejection reason")
	}
}

// TestP312ContextUnknownMaxContextAlwaysEligible verifies MaxContext == 0
// (unknown) never triggers the hard filter.
func TestP312ContextUnknownMaxContextAlwaysEligible(t *testing.T) {
	pipeline, _ := ctxBoundaryPipeline(t, 0)
	maxTokens := 1_000_000
	req := ctxReq("long_horizon", maxTokens)
	selected, rejected := runCtxRequest(t, pipeline, req)
	if !selected {
		t.Fatalf("unknown MaxContext must stay eligible, got rejected: %s", rejected)
	}
}

// TestP312ContextDefaultOutputBudget verifies absent MaxTokens uses the
// DefaultMaxTokens (4096) output budget: required = 4 + 4096 = 4100.
func TestP312ContextDefaultOutputBudget(t *testing.T) {
	pipeline, _ := ctxBoundaryPipeline(t, 4100)
	selected, rejected := runCtxRequest(t, pipeline, ctxReq("long_horizon", 0))
	if !selected {
		t.Fatalf("required 4100 == MaxContext 4100 (default budget) must be eligible, got rejected: %s", rejected)
	}

	pipeline2, _ := ctxBoundaryPipeline(t, 4099)
	selected, rejected = runCtxRequest(t, pipeline2, ctxReq("long_horizon", 0))
	if selected {
		t.Fatal("required 4100 > MaxContext 4099 must be rejected")
	}
	if rejected == "" {
		t.Fatal("expected an insufficient-context rejection reason")
	}
}

// TestP312ContextEstimateCountsMultimodal verifies image content adds 5
// tokens to the input estimate: required = 4 + 5 + 8192 = 8201. The provider
// must be vision-capable so the vision hard filter does not interfere.
func TestP312ContextEstimateCountsMultimodal(t *testing.T) {
	req := ctxReq("long_horizon", 8192)
	req.Messages = []apitypes.Message{{Role: "user", Content: []apitypes.ContentPart{
		{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "data:image/png;base64,AA"}},
	}}}

	visionBoundary := func(maxContext int) *router.DecisionPipeline {
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "ctx-p", supportsAll: true, vision: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, maxContext: maxContext},
		)
		setHealth(t, store, "ctx-p", runtime.StateHealthy, 100)
		return pipeline
	}

	selected, rejected := runCtxRequest(t, visionBoundary(8201), req)
	if !selected {
		t.Fatalf("required 8201 == MaxContext 8201 (with image) must be eligible, got rejected: %s", rejected)
	}

	selected, rejected = runCtxRequest(t, visionBoundary(8200), req)
	if selected {
		t.Fatal("required 8201 > MaxContext 8200 (with image) must be rejected")
	}
	if rejected == "" {
		t.Fatal("expected an insufficient-context rejection reason")
	}
}

// TestP312ContextEstimateCountsTools verifies tool definitions add to the
// input estimate: 1 tool "t" adds 8 + (1+3)/4 = 9 tokens;
// required = 4 + 9 + 8192 = 8205.
func TestP312ContextEstimateCountsTools(t *testing.T) {
	req := ctxReq("long_horizon", 8192)
	req.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "t"}}}

	pipeline, _ := ctxBoundaryPipeline(t, 8205)
	selected, rejected := runCtxRequest(t, pipeline, req)
	if !selected {
		t.Fatalf("required 8205 == MaxContext 8205 (with tool) must be eligible, got rejected: %s", rejected)
	}

	pipeline2, _ := ctxBoundaryPipeline(t, 8204)
	selected, rejected = runCtxRequest(t, pipeline2, req)
	if selected {
		t.Fatal("required 8205 > MaxContext 8204 (with tool) must be rejected")
	}
	if rejected == "" {
		t.Fatal("expected an insufficient-context rejection reason")
	}
}

// TestP312ContextFilterOnlyLongHorizonAndAgentic verifies NO other mode
// enforces the context budget, even for an absurdly large request.
func TestP312ContextFilterOnlyLongHorizonAndAgentic(t *testing.T) {
	modes := []string{"auto", "coding", "reasoning", "vision", "fast", "planning"}
	maxTokens := 1_000_000
	for _, mode := range modes {
		pipeline, _ := ctxBoundaryPipeline(t, 1024)
		selected, rejected := runCtxRequest(t, pipeline, ctxReq(mode, maxTokens))
		if !selected {
			t.Fatalf("%s must NOT enforce the context filter (MaxContext 1024 < required), got rejected: %s", mode, rejected)
		}
	}
}
