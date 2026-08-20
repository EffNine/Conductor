package router

import (
	"strings"

	"github.com/EffNine/conductor/internal/apitypes"
)

// Capabilities describes what a provider/model supports.
type Capabilities struct {
	Vision      bool
	Streaming   bool
	Reasoning   bool
	ToolCalling bool
	Structured  bool
	LongContext bool
	Embeddings  bool
	Images      bool
	Audio       bool
	Functions   bool
	MaxContext  int // 0 means unknown
}

// MergeCapabilities combines request-level capability hints with provider
// capabilities. A request capability is only considered when the provider
// also advertises it (AND semantics).
func MergeCapabilities(provider Capabilities, req *apitypes.ChatCompletionRequest) Capabilities {
	c := provider
	if req == nil {
		return c
	}
	// Streaming is inferred from the request.
	c.Streaming = req.Stream
	// Tool calling is inferred from the request having tools.
	c.ToolCalling = len(req.Tools) > 0 || req.ToolChoice != nil
	// Reasoning is inferred from request reasoning controls.
	c.Reasoning = req.SupportsReasoningParams()
	// Structured output is inferred from response_format.
	c.Structured = len(req.ResponseFormat) > 0
	// Vision is inferred from multimodal content.
	if c.Vision {
		for _, m := range req.Messages {
			if m.HasContentParts() {
				for _, p := range m.Content.([]apitypes.ContentPart) {
					if p.Type == apitypes.ContentPartImageURL && p.ImageURL != nil && p.ImageURL.URL != "" {
						c.Vision = true
						break
					}
				}
			}
		}
	}
	return c
}

// CapabilityHint describes what the request needs.
type CapabilityHint struct {
	Vision      bool
	Reasoning   bool
	ToolCalling bool
	Structured  bool
	Streaming   bool
}

// ExtractCapabilityHint derives the capability requirements from a request.
func ExtractCapabilityHint(req *apitypes.ChatCompletionRequest) CapabilityHint {
	h := CapabilityHint{}
	if req == nil {
		return h
	}
	h.Streaming = req.Stream
	h.ToolCalling = len(req.Tools) > 0 || req.ToolChoice != nil
	h.Reasoning = req.SupportsReasoningParams()
	h.Structured = len(req.ResponseFormat) > 0
	for _, m := range req.Messages {
		if m.HasContentParts() {
			for _, p := range m.Content.([]apitypes.ContentPart) {
				if p.Type == apitypes.ContentPartImageURL && p.ImageURL != nil && p.ImageURL.URL != "" {
					h.Vision = true
					break
				}
			}
		}
		if h.Vision {
			break
		}
	}
	return h
}

// matchScore computes how well provider capabilities satisfy the request hint.
// Returns a score in [0, 1]. Missing a required capability penalizes heavily.
func matchScore(hint CapabilityHint, caps Capabilities) float64 {
	if hint.Vision && !caps.Vision {
		return 0.0
	}
	if hint.Reasoning && !caps.Reasoning {
		return 0.3
	}
	if hint.ToolCalling && !caps.ToolCalling {
		return 0.3
	}
	if hint.Structured && !caps.Structured {
		return 0.5
	}
	// Bonus for exceeding expectations (e.g. provider supports vision even when not asked).
	score := 1.0
	if !hint.Vision && caps.Vision {
		score += 0.05
	}
	if !hint.Reasoning && caps.Reasoning {
		score += 0.03
	}
	if !hint.ToolCalling && caps.ToolCalling {
		score += 0.03
	}
	if !hint.Structured && caps.Structured {
		score += 0.02
	}
	if score > 1.0 {
		score = 1.0
	}
	return score
}

// DefaultCapabilities returns capabilities for a provider name based on heuristics.
// In production this would come from provider metadata; here we use common knowledge.
func DefaultCapabilities(providerName, modelID string) Capabilities {
	c := Capabilities{}
	switch providerName {
	case "openai":
		c.Streaming = true
		c.ToolCalling = true
		c.Structured = true
		c.Reasoning = true
		c.Vision = true
		c.MaxContext = 128000
	case "anthropic":
		c.Streaming = true
		c.ToolCalling = true
		c.Reasoning = true
		c.MaxContext = 200000
	case "gemini":
		c.Streaming = true
		c.ToolCalling = true
		c.Vision = true
		c.MaxContext = 1000000
	case "deepseek":
		c.Streaming = true
		c.Reasoning = true
		c.MaxContext = 64000
	case "openrouter":
		c.Streaming = true
		c.ToolCalling = true
		c.Reasoning = true
		c.Vision = true
		c.MaxContext = 128000
	case "groq":
		c.Streaming = true
		c.ToolCalling = true
		c.MaxContext = 128000
	case "ollama":
		c.Streaming = true
		c.MaxContext = 32768
	case "lmstudio":
		c.Streaming = true
		c.MaxContext = 32768
	case "opencode":
		c.Streaming = true
		c.ToolCalling = true
		c.Reasoning = true
		c.MaxContext = 128000
	case "nvidia_nim":
		c.Streaming = true
		c.ToolCalling = true
		c.Reasoning = true
		c.Vision = true
		c.MaxContext = 128000
	case "nous_portal":
		c.Streaming = true
		c.ToolCalling = true
		c.MaxContext = 32768
	case "xai":
		c.Streaming = true
		c.ToolCalling = true
		c.Reasoning = true
		c.MaxContext = 100000
	case "agnesai":
		c.Streaming = true
		c.ToolCalling = true
		c.MaxContext = 128000
	}

	// Model-specific overrides.
	lower := strings.ToLower(modelID)
	if strings.Contains(lower, "vision") || strings.Contains(lower, "vl") {
		c.Vision = true
	}
	if strings.Contains(lower, "reason") || strings.Contains(lower, "o1") || strings.Contains(lower, "o3") || strings.Contains(lower, "r1") {
		c.Reasoning = true
	}

	return c
}
