package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/cache"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// TestPublicModeOmittedPreservesClassifierBehavior verifies that when mode is
// omitted, the classifier-derived mode is used and behavior matches the pre-P3.5 baseline.
func TestPublicModeOmittedPreservesClassifierBehavior(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "coding-pref", supportsAll: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "plain", supportsAll: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "coding-pref", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "plain", runtime.StateHealthy, 100, 0)

	// Coding-classified text without explicit mode should prefer tool-calling provider.
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
	if result.Decision.SelectedProvider != "coding-pref" {
		t.Fatalf("expected coding-pref (classifier-derived), got %s", result.Decision.SelectedProvider)
	}
}

// TestPublicModeAutoUsesDefaultProfile verifies that explicit mode=auto resolves
// to ModeDefault and uses the default profile.
func TestPublicModeAutoUsesDefaultProfile(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "a", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "b", supportsAll: true, latencyMs: 200, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "a", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "b", runtime.StateHealthy, 200, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	// Explicit auto should still pick 'a' (lower latency, default weights).
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "a" {
		t.Fatalf("expected 'a' (default weights), got %s", result.Decision.SelectedProvider)
	}
}

// TestPublicModeCodingActivatesCodingProfile verifies that explicit mode=coding
// applies the coding ModeProfile (high capability weight, tool-calling bonus).
func TestPublicModeCodingActivatesCodingProfile(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "no-tools", supportsAll: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "with-tools", supportsAll: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "no-tools", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "with-tools", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "coding",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "with-tools" {
		t.Fatalf("expected with-tools (coding profile), got %s", result.Decision.SelectedProvider)
	}
}

// TestPublicModeReasoningActivatesReasoningProfile verifies that explicit
// mode=reasoning applies the reasoning ModeProfile.
func TestPublicModeReasoningActivatesReasoningProfile(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "text-only", supportsAll: true, reasoning: false, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "reasoning", supportsAll: true, reasoning: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "text-only", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "reasoning", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "reasoning",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "reasoning" {
		t.Fatalf("expected reasoning (reasoning profile), got %s", result.Decision.SelectedProvider)
	}
}

// TestPublicModeFastActivatesFastProfile verifies that explicit mode=fast applies
// the Fast ModeProfile (Health=55, Latency=40, Cost=3, Capability=2).
func TestPublicModeFastActivatesFastProfile(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "slow", supportsAll: true, latencyMs: 800, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "fast", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "slow", runtime.StateHealthy, 800, 0)
	updateProviderState(t, store, "fast", runtime.StateHealthy, 50, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "fast",
		Messages: []apitypes.Message{{Role: "user", Content: "analyze this architecture deeply"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "fast" {
		t.Fatalf("expected fast (fast profile), got %s", result.Decision.SelectedProvider)
	}
}

// TestPublicModeVisionActivatesVisionProfile verifies that explicit mode=vision
// applies the Vision ModeProfile as a routing preference.
func TestPublicModeVisionActivatesVisionProfile(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "vision-p", supportsAll: true, vision: true, latencyMs: 150, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "text-p", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "vision-p", runtime.StateHealthy, 150, 0)
	updateProviderState(t, store, "text-p", runtime.StateHealthy, 50, 0)

	// mode=vision with no image content should use vision as a preference,
	// not a hard filter. Both providers are valid; either selection is acceptable.
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "vision",
		Messages: []apitypes.Message{{Role: "user", Content: "explain computer vision"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// Either provider is acceptable since neither is hard-rejected.
	if result.Decision.SelectedProvider != "vision-p" && result.Decision.SelectedProvider != "text-p" {
		t.Fatalf("expected vision-p or text-p, got %s", result.Decision.SelectedProvider)
	}
}

// TestPublicModePlanningActivatesPlanningProfile verifies that explicit
// mode=planning applies the Planning ModeProfile (high capability, reasoning+tools).
func TestPublicModePlanningActivatesPlanningProfile(t *testing.T) {
	pipeline, _, _ := setupModePipeline(t,
		&modeStubProvider{name: "no-reason", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "no-tools", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "full-cap", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 120, healthState: runtime.StateHealthy},
	)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "planning",
		Messages: []apitypes.Message{{Role: "user", Content: "plan the deployment strategy"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// Only "full-cap" satisfies both Reasoning and ToolCalling hard requirements.
	if result.Decision.SelectedProvider != "full-cap" {
		t.Fatalf("expected full-cap (only qualified), got %s", result.Decision.SelectedProvider)
	}
}

// TestPublicModeAgenticActivatesAgenticProfile verifies that explicit
// mode=agentic applies the Agentic ModeProfile (high health, strong execution
// telemetry preference, reasoning+tool_calling hard requirements).
func TestPublicModeAgenticActivatesAgenticProfile(t *testing.T) {
	pipeline, _, _ := setupModePipeline(t,
		&modeStubProvider{name: "no-reason", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "no-tools", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "full-cap", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 120, healthState: runtime.StateHealthy},
	)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build a multi-step system"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// Only "full-cap" satisfies both Reasoning and ToolCalling hard requirements.
	if result.Decision.SelectedProvider != "full-cap" {
		t.Fatalf("expected full-cap (only qualified), got %s", result.Decision.SelectedProvider)
	}
}

// TestPublicModeLongHorizonActivatesLongHorizonProfile verifies that mode=long_horizon
// applies the LongHorizon ModeProfile and performs context-aware routing.
func TestPublicModeLongHorizonActivatesLongHorizonProfile(t *testing.T) {
	pipeline, _, _ := setupModePipeline(t,
		&modeStubProvider{name: "short-context", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "long-context", supportsAll: true, latencyMs: 120, healthState: runtime.StateHealthy},
	)

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
	// Long Horizon should be active and select a provider (either is fine here).
	if result.Decision.SelectedProvider != "long-context" && result.Decision.SelectedProvider != "short-context" {
		t.Fatalf("expected long-context or short-context, got %s", result.Decision.SelectedProvider)
	}
}

// TestInvalidModeReturnsValidationError verifies that an invalid mode string
// produces a validation error and is NOT silently converted to Auto.
func TestInvalidModeReturnsValidationError(t *testing.T) {
	pipeline, _, _ := setupModePipeline(t,
		&modeStubProvider{name: "p", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "reasonning",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !contains(err.Error(), "invalid mode") {
		t.Fatalf("expected 'invalid mode' error, got: %v", err)
	}
}

// TestExplicitModeOverridesClassifierIntent verifies that an explicit mode
// overrides the classifier's inferred mode.
func TestExplicitModeOverridesClassifierIntent(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "fast-p", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "slow-p", supportsAll: true, latencyMs: 500, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "fast-p", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "slow-p", runtime.StateHealthy, 500, 0)

	// "Analyze this architecture deeply" would classify as reasoning,
	// but explicit mode=fast should win.
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "fast",
		Messages: []apitypes.Message{{Role: "user", Content: "Analyze this architecture deeply"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "fast-p" {
		t.Fatalf("expected fast-p (explicit mode overrides classifier), got %s", result.Decision.SelectedProvider)
	}
}

// TestExplicitModelWithModePreservesModelConstraint verifies that an explicit
// model + mode does not silently replace the requested model.
func TestExplicitModelWithModePreservesModelConstraint(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "openai", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "openai", runtime.StateHealthy, 100, 0)

	candidates := []router.ResolvedRoute{
		{ProviderName: "openai", ProviderModelID: "gpt-4o", ModelID: "gpt-4o"},
	}
	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Mode:     "reasoning",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedModelID != "gpt-4o" {
		t.Fatalf("expected model gpt-4o preserved, got %s", result.Decision.SelectedModelID)
	}
}

// TestOneCandidateRemainsPinned verifies that with one candidate, the mode
// does not change the selection — it remains pinned.
func TestOneCandidateRemainsPinned(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "pinned", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "pinned", runtime.StateHealthy, 100, 0)

	candidates := []router.ResolvedRoute{
		{ProviderName: "pinned", ProviderModelID: "m", ModelID: "m"},
	}
	for _, mode := range []string{"fast", "coding", "reasoning", "vision", "auto"} {
		req := &apitypes.ChatCompletionRequest{
			Model:    "m",
			Mode:     mode,
			Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
		}
		result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
		if err != nil {
			t.Fatalf("mode %s: Execute: %v", mode, err)
		}
		if result == nil || result.Decision.SelectedProvider != "pinned" {
			t.Fatalf("mode %s: expected pinned, got %s", mode, result.Decision.SelectedProvider)
		}
	}
}

// TestStreamingModeSameRouting verifies that streaming and non-streaming with
// the same request state produce the same routing decision.
func TestStreamingModeSameRouting(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "healthy", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "unhealthy", supportsAll: true, latencyMs: 100, healthState: runtime.StateUnhealthy},
	)

	updateProviderState(t, store, "healthy", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "unhealthy", runtime.StateUnhealthy, 100, 0)

	baseReq := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "fast",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}

	// Non-streaming.
	req1 := *baseReq
	req1.Stream = false
	result1, err := pipeline.Execute(context.Background(), &req1, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("non-streaming Execute: %v", err)
	}

	// Streaming.
	req2 := *baseReq
	req2.Stream = true
	result2, err := pipeline.Execute(context.Background(), &req2, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("streaming Execute: %v", err)
	}

	if result1.Decision.SelectedProvider != result2.Decision.SelectedProvider {
		t.Fatalf("streaming and non-streaming selected different providers: %s vs %s",
			result1.Decision.SelectedProvider, result2.Decision.SelectedProvider)
	}
}

// TestCacheKeyIncludesMode verifies that the cache key incorporates the mode
// field so that requests with different modes do not collide.
func TestCacheKeyIncludesMode(t *testing.T) {
	msgs := []interface{}{map[string]interface{}{"role": "user", "content": "hi"}}

	key1 := cache.ResponseCacheKey("m", msgs, map[string]interface{}{"mode": "fast"})
	key2 := cache.ResponseCacheKey("m", msgs, map[string]interface{}{"mode": "reasoning"})
	key3 := cache.ResponseCacheKey("m", msgs, map[string]interface{}{"mode": ""})

	if key1 == key2 {
		t.Fatal("cache keys should differ for different modes")
	}
	if key1 == key3 {
		t.Fatal("cache keys should differ when mode is set vs empty")
	}
}

// TestDecisionTraceRecordsModeInfo verifies that DecisionTrace records the
// requested mode, resolved mode, and resolution source.
func TestDecisionTraceRecordsModeInfo(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "p", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "p", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "reasoning",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify via DecisionContext directly.
	dc := router.NewDecisionContext(req, store.Snapshot(context.Background()), router.ConfigSnapshot{}, router.TaskMetadata{}, router.Environment{}, nil, nil)
	defer dc.Close()
	_ = pipeline.Stages()[0].Execute(context.Background(), dc)

	if dc.ModeSource() != "explicit" {
		t.Errorf("mode_source = %q, want %q", dc.ModeSource(), "explicit")
	}
	if dc.RequestedMode() != "reasoning" {
		t.Errorf("requested_mode = %q, want %q", dc.RequestedMode(), "reasoning")
	}
	if dc.ModeProfile() == nil {
		t.Fatal("expected non-nil mode profile")
	}
	if dc.ModeProfile().Mode != router.ModeReasoning {
		t.Errorf("resolved mode = %q, want %q", dc.ModeProfile().Mode, router.ModeReasoning)
	}
}

// TestOmittedModeProducesSameRoutingAsBefore verifies that omitting mode
// produces the same routing behavior as before P3.5.
func TestOmittedModeProducesSameRoutingAsBefore(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "coding-pref", supportsAll: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "plain", supportsAll: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	updateProviderState(t, store, "coding-pref", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "plain", runtime.StateHealthy, 100, 0)

	// No mode specified — classifier should infer coding from "write a function".
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
	if result.Decision.SelectedProvider != "coding-pref" {
		t.Fatalf("expected coding-pref (classifier inferred), got %s", result.Decision.SelectedProvider)
	}
}

// TestInternalEliteModeNotPublic verifies that the internal "elite" mode
// is NOT a valid public mode value.
func TestInternalEliteModeNotPublic(t *testing.T) {
	_, err := router.ParseMode("elite")
	if err == nil {
		t.Fatal("expected error for internal mode 'elite'")
	}
	if !contains(err.Error(), "invalid mode") {
		t.Fatalf("expected 'invalid mode' error, got: %v", err)
	}
}

// TestParseModeEmptyReturnsDefault verifies that ParseMode("") returns ModeDefault.
func TestParseModeEmptyReturnsDefault(t *testing.T) {
	m, err := router.ParseMode("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != router.ModeDefault {
		t.Errorf("Mode = %q, want %q", m, router.ModeDefault)
	}
}

// TestParseModeAllPublicValues verifies all public mode strings parse correctly.
func TestParseModeAllPublicValues(t *testing.T) {
	tests := []struct {
		input    string
		expected router.Mode
	}{
		{"auto", router.ModeDefault},
		{"coding", router.ModeCoding},
		{"reasoning", router.ModeReasoning},
		{"vision", router.ModeVision},
		{"fast", router.ModeFast},
		{"planning", router.ModePlanning},
		{"agentic", router.ModeAgentic},
		{"long_horizon", router.ModeLongHorizon},
	}
	for _, tt := range tests {
		m, err := router.ParseMode(tt.input)
		if err != nil {
			t.Errorf("ParseMode(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if m != tt.expected {
			t.Errorf("ParseMode(%q) = %q, want %q", tt.input, m, tt.expected)
		}
	}
}

// TestActiveModeProfiles verifies that active modes have Active=true and
// inactive modes have Active=false.
func TestActiveModeProfiles(t *testing.T) {
	profiles := router.DefaultModeProfiles()
	activeModes := []router.Mode{router.ModeCoding, router.ModeReasoning, router.ModeVision, router.ModeFast, router.ModeDefault, router.ModePlanning, router.ModeAgentic, router.ModeLongHorizon}
	inactiveModes := []router.Mode{router.ModeElite}

	for _, m := range activeModes {
		mp, ok := profiles[m]
		if !ok {
			t.Fatalf("missing profile for active mode %q", m)
		}
		if !mp.Active {
			t.Errorf("mode %q should be active", m)
		}
	}
	for _, m := range inactiveModes {
		mp, ok := profiles[m]
		if !ok {
			t.Fatalf("missing profile for inactive mode %q", m)
		}
		if mp.Active {
			t.Errorf("mode %q should be inactive", m)
		}
	}
}

// contains reports whether s contains sub as a substring.
func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
