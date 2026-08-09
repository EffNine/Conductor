package router

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/policy"
)

func TestInterfaceDefinitions(t *testing.T) {
	// Verify that Engine implements RouterOrchestrator
	var _ RouterOrchestrator = (*Engine)(nil)

	// Verify ResolvedRoute has required fields
	resolved := ResolvedRoute{
		Provider:        nil,
		ProviderName:    "test",
		ProviderModelID: "test-model",
		ModelID:         "test",
		Breaker:         nil,
	}

	if resolved.ProviderName != "test" {
		t.Errorf("expected 'test', got %s", resolved.ProviderName)
	}
	if resolved.ProviderModelID != "test-model" {
		t.Errorf("expected 'test-model', got %s", resolved.ProviderModelID)
	}
}

// Test implementations for compile-time interface checks
type TestIntentResolver struct{}

func (t *TestIntentResolver) Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*policy.Intent, error) {
	return nil, nil
}

type TestCapabilityResolver struct{}

func (t *TestCapabilityResolver) Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*policy.CapabilityRequirement, error) {
	return nil, nil
}

type TestRouterEngine struct{}

func (t *TestRouterEngine) Select(ctx context.Context, modelID string, req *apitypes.ChatCompletionRequest) (*SelectionResult, error) {
	return nil, nil
}

func (t *TestRouterEngine) SelectWithFallback(ctx context.Context, modelID string, req *apitypes.ChatCompletionRequest) (*SelectionResult, []ResolvedRoute, error) {
	return nil, nil, nil
}

func (t *TestRouterEngine) GetProviderScores(capHint policy.CapabilityRequirement) []ProviderScoreView {
	return nil
}

func (t *TestRouterEngine) RecordResult(providerName string, latencyMs int64, success bool) {}

type TestExecutionEngine struct{}

func (t *TestExecutionEngine) Execute(ctx context.Context, resolved ResolvedRoute, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, nil
}

func (t *TestExecutionEngine) ExecuteStream(ctx context.Context, resolved ResolvedRoute, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, nil
}
