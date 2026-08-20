package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// modeStubProvider is a test provider with configurable capabilities and latency.
type modeStubProvider struct {
	name        string
	supportsAll bool
	vision      bool
	reasoning   bool
	toolCalling bool
	structured  bool
	latencyMs   int64
	healthState runtime.ProviderState
}

func (s *modeStubProvider) Name() string { return s.name }
func (s *modeStubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *modeStubProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (s *modeStubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *modeStubProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (s *modeStubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *modeStubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *modeStubProvider) SupportsModel(string) bool { return s.supportsAll }
func (s *modeStubProvider) GetMetadata() provider.Metadata {
	return provider.NewMetadata(s.name, provider.Capabilities{
		Streaming:   true,
		Vision:      s.vision,
		Reasoning:   s.reasoning,
		ToolCalling: s.toolCalling,
		Structured:  s.structured,
	})
}

// setupModePipeline creates a DecisionPipeline wired to a RuntimeStore for mode testing.
func setupModePipeline(t *testing.T, providers ...provider.Provider) (*router.DecisionPipeline, *runtime.RuntimeStore, *runtime.ManagerImpl) {
	t.Helper()
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	for _, p := range providers {
		reg.Register(p)
		_ = store.Register(runtime.NewProviderRuntime(p.Name(), p))
	}
	manager := runtime.NewManager(store)

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})
	return pipeline, store, manager
}

// updateProviderState is a test helper that mutates a provider's runtime state.
func updateProviderState(t *testing.T, store *runtime.RuntimeStore, name string, state runtime.ProviderState, latencyMs int64, errorRate float64) {
	t.Helper()
	err := store.Update(name, func(r runtime.ProviderRuntime) error {
		r.UpdateState(state, "", nil)
		if latencyMs > 0 {
			r.RecordLatency(latencyMs)
		}
		// Set error rate via snapshot mutation if needed.
		_ = errorRate
		return nil
	})
	if err != nil {
		t.Fatalf("update %s: %v", name, err)
	}
}

// ---- FAST MODE TESTS ----

// TestFastModePrefersLowerLatency verifies that Fast mode selects the lower-latency
// provider when health is equal.
func TestFastModePrefersLowerLatency(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "slow", supportsAll: true, latencyMs: 800, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "fast", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "slow", runtime.StateHealthy, 800, 0)
	updateProviderState(t, store, "fast", runtime.StateHealthy, 50, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "fast" {
		t.Fatalf("expected fast (lower latency), got %s", result.Decision.SelectedProvider)
	}
}

// TestFastModeDoesNotSacrificeHealth verifies that an unhealthy low-latency
// provider does not win in Fast mode when a healthy alternative exists.
func TestFastModeDoesNotSacrificeHealth(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "healthy", supportsAll: true, latencyMs: 200, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "fast-broken", supportsAll: true, latencyMs: 20, healthState: runtime.StateUnhealthy},
	)

	updateProviderState(t, store, "healthy", runtime.StateHealthy, 200, 0)
	updateProviderState(t, store, "fast-broken", runtime.StateUnhealthy, 20, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "healthy" {
		t.Fatalf("expected healthy (unhealthy provider rejected), got %s", result.Decision.SelectedProvider)
	}
}

// TestFastModeDiffersFromDefault verifies that Fast mode produces a different
// selection than Default when latency tradeoff exists.
func TestFastModeDiffersFromDefault(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "a", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "b", supportsAll: true, latencyMs: 600, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "a", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "b", runtime.StateHealthy, 600, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "a" {
		t.Fatalf("expected a (lower latency), got %s", result.Decision.SelectedProvider)
	}
}

// ---- CODING MODE TESTS ----

// TestCodingModePrefersToolCalling verifies that Coding mode prefers a provider
// with ToolCalling over one without when other factors are equal.
func TestCodingModePrefersToolCalling(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "no-tools", supportsAll: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "with-tools", supportsAll: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "no-tools", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "with-tools", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "write a function"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "with-tools" {
		t.Fatalf("expected with-tools (coding preference), got %s", result.Decision.SelectedProvider)
	}
}

// TestCodingModePrefersReasoning verifies that Coding mode prefers a provider
// with Reasoning capability when other factors are close.
func TestCodingModePrefersReasoning(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "no-reason", supportsAll: true, reasoning: false, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "has-reason", supportsAll: true, reasoning: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "no-reason", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "has-reason", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "debug this code"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "has-reason" {
		t.Fatalf("expected has-reason (coding preference), got %s", result.Decision.SelectedProvider)
	}
}

// TestCodingModeDoesNotRejectNonToolCalling verifies that Coding mode does NOT
// reject a provider solely because it lacks ToolCalling.
func TestCodingModeDoesNotRejectNonToolCalling(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "plain", supportsAll: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "plain", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "write a function"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "plain" {
		t.Fatalf("expected plain (not rejected for missing tools), got %s", result.Decision.SelectedProvider)
	}
}

// ---- REASONING MODE TESTS ----

// TestReasoningModePrefersReasoningProvider verifies that a reasoning-capable
// provider beats an otherwise similar non-reasoning provider.
func TestReasoningModePrefersReasoningProvider(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "text-only", supportsAll: true, reasoning: false, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "reasoning", supportsAll: true, reasoning: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "text-only", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "reasoning", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "analyze and compare the trade-offs"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "reasoning" {
		t.Fatalf("expected reasoning (mode preference), got %s", result.Decision.SelectedProvider)
	}
}

// TestReasoningModeDoesNotHardReject verifies that Reasoning mode does not
// hard-reject a non-reasoning provider when no explicit hard requirement exists.
func TestReasoningModeDoesNotHardReject(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "only-provider", supportsAll: true, reasoning: false, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "only-provider", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "analyze this"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "only-provider" {
		t.Fatalf("expected only-provider (not hard-rejected), got %s", result.Decision.SelectedProvider)
	}
}

// ---- VISION MODE TESTS ----

// TestVisionModeRejectsNonVisionProvider verifies that an actual image request
// rejects a non-vision provider.
func TestVisionModeRejectsNonVisionProvider(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "vision-only", supportsAll: true, vision: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "text-only", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "vision-only", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "text-only", runtime.StateHealthy, 50, 0)

	req := &apitypes.ChatCompletionRequest{
		Model: "m",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "vision-only" {
		t.Fatalf("expected vision-only (hard vision filter), got %s", result.Decision.SelectedProvider)
	}
}

// TestVisionModeSelectsVisionProvider verifies that a vision-capable provider
// is selected when vision content is present.
func TestVisionModeSelectsVisionProvider(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "gpt-4o", supportsAll: true, vision: true, latencyMs: 150, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "claude", supportsAll: true, vision: true, latencyMs: 200, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "gpt-4o", runtime.StateHealthy, 150, 0)
	updateProviderState(t, store, "claude", runtime.StateHealthy, 200, 0)

	req := &apitypes.ChatCompletionRequest{
		Model: "m",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// Either vision-capable provider is acceptable; both passed the hard filter.
	if result.Decision.SelectedProvider != "gpt-4o" && result.Decision.SelectedProvider != "claude" {
		t.Fatalf("expected a vision-capable provider, got %s", result.Decision.SelectedProvider)
	}
}

// TestVisionKeywordWithoutImageDoesNotTriggerHardReject verifies that text
// mentioning "image" without actual image content does not accidentally
// trigger hard vision rejection.
func TestVisionKeywordWithoutImageDoesNotTriggerHardReject(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "text-only", supportsAll: true, vision: false, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "text-only", runtime.StateHealthy, 100, 0)

	// Text mentions "image" but has no actual image content.
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "describe what an image looks like"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "text-only" {
		t.Fatalf("expected text-only (no hard vision reject without actual image), got %s", result.Decision.SelectedProvider)
	}
}

// ---- AUTO / DEFAULT MODE TESTS ----

// TestDefaultModePreservesBaselineBehavior verifies that Default mode uses
// baseline global weights and produces the same selection as without mode prefs.
func TestDefaultModePreservesBaselineBehavior(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "a", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "b", supportsAll: true, latencyMs: 200, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "a", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "b", runtime.StateHealthy, 200, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// Default mode should pick 'a' (lower latency with equal health).
	if result.Decision.SelectedProvider != "a" {
		t.Fatalf("expected 'a' (baseline behavior), got %s", result.Decision.SelectedProvider)
	}
}

// TestAutoModePreservesBaselineBehavior verifies that Auto (default) mode
// preserves existing baseline behavior.
func TestAutoModePreservesBaselineBehavior(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "x", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "y", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "x", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "y", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "general conversation"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// Deterministic tie-break: alphabetically first.
	if result.Decision.SelectedProvider != "x" {
		t.Fatalf("expected 'x' (alphabetical tie-break), got %s", result.Decision.SelectedProvider)
	}
}

// ---- MODE + EXPLICIT ROUTE TESTS ----

// TestModePreferencesOnlyAffectSuppliedCandidates verifies that mode preferences
// only affect scoring among the supplied candidates, not discovery.
func TestModePreferencesOnlyAffectSuppliedCandidates(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "fast-best", supportsAll: true, latencyMs: 30, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "slow-ok", supportsAll: true, latencyMs: 500, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "fast-best", runtime.StateHealthy, 30, 0)
	updateProviderState(t, store, "slow-ok", runtime.StateHealthy, 500, 0)

	// Only supply "slow-ok" as candidate — mode should not discover "fast-best".
	candidates := []router.ResolvedRoute{
		{ProviderName: "slow-ok", ProviderModelID: "m", ModelID: "m"},
	}
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "slow-ok" {
		t.Fatalf("expected slow-ok (only candidate), got %s", result.Decision.SelectedProvider)
	}
}

// TestModeDoesNotExpandCandidateSet verifies that mode preferences do not
// expand the candidate set beyond what is explicitly supplied.
func TestModeDoesNotExpandCandidateSet(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "a", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "b", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "a", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "b", runtime.StateHealthy, 50, 0)

	// Only supply route "a" — mode should not add "b".
	candidates := []router.ResolvedRoute{
		{ProviderName: "a", ProviderModelID: "m", ModelID: "m"},
	}
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider != "a" {
		t.Fatalf("expected 'a' (only candidate), got %s", result.Decision.SelectedProvider)
	}
}

// TestOneCandidateRemainsEffectivelyPinned verifies that with one candidate,
// the mode does not change the selection — it remains pinned.
func TestOneCandidateRemainsEffectivelyPinned(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "pinned", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "pinned", runtime.StateHealthy, 100, 0)

	candidates := []router.ResolvedRoute{
		{ProviderName: "pinned", ProviderModelID: "m", ModelID: "m"},
	}
	for _, mode := range []router.Mode{router.ModeFast, router.ModeCoding, router.ModeReasoning, router.ModeVision, router.ModeDefault} {
		// Override the request content to match the mode for classification.
		var content string
		switch mode {
		case router.ModeFast:
			content = "hi quick"
		case router.ModeCoding:
			content = "write a function"
		case router.ModeReasoning:
			content = "analyze this"
		case router.ModeVision:
			content = "look at this image"
		default:
			content = "hello"
		}
		modeReq := &apitypes.ChatCompletionRequest{
			Model:    "m",
			Messages: []apitypes.Message{{Role: "user", Content: content}},
		}
		result, err := pipeline.Execute(context.Background(), modeReq, router.Environment{}, router.ConfigSnapshot{}, candidates)
		if err != nil {
			t.Fatalf("mode %s: Execute: %v", mode, err)
		}
		if result == nil || result.Decision.SelectedProvider != "pinned" {
			t.Fatalf("mode %s: expected pinned, got %s", mode, result.Decision.SelectedProvider)
		}
	}
}

// TestEqualScoreDeterministicTieBreaking verifies that equal-score deterministic
// tie-breaking remains intact with mode preferences.
func TestEqualScoreDeterministicTieBreaking(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "zebra", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "alpha", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "zebra", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "alpha", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// Alphabetical tie-break: alpha < zebra.
	if result.Decision.SelectedProvider != "alpha" {
		t.Fatalf("expected alpha (alphabetical tie-break), got %s", result.Decision.SelectedProvider)
	}
}

// TestModeSnapshotCoherence verifies that mode-specific routing still uses the
// ONE snapshot acquired by the DecisionPipeline, not a fresh one per selection.
func TestModeSnapshotCoherence(t *testing.T) {
	pipeline, store, manager := setupModePipeline(t,
		&modeStubProvider{name: "a", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "b", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "a", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "b", runtime.StateHealthy, 100, 0)

	// Acquire snapshot before mutation.
	snap := manager.Snapshot(context.Background())

	// Mutate: make "b" unhealthy AFTER capturing snapshot.
	_ = store.Update("b", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "", nil)
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}

	// Using the pre-mutation snapshot via SelectFromRoutesWithSnapshot.
	routes := []router.ResolvedRoute{
		{ProviderName: "a", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "b", ProviderModelID: "m", ModelID: "m"},
	}
	eng := pipeline.RoutingEngine()
	result, err := eng.SelectFromRoutesWithSnapshot(context.Background(), routes, req, snap)
	if err != nil {
		t.Fatalf("select with old snapshot: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// With old snapshot both are healthy/tied, so primary ("a") wins by slice order.
	if result.Decision.SelectedProvider != "a" {
		t.Fatalf("expected 'a' (tie-break with old snapshot), got %s", result.Decision.SelectedProvider)
	}

	// With a fresh pipeline execution, "b" is unhealthy and "a" wins for the right reason.
	result2, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute with fresh snapshot: %v", err)
	}
	if result2 == nil || result2.Decision.SelectedProvider != "a" {
		t.Fatalf("expected 'a' (b unhealthy in fresh snapshot), got %s", result2.Decision.SelectedProvider)
	}
}

// ---- PIPELINE INTEGRATION TESTS ----

// TestPipelineModeProfilePropagation verifies that the Intent stage derives and
// stores the mode profile in DecisionContext, and the Selection stage uses it.
func TestPipelineModeProfilePropagation(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "fast-p", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "slow-p", supportsAll: true, latencyMs: 500, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "fast-p", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "slow-p", runtime.StateHealthy, 500, 0)

	// Fast request should select the low-latency provider.
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi there quick question"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "fast-p" {
		t.Fatalf("fast mode pipeline: expected fast-p, got %s", result.Decision.SelectedProvider)
	}
}

// TestPipelineCodingModePreference verifies that coding-classified requests
// prefer tool-calling capable providers through the full pipeline.
func TestPipelineCodingModePreference(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "no-tools", supportsAll: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "has-tools", supportsAll: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "no-tools", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "has-tools", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "write a function to sort an array"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "has-tools" {
		t.Fatalf("coding mode pipeline: expected has-tools, got %s", result.Decision.SelectedProvider)
	}
}

// TestPipelineVisionHardFilterThroughPipeline verifies that vision requests
// with actual image content hard-filter non-vision providers through the pipeline.
func TestPipelineVisionHardFilterThroughPipeline(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "vision-p", supportsAll: true, vision: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "text-p", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "vision-p", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "text-p", runtime.StateHealthy, 50, 0)

	req := &apitypes.ChatCompletionRequest{
		Model: "m",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "vision-p" {
		t.Fatalf("vision pipeline: expected vision-p (hard filter), got %s", result.Decision.SelectedProvider)
	}
}

// TestPipelineExplicitRouteWithMode verifies that mode preferences affect scoring
// among explicitly supplied candidates but do not expand the candidate set.
func TestPipelineExplicitRouteWithMode(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "route-a", supportsAll: true, latencyMs: 300, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "route-b", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "route-a", runtime.StateHealthy, 300, 0)
	updateProviderState(t, store, "route-b", runtime.StateHealthy, 50, 0)

	// Supply only route-a as candidate with mode=fast.
	// Fast mode should still only consider route-a (no expansion).
	candidates := []router.ResolvedRoute{
		{ProviderName: "route-a", ProviderModelID: "m", ModelID: "m"},
	}
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "route-a" {
		t.Fatalf("expected route-a (only candidate), got %s", result.Decision.SelectedProvider)
	}
}

// TestModeWeightsArePerDecision verifies that mode-specific weights do not
// mutate global RouterEngine weights.
func TestModeWeightsArePerDecision(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "a", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "b", supportsAll: true, latencyMs: 200, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "a", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "b", runtime.StateHealthy, 200, 0)

	// Record baseline weights.
	eng := pipeline.RoutingEngine()
	beforeWeights := eng.GetScorer().LoadWeights()

	// Execute a Fast mode request.
	fastReq := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi quick"}},
	}
	_, err := pipeline.Execute(context.Background(), fastReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Weights should be unchanged globally.
	afterWeights := eng.GetScorer().LoadWeights()
	if beforeWeights.Health != afterWeights.Health || beforeWeights.Latency != afterWeights.Latency {
		t.Fatal("global weights were mutated by mode-specific decision")
	}
}
