package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// TestP311ContextFilterProportional verifies the long_horizon context filter
// is proportional: even a tiny request carries the 4096 default output budget,
// so a 4096-context candidate is rejected while an 8192 candidate is accepted.
func TestP311ContextFilterProportional(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "ctx4096", supportsAll: true, maxContext: 4096, latencyMs: 50, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "ctx8192", supportsAll: true, maxContext: 8192, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "ctx4096", runtime.StateHealthy, 50)
	setHealth(t, store, "ctx8192", runtime.StateHealthy, 100)

	req := execReq("long_horizon", "hi")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "ctx8192" {
		t.Fatalf("expected ctx8192 (4096 cannot fit tiny request + default output budget), got %s", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "ctx4096" && !cs.Rejected {
			t.Fatal("ctx4096 must be rejected: 4101 estimated tokens > 4096")
		}
	}
}

// TestP311MaxTokensOverrideAffectsRequirement verifies req.MaxTokens
// participates in the requirement: with a small explicit output budget a
// 16k candidate is accepted; with a large output budget it is rejected.
func TestP311MaxTokensOverrideAffectsRequirement(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "ctx16k", supportsAll: true, maxContext: 16384, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "ctx16k", runtime.StateHealthy, 50)

	small := execReq("long_horizon", "hello world")
	small.MaxTokens = intPtr(256)
	resSmall, err := pipeline.Execute(context.Background(), small, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute (small): %v", err)
	}
	if resSmall.Decision.SelectedProvider != "ctx16k" {
		t.Fatalf("expected ctx16k accepted with small output budget, got %s", resSmall.Decision.SelectedProvider)
	}

	large := execReq("long_horizon", "hello world")
	large.MaxTokens = intPtr(16384)
	resLarge, err := pipeline.Execute(context.Background(), large, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute (large): %v", err)
	}
	if resLarge.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection with 16k output budget (16384+ > 16384), got %s", resLarge.Decision.SelectedProvider)
	}
	for _, cs := range resLarge.Decision.CandidateScores {
		if cs.Provider == "ctx16k" && !cs.Rejected {
			t.Fatal("ctx16k must be rejected when output budget alone exceeds context")
		}
	}
}

// TestP311ThinkingBudgetAffectsRequirement verifies thinking_budget tokens
// contribute to the context requirement at the pipeline level.
func TestP311ThinkingBudgetAffectsRequirement(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "ctx8192", supportsAll: true, maxContext: 8192, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "ctx8192", runtime.StateHealthy, 50)

	noBudget := execReq("long_horizon", "plan carefully")
	resNo, err := pipeline.Execute(context.Background(), noBudget, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute (no budget): %v", err)
	}
	if resNo.Decision.SelectedProvider != "ctx8192" {
		t.Fatalf("expected ctx8192 accepted without thinking budget, got %s", resNo.Decision.SelectedProvider)
	}

	withBudget := execReq("long_horizon", "plan carefully")
	withBudget.ThinkingBudget = intPtr(100000)
	resWith, err := pipeline.Execute(context.Background(), withBudget, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute (budget): %v", err)
	}
	if resWith.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection with 100k thinking budget, got %s", resWith.Decision.SelectedProvider)
	}
}

// TestP311ResponseFormatDoesNotConsumeContext verifies structured-output
// requests are not penalized by the context filter (response_format is not
// part of the token estimate).
func TestP311ResponseFormatDoesNotConsumeContext(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "ctx8192", supportsAll: true, maxContext: 8192, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "ctx8192", runtime.StateHealthy, 50)

	req := execReq("long_horizon", "hello")
	req.ResponseFormat = map[string]interface{}{"type": "json_object"}
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "ctx8192" {
		t.Fatalf("expected ctx8192 (response_format does not consume context), got %s", res.Decision.SelectedProvider)
	}
}

// TestP311SlightlyBelowRejectedExactlyEqualAccepted verifies the boundary
// semantics at the pipeline level using max_tokens to control the estimate.
func TestP311SlightlyBelowRejectedExactlyEqualAccepted(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "ctx4100", supportsAll: true, maxContext: 4100, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "ctx4100", runtime.StateHealthy, 50)

	// "hi": 4 structural + 1 content = 5 input tokens. With max_tokens 4096,
	// requirement = 4101 > 4100 → rejected. With max_tokens 4095, requirement
	// = 4100 == 4100 → accepted (exact boundary is inclusive).
	over := execReq("long_horizon", "hi")
	over.MaxTokens = intPtr(4096)
	resOver, err := pipeline.Execute(context.Background(), over, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute (over): %v", err)
	}
	if resOver.Decision.SelectedProvider != "" {
		t.Fatalf("expected rejection (4101 > 4100), got %s", resOver.Decision.SelectedProvider)
	}

	exact := execReq("long_horizon", "hi")
	exact.MaxTokens = intPtr(4095)
	resExact, err := pipeline.Execute(context.Background(), exact, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute (exact): %v", err)
	}
	if resExact.Decision.SelectedProvider != "ctx4100" {
		t.Fatalf("expected acceptance at exact boundary (4100 == 4100), got %s", resExact.Decision.SelectedProvider)
	}
}
