package openai

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for OpenAI.
type Provider struct {
	*openaibase.Base
	apiKey  string
	baseURL string
	name    string
}

// NewProvider creates a new OpenAI provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	p := &Provider{
		apiKey:  apiKey,
		baseURL: baseURL,
		name:    "openai",
	}
	p.Base = openaibase.New("openai", apiKey, baseURL, timeout,
		openaibase.WithPricing(openaiPricing),
		openaibase.WithCapabilities(provider.Capabilities{
			Streaming:    true,
			Vision:       true,
			Reasoning:    true,
			ToolCalling:  true,
			Structured:   true,
			LongContext:  true,
			Embeddings:   true,
		}),
	)
	return p
}

// Name returns the provider name.
func (p *Provider) Name() string { return p.name }

// GetMetadata returns metadata for this provider.
func (p *Provider) GetMetadata() provider.Metadata {
	meta := provider.NewMetadata(p.name, provider.Capabilities{
		Streaming:    true,
		Vision:       true,
		Reasoning:    true,
		ToolCalling:  true,
		Structured:   true,
		LongContext:  true,
		Embeddings:   true,
	})
	meta.BaseURL = p.baseURL
	meta.DisplayName = "OpenAI"
	meta.Description = "OpenAI chat completion and embeddings API"
	return meta
}

// ChatCompletion maps the canonical request to OpenAI format, forwards to the base,
// then normalizes the canonical response.
func (p *Provider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	mapped := MapRequest(req)
	resp, err := p.Base.ChatCompletion(ctx, mappedToCanonical(mapped))
	if err != nil {
		return nil, err
	}
	apitypes.NormalizeChoices(resp.Choices)
	return resp, nil
}

// ChatCompletionStream maps the canonical request to OpenAI format, forwards to the base,
// then normalizes each stream chunk.
func (p *Provider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	mapped := MapRequest(req)
	ch, err := p.Base.ChatCompletionStream(ctx, mappedToCanonical(mapped))
	if err != nil {
		return nil, err
	}
	return mapStreamChannel(ch), nil
}

// mapStreamChannel wraps the upstream channel to normalize chunks.
func mapStreamChannel(ch <-chan apitypes.StreamChunk) <-chan apitypes.StreamChunk {
	out := make(chan apitypes.StreamChunk)
	go func() {
		defer close(out)
		for chunk := range ch {
			if chunk.Done || chunk.Error != nil {
				out <- chunk
				continue
			}
			normalized := MapStreamChunk(&chunk)
			out <- normalized
		}
	}()
	return out
}

// mappedToCanonical converts an openaiChatRequest back to apitypes.ChatCompletionRequest
// for forwarding through the base transport.
func mappedToCanonical(req *openaiChatRequest) *apitypes.ChatCompletionRequest {
	if req == nil {
		return nil
	}
	out := &apitypes.ChatCompletionRequest{
		Model:            req.Model,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		N:                req.N,
		Stream:           req.Stream,
		Stop:             req.Stop,
		MaxTokens:        req.MaxTokens,
		PresencePenalty:  req.PresencePenalty,
		FrequencyPenalty: req.FrequencyPenalty,
		User:             req.User,
		LogitBias:        req.LogitBias,
		ResponseFormat:   req.ResponseFormat,
		Seed:             req.Seed,
	}
	if req.StreamOptions != nil {
		out.StreamOptions = &apitypes.StreamOptions{
			IncludeUsage: req.StreamOptions.IncludeUsage,
		}
	}
	if req.Reasoning != nil {
		out.Reasoning = &apitypes.ReasoningConfig{}
		if req.Reasoning["effort"] != nil {
			out.Reasoning.Effort = req.Reasoning["effort"].(string)
		}
		if req.Reasoning["max_tokens"] != nil {
			if v, ok := req.Reasoning["max_tokens"].(int); ok {
				out.Reasoning.MaxTokens = &v
			}
		}
		if req.Reasoning["exclude"] != nil {
			if v, ok := req.Reasoning["exclude"].(bool); ok {
				out.Reasoning.Exclude = &v
			}
		}
		if req.Reasoning["enabled"] != nil {
			if v, ok := req.Reasoning["enabled"].(bool); ok {
				out.Reasoning.Enabled = &v
			}
		}
		if req.Reasoning["summary"] != nil {
			out.Reasoning.Summary = req.Reasoning["summary"].(string)
		}
	}
	if req.ReasoningEffort != "" {
		out.ReasoningEffort = req.ReasoningEffort
	}
	if req.IncludeReasoning != nil {
		out.IncludeReasoning = req.IncludeReasoning
	}
	if req.ThinkingBudget != nil {
		out.ThinkingBudget = req.ThinkingBudget
	}
	if len(req.ChatTemplateKwargs) > 0 {
		out.ChatTemplateKwargs = req.ChatTemplateKwargs
	}
	out.Messages = make([]apitypes.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, openaiToCanonicalMessage(m))
	}
	out.Tools = make([]apitypes.Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, openaiToCanonicalTool(t))
	}
	if req.ToolChoice != nil {
		out.ToolChoice = req.ToolChoice
	}
	return out
}

func openaiToCanonicalMessage(m openaiMessage) apitypes.Message {
	msg := apitypes.Message{
		Role:             m.Role,
		Name:             m.Name,
		ToolCallID:       m.ToolCallID,
		Reasoning:        m.Reasoning,
		ReasoningContent: m.ReasoningContent,
	}

	switch v := m.Content.(type) {
	case string:
		msg.Content = v
	case []openaiContentPart:
		parts := make([]apitypes.ContentPart, 0, len(v))
		for _, p := range v {
			parts = append(parts, apitypes.ContentPart{
				Type:     apitypes.ContentPartType(p.Type),
				Text:     p.Text,
				ImageURL: p.ImageURL,
			})
		}
		msg.Content = parts
	}

	if len(m.ToolCalls) > 0 {
		msg.ToolCalls = make([]apitypes.ToolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, apitypes.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: apitypes.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	return msg
}

func openaiToCanonicalTool(t openaiTool) apitypes.Tool {
	return apitypes.Tool{
		Type: t.Type,
		Function: apitypes.FunctionDef{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		},
	}
}

func openaiPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"gpt-4o": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0025,
			OutputPrice: 0.010,
			Currency:    "USD",
		},
		"gpt-4o-2024-08-06": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0025,
			OutputPrice: 0.010,
			Currency:    "USD",
		},
		"gpt-4o-mini": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00015,
			OutputPrice: 0.0006,
			Currency:    "USD",
		},
		"gpt-4-turbo": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.010,
			OutputPrice: 0.030,
			Currency:    "USD",
		},
		"gpt-3.5-turbo": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0005,
			OutputPrice: 0.0015,
			Currency:    "USD",
		},
		"text-embedding-3-small": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00002,
			OutputPrice: 0.00002,
			Currency:    "USD",
		},
		"text-embedding-3-large": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00013,
			OutputPrice: 0.00013,
			Currency:    "USD",
		},
	}, nil
}
