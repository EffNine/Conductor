package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

func newRankedEligibleEngine(t *testing.T, providers ...*autoTestProvider) *router.RouterEngine {
	t.Helper()
	reg := provider.NewRegistry()
	for _, p := range providers {
		reg.Register(p)
	}
	cat := catalog.New(reg, nil)
	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
	})
	return engine
}

func rankedFor(t *testing.T, engine *router.RouterEngine, req *apitypes.ChatCompletionRequest) []router.CandidateScore {
	t.Helper()
	resolver := engine.AutoModelResolver()
	if resolver == nil {
		t.Fatal("engine must expose an auto resolver")
	}
	scored, err := resolver.RankedEligible(context.Background(), req)
	if err != nil {
		t.Fatalf("RankedEligible: %v", err)
	}
	return scored
}

func textReq(model string) *apitypes.ChatCompletionRequest {
	return &apitypes.ChatCompletionRequest{
		Model:    model,
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
}

func TestRankedEligibleReturnsAllHealthyCandidatesSorted(t *testing.T) {
	openai := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}
	groq := &autoTestProvider{name: "groq", modelID: "llama-3.1-8b-instruct", healthy: true}
	engine := newRankedEligibleEngine(t, openai, groq)

	scored := rankedFor(t, engine, textReq("gpt-4o"))

	if len(scored) != 2 {
		t.Fatalf("len(scored) = %d, want 2 eligible candidates: %+v", len(scored), scored)
	}
	for i := 1; i < len(scored); i++ {
		if scored[i-1].TotalScore < scored[i].TotalScore {
			t.Fatalf("scores not sorted descending: %v < %v", scored[i-1].TotalScore, scored[i].TotalScore)
		}
	}
	for _, cs := range scored {
		if cs.Rejected {
			t.Fatalf("candidate %s/%s unexpectedly rejected: %s", cs.Provider, cs.ProviderID, cs.RejectionReason)
		}
	}
}

func TestRankedEligibleVisionFilterExcludesNonVisionModels(t *testing.T) {
	openai := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}            // default caps: vision
	groq := &autoTestProvider{name: "groq", modelID: "llama-3.1-8b-instruct", healthy: true} // default caps: no vision
	engine := newRankedEligibleEngine(t, openai, groq)

	req := textReq("gpt-4o")
	req.Messages = []apitypes.Message{{
		Role: "user",
		Content: []apitypes.ContentPart{
			{Type: apitypes.ContentPartText, Text: "what is this?"},
			{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
		},
	}}

	scored := rankedFor(t, engine, req)

	if len(scored) != 1 {
		t.Fatalf("len(scored) = %d, want only the vision-capable candidate: %+v", len(scored), scored)
	}
	if scored[0].Provider != "openai" {
		t.Fatalf("candidate = %s, want openai", scored[0].Provider)
	}
}

func TestRankedEligiblePlanningFilterExcludesNonReasoningModels(t *testing.T) {
	openai := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}            // reasoning+tools
	groq := &autoTestProvider{name: "groq", modelID: "llama-3.1-8b-instruct", healthy: true} // tools, no reasoning
	engine := newRankedEligibleEngine(t, openai, groq)

	req := textReq("gpt-4o")
	req.Mode = "planning"

	scored := rankedFor(t, engine, req)

	if len(scored) != 1 {
		t.Fatalf("len(scored) = %d, want only the reasoning-capable candidate: %+v", len(scored), scored)
	}
	if scored[0].Provider != "openai" {
		t.Fatalf("candidate = %s, want openai", scored[0].Provider)
	}
}
