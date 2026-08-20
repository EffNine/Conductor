package router

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/provider"
)

// --- helper: build a RouterEngine with given registry -----------------------

func newTestRouter(reg *provider.Registry) *RouterEngine {
	return NewRouterEngine(RouterEngineConfig{
		Registry: reg,
	})
}

// registerModelCaps is a test helper that registers model-level capabilities
// with both the provider's ListModels() and the registry's model-capabilities store.
func registerModelCaps(t *testing.T, reg *provider.Registry, p *stubProviderWithModelCaps) {
	t.Helper()
	if p.modelID != "" && p.capabilities != nil {
		reg.SetModelCapabilities(p.name, p.modelID, provider.Capabilities{
			Streaming:   p.capabilities.Streaming,
			Vision:      p.capabilities.Vision,
			Reasoning:   p.capabilities.Reasoning,
			ToolCalling: p.capabilities.ToolCalling || p.capabilities.Functions,
			Structured:  p.capabilities.Structured,
			LongContext: p.capabilities.LongContext,
			Embeddings:  p.capabilities.Embeddings,
			Images:      p.capabilities.Images,
			Audio:       p.capabilities.Audio,
			Functions:   p.capabilities.Functions,
		}, p.maxContext)
	}
}

// --- 1. Model capability metadata is preserved -------------------------------

func TestModelCapabilityMetadataPreserved(t *testing.T) {
	reg := provider.NewRegistry()
	visionCaps := &provider.Capabilities{Streaming: true, Vision: true, ToolCalling: true}
	p := &stubProviderWithModelCaps{
		name:         "openai",
		modelID:      "gpt-4o-vision",
		capabilities: visionCaps,
		maxContext:   128000,
	}
	reg.Register(p)
	registerModelCaps(t, reg, p)

	re := newTestRouter(reg)
	caps := re.getCapabilities("openai", "gpt-4o-vision")

	if !caps.Vision {
		t.Fatal("expected Vision=true from model metadata")
	}
	if caps.MaxContext != 128000 {
		t.Fatalf("expected MaxContext=128000, got %d", caps.MaxContext)
	}
}

// --- 2. Provider defaults are used when model metadata is unknown ------------

func TestProviderDefaultsUsedWhenModelMetadataUnknown(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProviderWithModelCaps{
		name: "openai",
		// No model-level caps set — should fall back to provider defaults.
	})

	re := newTestRouter(reg)
	caps := re.getCapabilities("openai", "some-model")

	// OpenAI provider default has Streaming, ToolCalling, Structured, Reasoning, Vision.
	if !caps.Streaming {
		t.Fatal("expected Streaming from provider default")
	}
	if !caps.ToolCalling {
		t.Fatal("expected ToolCalling from provider default")
	}
}

// --- 3. Explicit model capability overrides provider default -----------------

func TestModelOverrideOverridesProviderDefault(t *testing.T) {
	reg := provider.NewRegistry()
	// Model explicitly disables Vision despite provider supporting it.
	noVisionCaps := &provider.Capabilities{Streaming: true, Vision: false, ToolCalling: true}
	p := &stubProviderWithModelCaps{
		name:         "openai",
		modelID:      "text-only-model",
		capabilities: noVisionCaps,
	}
	reg.Register(p)
	registerModelCaps(t, reg, p)

	re := newTestRouter(reg)
	caps := re.getCapabilities("openai", "text-only-model")

	// Model explicitly says Vision=false — should override provider's Vision=true.
	if caps.Vision {
		t.Fatal("expected Vision=false from explicit model override")
	}
	if !caps.Streaming {
		t.Fatal("expected Streaming to remain from provider default")
	}
}

// --- 4. Unknown capability does not become false accidentally ----------------

func TestUnknownCapabilityDoesNotBecomeFalse(t *testing.T) {
	reg := provider.NewRegistry()
	// Provider supports Vision; model has no metadata.
	reg.Register(&stubProviderWithModelCaps{
		name: "openai",
	})

	re := newTestRouter(reg)
	caps := re.getCapabilities("openai", "unknown-model")

	// Should retain provider default (Vision=true for openai), not become false.
	if !caps.Vision {
		t.Fatal("expected Vision=true from provider default, not accidentally false")
	}
}

// --- 5. Vision model-level capability works ----------------------------------

func TestVisionModelLevelCapability(t *testing.T) {
	reg := provider.NewRegistry()
	visionOnly := &provider.Capabilities{Streaming: true, Vision: true}
	p := &stubProviderWithModelCaps{
		name:         "generic",
		modelID:      "my-vision-model",
		capabilities: visionOnly,
	}
	reg.Register(p)
	registerModelCaps(t, reg, p)

	re := newTestRouter(reg)
	caps := re.getCapabilities("generic", "my-vision-model")

	if !caps.Vision {
		t.Fatal("expected Vision=true from model metadata")
	}
}

// --- 6. Reasoning model-level capability works -------------------------------

func TestReasoningModelLevelCapability(t *testing.T) {
	reg := provider.NewRegistry()
	reasoningCaps := &provider.Capabilities{Streaming: true, Reasoning: true}
	p := &stubProviderWithModelCaps{
		name:         "deepseek",
		modelID:      "deepseek-r1",
		capabilities: reasoningCaps,
	}
	reg.Register(p)
	registerModelCaps(t, reg, p)

	re := newTestRouter(reg)
	caps := re.getCapabilities("deepseek", "deepseek-r1")

	if !caps.Reasoning {
		t.Fatal("expected Reasoning=true from model metadata")
	}
}

// --- 7. ToolCalling model-level capability works -----------------------------

func TestToolCallingModelLevelCapability(t *testing.T) {
	reg := provider.NewRegistry()
	toolCaps := &provider.Capabilities{Streaming: true, ToolCalling: true, Functions: true}
	p := &stubProviderWithModelCaps{
		name:         "groq",
		modelID:      "llama-tool-model",
		capabilities: toolCaps,
	}
	reg.Register(p)
	registerModelCaps(t, reg, p)

	re := newTestRouter(reg)
	caps := re.getCapabilities("groq", "llama-tool-model")

	if !caps.ToolCalling {
		t.Fatal("expected ToolCalling=true from model metadata")
	}
}

// --- 8. MaxContextLength is preserved ----------------------------------------

func TestMaxContextLengthPreserved(t *testing.T) {
	reg := provider.NewRegistry()
	p := &stubProviderWithModelCaps{
		name:         "openai",
		modelID:      "gpt-4o",
		capabilities: &provider.Capabilities{Streaming: true},
		maxContext:   128000,
	}
	reg.Register(p)
	registerModelCaps(t, reg, p)

	re := newTestRouter(reg)
	caps := re.getCapabilities("openai", "gpt-4o")

	if caps.MaxContext != 128000 {
		t.Fatalf("expected MaxContext=128000, got %d", caps.MaxContext)
	}
}

// --- 9. LongContext is preserved ---------------------------------------------

func TestLongContextPreserved(t *testing.T) {
	reg := provider.NewRegistry()
	// Provider default has LongContext=false, but model explicitly enables it.
	modelCaps := &provider.Capabilities{Streaming: true, LongContext: true}
	p := &stubProviderWithModelCaps{
		name:         "ollama",
		modelID:      "long-context-model",
		capabilities: modelCaps,
	}
	reg.Register(p)
	registerModelCaps(t, reg, p)

	re := newTestRouter(reg)
	caps := re.getCapabilities("ollama", "long-context-model")

	if !caps.LongContext {
		t.Fatal("expected LongContext=true from model override")
	}
}

// --- 10. Embeddings/Images/Audio metadata is not silently lost ----------------

func TestEmbeddingsImagesAudioNotLost(t *testing.T) {
	reg := provider.NewRegistry()
	fullCaps := &provider.Capabilities{
		Streaming: true, Embeddings: true, Images: true, Audio: true,
	}
	p := &stubProviderWithModelCaps{
		name:         "gemini",
		modelID:      "multimodal-model",
		capabilities: fullCaps,
	}
	reg.Register(p)
	registerModelCaps(t, reg, p)

	re := newTestRouter(reg)
	caps := re.getCapabilities("gemini", "multimodal-model")

	if !caps.Embeddings {
		t.Fatal("expected Embeddings=true, was silently lost")
	}
	if !caps.Images {
		t.Fatal("expected Images=true, was silently lost")
	}
	if !caps.Audio {
		t.Fatal("expected Audio=true, was silently lost")
	}
}

// --- 11. Existing heuristic fallback still works when metadata is absent -----

func TestHeuristicFallbackStillWorks(t *testing.T) {
	reg := provider.NewRegistry()
	// No model-level caps — should fall back to heuristics.
	reg.Register(&stubProviderWithModelCaps{
		name: "generic",
	})

	re := newTestRouter(reg)

	// Model name contains "vision" → heuristic should set Vision=true.
	caps := re.getCapabilities("generic", "my-vision-model")
	if !caps.Vision {
		t.Fatal("expected Vision=true from heuristic fallback")
	}

	// Model name contains "reason" → heuristic should set Reasoning=true.
	caps = re.getCapabilities("generic", "deep-reason-model")
	if !caps.Reasoning {
		t.Fatal("expected Reasoning=true from heuristic fallback")
	}
}

// --- 12. Existing vision hard rejection behavior remains unchanged -----------

func TestVisionHardRejectionUnchanged(t *testing.T) {
	hint := CapabilityHint{Vision: true}
	noVision := Capabilities{Streaming: true} // no Vision
	score := matchScore(hint, noVision)
	if score != 0.0 {
		t.Fatalf("expected hard rejection score=0.0 for missing vision, got %f", score)
	}
}

// --- 13. Existing reasoning soft scoring behavior remains unchanged ----------

func TestReasoningSoftScoringUnchanged(t *testing.T) {
	hint := CapabilityHint{Reasoning: true}
	noReason := Capabilities{Streaming: true} // no Reasoning
	score := matchScore(hint, noReason)
	if score != 0.3 {
		t.Fatalf("expected soft rejection score=0.3 for missing reasoning, got %f", score)
	}
}

// --- 14. Existing ToolCalling soft scoring behavior remains unchanged --------

func TestToolCallingSoftScoringUnchanged(t *testing.T) {
	hint := CapabilityHint{ToolCalling: true}
	noTools := Capabilities{Streaming: true} // no ToolCalling
	score := matchScore(hint, noTools)
	if score != 0.3 {
		t.Fatalf("expected soft rejection score=0.3 for missing tool calling, got %f", score)
	}
}

// --- 15. Existing provider behavior remains unchanged ------------------------

func TestProviderBehaviorUnchanged(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProviderWithModelCaps{name: "openai"})
	reg.Register(&stubProviderWithModelCaps{name: "anthropic"})
	reg.Register(&stubProviderWithModelCaps{name: "groq"})

	re := newTestRouter(reg)

	// Each provider should still get its default capabilities.
	openaiCaps := re.getCapabilities("openai", "gpt-4o")
	if !openaiCaps.Streaming || !openaiCaps.ToolCalling || !openaiCaps.Reasoning {
		t.Fatal("openai provider defaults changed")
	}

	anthropicCaps := re.getCapabilities("anthropic", "claude-3")
	if !anthropicCaps.Streaming || !anthropicCaps.ToolCalling || !anthropicCaps.Reasoning {
		t.Fatal("anthropic provider defaults changed")
	}

	groqCaps := re.getCapabilities("groq", "llama3-8b")
	if !groqCaps.Streaming || !groqCaps.ToolCalling {
		t.Fatal("groq provider defaults changed")
	}
}

// --- Additional: mergeModelOverrides semantics -------------------------------

func TestMergeModelOverridesApplyFullProfile(t *testing.T) {
	// When model caps are provided, they represent the complete profile.
	// All fields are applied unconditionally.
	providerCaps := Capabilities{Streaming: true, Vision: true, Reasoning: true}
	modelCaps := Capabilities{Vision: true} // only Vision set; others are zero

	merged := mergeModelOverrides(providerCaps, modelCaps)
	if !merged.Vision {
		t.Fatal("Vision should remain true")
	}
	// With full-profile semantics, unset fields in model override provider defaults.
	if merged.Reasoning {
		t.Fatal("Reasoning should be overridden to false by explicit model profile")
	}
	if merged.Streaming {
		t.Fatal("Streaming should be overridden to false by explicit model profile")
	}
}

func TestMergeModelOverridesExplicitTrue(t *testing.T) {
	providerCaps := Capabilities{Streaming: true, Vision: false}
	modelCaps := Capabilities{Vision: true} // model enables vision

	merged := mergeModelOverrides(providerCaps, modelCaps)
	if !merged.Vision {
		t.Fatal("Vision should be overridden to true by model metadata")
	}
}

// --- Additional: metadataToCapabilities preserves all fields ----------------

func TestMetadataToCapabilitiesPreservesAllFields(t *testing.T) {
	meta := provider.Metadata{
		Name:             "test",
		MaxContextLength: 200000,
		Capabilities: provider.Capabilities{
			Streaming:   true,
			Vision:      true,
			Reasoning:   true,
			ToolCalling: true,
			Structured:  true,
			LongContext: true,
			Embeddings:  true,
			Images:      true,
			Audio:       true,
			Functions:   true,
		},
	}
	caps := metadataToCapabilities(meta)
	if !caps.Streaming || !caps.Vision || !caps.Reasoning || !caps.ToolCalling ||
		!caps.Structured || !caps.LongContext || !caps.Embeddings ||
		!caps.Images || !caps.Audio || !caps.Functions {
		t.Fatalf("expected all capabilities true, got %+v", caps)
	}
	if caps.MaxContext != 200000 {
		t.Fatalf("expected MaxContext=200000, got %d", caps.MaxContext)
	}
}

// --- Additional: catalog integration with model capabilities -----------------

func TestCatalogPreservesModelCapabilities(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProviderWithModelCaps{
		name:         "openai",
		modelID:      "gpt-4o",
		capabilities: &provider.Capabilities{Streaming: true, Vision: true},
		maxContext:   128000,
	})

	c := catalog.New(reg, nil)
	entries, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Capabilities == nil {
		t.Fatal("expected model capabilities in catalog entry")
	}
	if !e.Capabilities.Vision {
		t.Fatal("expected Vision=true in catalog entry")
	}
	if e.MaxContextLength != 128000 {
		t.Fatalf("expected MaxContextLength=128000, got %d", e.MaxContextLength)
	}
}

// --- Additional: RouterEngine.LoadCatalogCapabilities -----------------------

func TestLoadCatalogCapabilities(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProviderWithModelCaps{name: "openai"})

	re := newTestRouter(reg)
	entries := []catalog.Entry{
		{
			ModelID:          "openai/gpt-4o",
			Provider:         "openai",
			Capabilities:     &provider.Capabilities{Streaming: true, Vision: true},
			MaxContextLength: 128000,
		},
		{
			ModelID:          "openai/text-only",
			Provider:         "openai",
			Capabilities:     &provider.Capabilities{Streaming: true, Vision: false},
			MaxContextLength: 8192,
		},
	}
	re.LoadCatalogCapabilities(entries)

	// gpt-4o should have model-level overrides.
	caps := re.getCapabilities("openai", "openai/gpt-4o")
	if !caps.Vision {
		t.Fatal("expected Vision=true from catalog override")
	}
	if caps.MaxContext != 128000 {
		t.Fatalf("expected MaxContext=128000, got %d", caps.MaxContext)
	}

	// text-only should have Vision explicitly false.
	caps = re.getCapabilities("openai", "openai/text-only")
	if caps.Vision {
		t.Fatal("expected Vision=false from explicit catalog override")
	}
	if caps.MaxContext != 8192 {
		t.Fatalf("expected MaxContext=8192, got %d", caps.MaxContext)
	}
}

// --- Additional: heuristic-only path (no provider metadata) ------------------

func TestHeuristicOnlyPath(t *testing.T) {
	reg := provider.NewRegistry()
	// Register a provider with no custom metadata — uses DefaultMetadata.
	reg.Register(&stubProviderWithModelCaps{name: "unknown_provider"})

	re := newTestRouter(reg)
	// Model name triggers heuristic.
	caps := re.getCapabilities("unknown_provider", "my-vision-model")
	if !caps.Vision {
		t.Fatal("expected heuristic to set Vision=true")
	}
	caps = re.getCapabilities("unknown_provider", "o1-preview")
	if !caps.Reasoning {
		t.Fatal("expected heuristic to set Reasoning=true for o1 model")
	}
}

// --- Additional: SetModelCapabilities invalidates provider cache -------------

func TestSetModelCapabilitiesInvalidatesProviderCache(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProviderWithModelCaps{name: "openai"})

	re := newTestRouter(reg)
	// First call populates provider cache.
	_ = re.getCapabilities("openai", "gpt-4o")
	// Now set model-specific override.
	re.SetModelCapabilities("openai", "gpt-4o", Capabilities{Vision: true, MaxContext: 99999})
	// Second call should return model-specific result.
	c2 := re.getCapabilities("openai", "gpt-4o")
	if !c2.Vision {
		t.Fatal("expected Vision=true from model override")
	}
	if c2.MaxContext != 99999 {
		t.Fatalf("expected MaxContext=99999, got %d", c2.MaxContext)
	}
	// Other models should still use provider defaults.
	c3 := re.getCapabilities("openai", "other-model")
	if c3.MaxContext == 99999 {
		t.Fatal("other model should not be affected by gpt-4o override")
	}
}

// --- stub provider with optional model-level capabilities --------------------

type stubProviderWithModelCaps struct {
	name         string
	modelID      string
	capabilities *provider.Capabilities
	maxContext   int
}

func (s *stubProviderWithModelCaps) Name() string { return s.name }

func (s *stubProviderWithModelCaps) ChatCompletion(
	context.Context, *apitypes.ChatCompletionRequest,
) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProviderWithModelCaps) ChatCompletionStream(
	context.Context, *apitypes.ChatCompletionRequest,
) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProviderWithModelCaps) Embeddings(
	context.Context, *apitypes.EmbeddingRequest,
) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProviderWithModelCaps) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	if s.modelID == "" {
		return nil, nil
	}
	mi := provider.ModelInfo{
		ProviderModelID: s.modelID,
		ModelID:         s.modelID,
	}
	if s.capabilities != nil {
		mi.Capabilities = s.capabilities
		mi.MaxContextLength = s.maxContext
	}
	return []provider.ModelInfo{mi}, nil
}

func (s *stubProviderWithModelCaps) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProviderWithModelCaps) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}

func (s *stubProviderWithModelCaps) SupportsModel(modelID string) bool {
	return s.modelID == "" || s.modelID == modelID
}

func (s *stubProviderWithModelCaps) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}
