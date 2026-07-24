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
//   - remaps OpenAI "developer" role to "system" (NIM 500s with chat_template_kwargs)
//   - injects per-model chat_template_kwargs for reasoning families so streams do not
//     hang / return empty content when clients omit the field (e.g. OpenCode)
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
	applyThinkingChatTemplate(&out)
	return &out
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
