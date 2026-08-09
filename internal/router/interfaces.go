// Package router defines the interfaces that split routing responsibilities.
//
// The current RouterEngine owns all routing logic. These interfaces define
// the boundaries for future refactoring while maintaining backward compatibility.
package router

import (
	"context"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/policy"
)

// IntentResolver resolves the intent of an incoming request.
type IntentResolver interface {
	Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*policy.Intent, error)
}

// CapabilityResolver resolves the capability requirements of a request.
type CapabilityResolver interface {
	Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*policy.CapabilityRequirement, error)
}

// RoutingEngine selects the best provider for a request.
type RoutingEngine interface {
	// Select selects the best provider for a request.
	Select(ctx context.Context, modelID string, req *apitypes.ChatCompletionRequest) (*SelectionResult, error)

	// SelectWithFallback selects the best provider with fallback support.
	SelectWithFallback(ctx context.Context, modelID string, req *apitypes.ChatCompletionRequest) (*SelectionResult, []ResolvedRoute, error)

	// GetProviderScores returns current scores for all providers.
	GetProviderScores(capHint policy.CapabilityRequirement) []ProviderScoreView

	// RecordResult records a request result for a provider.
	RecordResult(providerName string, latencyMs int64, success bool)
}

// ExecutionEngine executes a resolved route against a provider.
type ExecutionEngine interface {
	// Execute executes a resolved route.
	Execute(ctx context.Context, resolved ResolvedRoute, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error)

	// ExecuteStream executes a resolved route with streaming.
	ExecuteStream(ctx context.Context, resolved ResolvedRoute, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error)
}

// RouterOrchestrator orchestrates the routing pipeline.
type RouterOrchestrator interface {
	// Resolve resolves a model ID to a provider route.
	Resolve(modelID string) (*ResolvedRoute, error)

	// ResolveWithContext resolves a model ID with request context.
	ResolveWithContext(ctx context.Context, modelID string, messages []apitypes.Message) (*ResolvedRoute, error)

	// ResolveWithFallback resolves with fallback support.
	ResolveWithFallback(modelID string) (*ResolvedRoute, []ResolvedRoute, error)

	// ResolveWithFallbackAndMessages resolves with fallback and messages.
	ResolveWithFallbackAndMessages(modelID string, messages []apitypes.Message) (*ResolvedRoute, []ResolvedRoute, error)

	// ResolveWithFallbackAndContext resolves with fallback and context.
	ResolveWithFallbackAndContext(ctx context.Context, modelID string, messages []apitypes.Message) (*ResolvedRoute, []ResolvedRoute, error)

	// SetAutoSelector wires runtime automatic model selection.
	SetAutoSelector(s AutoSelector)

	// HasAutoSelector reports whether an auto selector is currently wired.
	HasAutoSelector() bool

	// BreakerPool exposes the circuit breaker pool.
	BreakerPool() *BreakerPool
}
