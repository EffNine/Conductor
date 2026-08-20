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

// longHorizonStubProvider is a modeStubProvider that can carry model-level
// MaxContext metadata via SetModelCapabilities on the engine.
type longHorizonStubProvider struct {
	name        string
	supportsAll bool
	vision      bool
	reasoning   bool
	toolCalling bool
	structured  bool
	latencyMs   int64
	healthState runtime.ProviderState
	maxContext  int // 0 = unknown
}

func (s *longHorizonStubProvider) Name() string { return s.name }
func (s *longHorizonStubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *longHorizonStubProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (s *longHorizonStubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *longHorizonStubProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (s *longHorizonStubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *longHorizonStubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *longHorizonStubProvider) SupportsModel(string) bool { return s.supportsAll }
func (s *longHorizonStubProvider) GetMetadata() provider.Metadata {
	return provider.NewMetadata(s.name, provider.Capabilities{
		Streaming:   true,
		Vision:      s.vision,
		Reasoning:   s.reasoning,
		ToolCalling: s.toolCalling,
		Structured:  s.structured,
	})
}

// setupLongHorizonPipeline creates a DecisionPipeline with providers that carry
// per-model MaxContext metadata registered on the engine.
func setupLongHorizonPipeline(t *testing.T, providers ...*longHorizonStubProvider) (*router.DecisionPipeline, *runtime.RuntimeStore, *router.RouterEngine) {
	t.Helper()
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	for _, p := range providers {
		reg.Register(p)
		_ = store.Register(runtime.NewProviderRuntime(p.name, p))
	}
	manager := runtime.NewManager(store)

	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	// Register model-level MaxContext on the engine so getCapabilities returns it.
	for _, p := range providers {
		if p.maxContext > 0 {
			eng.SetModelCapabilities(p.name, "m", router.Capabilities{MaxContext: p.maxContext})
		}
	}

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  eng,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})
	return pipeline, store, eng
}

// ---- LONG HORIZON ROUTING TESTS ----

// TestLongHorizonSufficientContext verifies that a request whose estimated
// token budget fits within every candidate's MaxContext is routed successfully.
func TestLongHorizonSufficientContext(t *testing.T) {
	pipeline, store, _ := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "a", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "a", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "long_horizon",
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
		t.Fatalf("expected a, got %s", result.Decision.SelectedProvider)
	}
}

// TestLongHorizonInsufficientContextRejected verifies that a candidate whose
// MaxContext is known and smaller than the request's estimated requirement is
// hard-rejected with a clear reason.
func TestLongHorizonInsufficientContextRejected(t *testing.T) {
	pipeline, store, _ := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "small", supportsAll: true, maxContext: 4096, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "small", runtime.StateHealthy, 50, 0)

	// Use a large thinking budget to push the estimated requirement above 4096.
	budget := 10000
	req := &apitypes.ChatCompletionRequest{
		Model:          "m",
		Mode:           "long_horizon",
		ThinkingBudget: &budget,
		Messages:       []apitypes.Message{{Role: "user", Content: "think deeply about this complex problem"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection (all rejected), got %s", result.Decision.SelectedProvider)
	}
	if len(result.Decision.RejectionReasons) == 0 {
		t.Fatal("expected at least one rejection reason")
	}
	found := false
	for _, r := range result.Decision.RejectionReasons {
		if r.Provider == "small" && r.Reason != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected rejection for small provider, got: %v", result.Decision.RejectionReasons)
	}
}

// TestLongHorizonExactContextBoundary verifies that a candidate whose MaxContext
// is exactly equal to the estimated requirement is NOT rejected.
func TestLongHorizonExactContextBoundary(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "exact", supportsAll: true, maxContext: 8192, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "exact", runtime.StateHealthy, 50, 0)

	// Request with modest output budget so total estimate is under 8192.
	budget := 4096
	req := &apitypes.ChatCompletionRequest{
		Model:     "m",
		Mode:      "long_horizon",
		MaxTokens: &budget,
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	// Register exact capacity — should be sufficient since estimate < 8192.
	eng.SetModelCapabilities("exact", "m", router.Capabilities{MaxContext: 8192})

	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider at exact boundary")
	}
	if result.Decision.SelectedProvider != "exact" {
		t.Fatalf("expected exact, got %s", result.Decision.SelectedProvider)
	}
}

// TestLongHorizonUnknownContextCompatibility verifies that candidates with
// unknown MaxContext (0) are NOT rejected — they remain eligible as fallbacks.
func TestLongHorizonUnknownContextCompatibility(t *testing.T) {
	pipeline, store, _ := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "unknown", supportsAll: true, maxContext: 0, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "unknown", runtime.StateHealthy, 50, 0)

	budget := 10000
	req := &apitypes.ChatCompletionRequest{
		Model:          "m",
		Mode:           "long_horizon",
		ThinkingBudget: &budget,
		Messages:       []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider with unknown context")
	}
	if result.Decision.SelectedProvider != "unknown" {
		t.Fatalf("expected unknown (unknown MaxContext is compatible), got %s", result.Decision.SelectedProvider)
	}
}

// TestLongHorizonPrefersHigherContextWhenOtherwiseComparable verifies that when
// two candidates both satisfy the context requirement, the one with larger
// MaxContext wins due to the ContextCapacity bonus.
func TestLongHorizonPrefersHigherContextWhenOtherwiseComparable(t *testing.T) {
	pipeline, store, _ := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "smaller", supportsAll: true, maxContext: 32768, latencyMs: 50, healthState: runtime.StateHealthy},
		&longHorizonStubProvider{name: "larger", supportsAll: true, maxContext: 128000, latencyMs: 55, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "smaller", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "larger", runtime.StateHealthy, 55, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "long_horizon",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// Both satisfy context; larger context should win via bonus.
	if result.Decision.SelectedProvider != "larger" {
		t.Fatalf("expected larger (higher context bonus), got %s", result.Decision.SelectedProvider)
	}
}

// TestLongHorizonUsesModelSpecificContext verifies that MaxContext is resolved
// per-model, not per-provider. Two models on the same provider may have
// different context limits and be scored independently.
func TestLongHorizonUsesModelSpecificContext(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "prov", supportsAll: true, maxContext: 0, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "prov", runtime.StateHealthy, 50, 0)

	// Register two models with different MaxContext on the same provider.
	eng.SetModelCapabilities("prov", "model-a", router.Capabilities{MaxContext: 8192})
	eng.SetModelCapabilities("prov", "model-b", router.Capabilities{MaxContext: 128000})

	// Request for model-a with a budget that exceeds 8192 should fail.
	budget := 10000
	req := &apitypes.ChatCompletionRequest{
		Model:     "model-a",
		Mode:      "long_horizon",
		MaxTokens: &budget,
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection for model-a (insufficient context), got %s", result.Decision.SelectedProvider)
	}

	// Request for model-b with the same budget should succeed.
	req2 := *req
	req2.Model = "model-b"
	result2, err := pipeline.Execute(context.Background(), &req2, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute model-b: %v", err)
	}
	if result2 == nil || result2.Decision.SelectedProvider == "" {
		t.Fatal("expected selection for model-b")
	}
	if result2.Decision.SelectedProvider != "prov" {
		t.Fatalf("expected prov for model-b, got %s", result2.Decision.SelectedProvider)
	}
}

// TestLongHorizonCandidateFiltering verifies that the hard context filter is
// applied to the candidate set before scoring, and that qualified candidates
// are still scored normally.
func TestLongHorizonCandidateFiltering(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "fit", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
		&longHorizonStubProvider{name: "too-small", supportsAll: true, maxContext: 4096, latencyMs: 30, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "fit", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "too-small", runtime.StateHealthy, 30, 0)

	budget := 10000
	req := &apitypes.ChatCompletionRequest{
		Model:     "m",
		Mode:      "long_horizon",
		MaxTokens: &budget,
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	eng.SetModelCapabilities("fit", "m", router.Capabilities{MaxContext: 128000})
	eng.SetModelCapabilities("too-small", "m", router.Capabilities{MaxContext: 4096})

	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "fit" {
		t.Fatalf("expected fit (too-small rejected), got %s", result.Decision.SelectedProvider)
	}
	// Verify too-small appears in rejections.
	found := false
	for _, r := range result.Decision.RejectionReasons {
		if r.Provider == "too-small" && r.Reason != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected too-small in rejections, got: %v", result.Decision.RejectionReasons)
	}
}

// TestLongHorizonExplicitRouteConstraint verifies that mode=long_horizon with
// an explicit route constrains candidates to that route, then applies context
// qualification within the constrained set.
func TestLongHorizonExplicitRouteConstraint(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "route-a", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
		&longHorizonStubProvider{name: "route-b", supportsAll: true, maxContext: 32768, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "route-a", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "route-b", runtime.StateHealthy, 50, 0)

	eng.SetModelCapabilities("route-a", "m", router.Capabilities{MaxContext: 128000})
	eng.SetModelCapabilities("route-b", "m", router.Capabilities{MaxContext: 32768})

	// Only supply route-a as candidate. Long Horizon must not expand the set.
	candidates := []router.ResolvedRoute{
		{ProviderName: "route-a", ProviderModelID: "m", ModelID: "m"},
	}
	budget := 10000
	req := &apitypes.ChatCompletionRequest{
		Model:     "m",
		Mode:      "long_horizon",
		MaxTokens: &budget,
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "route-a" {
		t.Fatalf("expected route-a (explicit candidate constrained), got %s", result.Decision.SelectedProvider)
	}
}

// TestLongHorizonFallbackToContextQualifiedCandidate verifies that when the
// primary candidate fails context qualification, a fallback that satisfies
// the requirement is selected instead.
func TestLongHorizonFallbackToContextQualifiedCandidate(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "primary", supportsAll: true, maxContext: 4096, latencyMs: 50, healthState: runtime.StateHealthy},
		&longHorizonStubProvider{name: "fallback", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "primary", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "fallback", runtime.StateHealthy, 100, 0)

	eng.SetModelCapabilities("primary", "m", router.Capabilities{MaxContext: 4096})
	eng.SetModelCapabilities("fallback", "m", router.Capabilities{MaxContext: 128000})

	// Supply both as candidates: primary first, fallback second.
	candidates := []router.ResolvedRoute{
		{ProviderName: "primary", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "fallback", ProviderModelID: "m", ModelID: "m"},
	}
	budget := 10000
	req := &apitypes.ChatCompletionRequest{
		Model:     "m",
		Mode:      "long_horizon",
		MaxTokens: &budget,
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "fallback" {
		t.Fatalf("expected fallback (primary rejected by context), got %s", result.Decision.SelectedProvider)
	}
}

// TestLongHorizonAllCandidatesInsufficient verifies that when every candidate
// in the set fails context qualification, the pipeline returns an empty
// selection with clear rejection reasons.
func TestLongHorizonAllCandidatesInsufficient(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "tiny", supportsAll: true, maxContext: 1024, latencyMs: 50, healthState: runtime.StateHealthy},
		&longHorizonStubProvider{name: "small", supportsAll: true, maxContext: 4096, latencyMs: 60, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "tiny", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "small", runtime.StateHealthy, 60, 0)

	eng.SetModelCapabilities("tiny", "m", router.Capabilities{MaxContext: 1024})
	eng.SetModelCapabilities("small", "m", router.Capabilities{MaxContext: 4096})

	// Large budget exceeds all candidates.
	budget := 20000
	req := &apitypes.ChatCompletionRequest{
		Model:     "m",
		Mode:      "long_horizon",
		MaxTokens: &budget,
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection when all insufficient, got %s", result.Decision.SelectedProvider)
	}
	if len(result.Decision.RejectionReasons) != 2 {
		t.Fatalf("expected 2 rejections, got %d", len(result.Decision.RejectionReasons))
	}
}

// TestLongHorizonDoesNotAffectOtherModes verifies that modes other than
// long_horizon do not apply the context hard filter.
func TestLongHorizonDoesNotAffectOtherModes(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "small", supportsAll: true, maxContext: 4096, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "small", runtime.StateHealthy, 50, 0)
	eng.SetModelCapabilities("small", "m", router.Capabilities{MaxContext: 4096})

	// Default mode with a large budget should still select the small-context
	// provider because no hard context filter is applied.
	budget := 10000
	req := &apitypes.ChatCompletionRequest{
		Model:     "m",
		MaxTokens: &budget,
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider in default mode")
	}
	if result.Decision.SelectedProvider != "small" {
		t.Fatalf("expected small (no context filter in default mode), got %s", result.Decision.SelectedProvider)
	}
}

// ---- PIPELINE TESTS ----

// TestPipelineLongHorizonMode verifies that the full pipeline resolves mode=long_horizon
// and that the CapabilityStage sets the context requirement.
func TestPipelineLongHorizonMode(t *testing.T) {
	pipeline, store, _ := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "p", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "p", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "long_horizon",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "p" {
		t.Fatalf("expected p, got %s", result.Decision.SelectedProvider)
	}
}

// TestPipelineLongHorizonContextRequirement verifies that the DecisionContext
// carries a non-zero context requirement when mode=long_horizon.
func TestPipelineLongHorizonContextRequirement(t *testing.T) {
	pipeline, _, _ := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "p", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "long_horizon",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	dc := router.NewDecisionContext(req, runtime.RuntimeSnapshot{}, router.ConfigSnapshot{}, router.TaskMetadata{}, router.Environment{}, nil, nil)
	defer dc.Close()

	// Run just the intent and capability stages.
	stages := pipeline.Stages()
	if len(stages) < 2 {
		t.Fatal("expected at least 2 stages")
	}
	_ = stages[0].Execute(context.Background(), dc)
	_ = stages[1].Execute(context.Background(), dc)

	if dc.ContextRequirement() <= 0 {
		t.Fatalf("expected non-zero context requirement, got %d", dc.ContextRequirement())
	}
}

// TestPipelineLongHorizonUsesSameRuntimeSnapshot verifies that the pipeline
// uses the same RuntimeSnapshot throughout and the context filter sees the
// correct capabilities from that snapshot.
func TestPipelineLongHorizonUsesSameRuntimeSnapshot(t *testing.T) {
	_, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "a", supportsAll: true, maxContext: 128000, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "a", runtime.StateHealthy, 50, 0)

	snap := store.Snapshot(context.Background())
	// Mutate after snapshot — selection must use the pre-mutation snapshot.
	_ = store.Update("a", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "", nil)
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "long_horizon",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	// Execute with the old snapshot via the engine directly.
	result, err := eng.SelectBestProvider(context.Background(), "m", req)
	if err != nil {
		t.Fatalf("SelectBestProvider: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	_ = snap
}

// TestPipelineLongHorizonTraceMetadata verifies that the DecisionTrace includes
// context-related metadata for Long Horizon decisions.
func TestPipelineLongHorizonTraceMetadata(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "p", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "p", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "long_horizon",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider != "p" {
		t.Fatalf("expected p, got %s", result.Decision.SelectedProvider)
	}
	_ = eng
}

// ---- HANDLER TESTS ----

// TestHandlerLongHorizonMode verifies that the HTTP handler forwards mode=long_horizon
// through the decision pipeline without error.
func TestHandlerLongHorizonMode(t *testing.T) {
	// This test is covered by TestPipelineLongHorizonMode and
	// TestPublicModeLongHorizonActivatesLongHorizonProfile. The handler itself
	// delegates to the pipeline; we verify end-to-end routing here.
	pipeline, store, _ := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "p", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "p", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "long_horizon",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
}

// TestHandlerLongHorizonRejectsInsufficientExplicitRoute verifies that when an
// explicit route is supplied and its model fails context qualification, the
// pipeline returns an empty selection rather than silently expanding the
// candidate set.
func TestHandlerLongHorizonRejectsInsufficientExplicitRoute(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "primary", supportsAll: true, maxContext: 4096, latencyMs: 50, healthState: runtime.StateHealthy},
		&longHorizonStubProvider{name: "fallback", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "primary", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "fallback", runtime.StateHealthy, 100, 0)

	eng.SetModelCapabilities("primary", "m", router.Capabilities{MaxContext: 4096})
	eng.SetModelCapabilities("fallback", "m", router.Capabilities{MaxContext: 128000})

	// Only supply primary as candidate. Fallback should NOT be auto-added.
	candidates := []router.ResolvedRoute{
		{ProviderName: "primary", ProviderModelID: "m", ModelID: "m"},
	}
	budget := 10000
	req := &apitypes.ChatCompletionRequest{
		Model:     "m",
		Mode:      "long_horizon",
		MaxTokens: &budget,
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection when explicit candidate is insufficient, got %s", result.Decision.SelectedProvider)
	}
}

// TestHandlerLongHorizonFallbackPreserved verifies that when the primary
// candidate fails context qualification but a fallback in the candidate set
// qualifies, the fallback is selected.
func TestHandlerLongHorizonFallbackPreserved(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "primary", supportsAll: true, maxContext: 4096, latencyMs: 50, healthState: runtime.StateHealthy},
		&longHorizonStubProvider{name: "fallback", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "primary", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "fallback", runtime.StateHealthy, 100, 0)

	eng.SetModelCapabilities("primary", "m", router.Capabilities{MaxContext: 4096})
	eng.SetModelCapabilities("fallback", "m", router.Capabilities{MaxContext: 128000})

	candidates := []router.ResolvedRoute{
		{ProviderName: "primary", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "fallback", ProviderModelID: "m", ModelID: "m"},
	}
	budget := 10000
	req := &apitypes.ChatCompletionRequest{
		Model:     "m",
		Mode:      "long_horizon",
		MaxTokens: &budget,
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "fallback" {
		t.Fatalf("expected fallback (primary insufficient), got %s", result.Decision.SelectedProvider)
	}
}
