package nvidianim

import (
	"context"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for NVIDIA NIM.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new NVIDIA NIM provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("nvidia_nim", apiKey, baseURL, timeout, openaibase.WithPricing(nvidiaNimPricing)),
	}
}

// ChatCompletion applies NIM request shaping then forwards to the OpenAI-compatible base.
func (p *Provider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return p.Base.ChatCompletion(ctx, prepareChatRequest(req))
}

// ChatCompletionStream applies NIM request shaping then forwards to the OpenAI-compatible base.
func (p *Provider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return p.Base.ChatCompletionStream(ctx, prepareChatRequest(req))
}

// prepareChatRequest returns a shallow copy shaped for NVIDIA NIM:
// - remaps OpenAI "developer" role to "system" (NIM 500s with chat_template_kwargs)
// - injects chat_template_kwargs for DeepSeek V4 so reasoning streams instead of
//   hanging / returning empty content when clients omit the field (e.g. OpenCode)
func prepareChatRequest(req *apitypes.ChatCompletionRequest) *apitypes.ChatCompletionRequest {
	if req == nil {
		return nil
	}
	out := *req
	if len(req.Messages) > 0 {
		msgs := make([]apitypes.Message, len(req.Messages))
		copy(msgs, req.Messages)
		for i := range msgs {
			if strings.EqualFold(msgs[i].Role, "developer") {
				msgs[i].Role = "system"
			}
		}
		out.Messages = msgs
	}
	applyDeepSeekV4ChatTemplate(&out)
	return &out
}

func isDeepSeekV4(model string) bool {
	return strings.Contains(strings.ToLower(model), "deepseek-v4")
}

// mapNIMReasoningEffort maps OpenAI-style effort values onto NIM's none|high|max.
func mapNIMReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "high", "medium", "low", "minimal":
		return "high"
	case "none", "disable", "disabled", "off":
		return "none"
	case "max", "xhigh":
		return "max"
	default:
		return "high"
	}
}

func resolveDeepSeekV4Effort(req *apitypes.ChatCompletionRequest) string {
	if req.ReasoningEffort != "" {
		return mapNIMReasoningEffort(req.ReasoningEffort)
	}
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		return mapNIMReasoningEffort(req.Reasoning.Effort)
	}
	if req.ChatTemplateKwargs != nil {
		if v, ok := req.ChatTemplateKwargs["reasoning_effort"].(string); ok && v != "" {
			return mapNIMReasoningEffort(v)
		}
		if thinking, ok := req.ChatTemplateKwargs["thinking"].(bool); ok && !thinking {
			return "none"
		}
	}
	// NIM DeepSeek V4 defaults to high; clients like OpenCode often omit the field.
	return "high"
}

func applyDeepSeekV4ChatTemplate(req *apitypes.ChatCompletionRequest) {
	if req == nil || !isDeepSeekV4(req.Model) {
		return
	}

	effort := resolveDeepSeekV4Effort(req)
	kwargs := map[string]any{}
	if req.ChatTemplateKwargs != nil {
		for k, v := range req.ChatTemplateKwargs {
			kwargs[k] = v
		}
	}
	// Client-provided keys win; fill only what NIM needs when omitted.
	if _, ok := kwargs["thinking"]; !ok {
		kwargs["thinking"] = effort != "none"
	}
	if _, ok := kwargs["reasoning_effort"]; !ok && effort != "none" {
		kwargs["reasoning_effort"] = effort
	}
	req.ChatTemplateKwargs = kwargs

	// Top-level reasoning_effort is what NVIDIA's API snippets document; keep it
	// aligned so either translation path works.
	if req.ReasoningEffort == "" {
		req.ReasoningEffort = effort
	}
}

func nvidiaNimPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	// NVIDIA NIM pricing varies by deployment/hosting. Provide a placeholder
	// map for commonly hosted models; operators should override via cost.rates.
	return map[string]provider.PricingInfo{
		"meta/llama-3.1-70b-instruct": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0007,
			OutputPrice: 0.0009,
			Currency:    "USD",
		},
		"meta/llama-3.1-8b-instruct": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0002,
			OutputPrice: 0.0002,
			Currency:    "USD",
		},
	}, nil
}
