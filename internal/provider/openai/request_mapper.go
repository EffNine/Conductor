package openai

import "github.com/EffNine/conductor/internal/apitypes"

// MapRequest converts a canonical ChatCompletionRequest to an OpenAI-specific request.
func MapRequest(req *apitypes.ChatCompletionRequest) *openaiChatRequest {
	if req == nil {
		return nil
	}

	out := &openaiChatRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		N:           req.N,
		Stream:      req.Stream,
		Stop:        req.Stop,
		MaxTokens:   req.MaxTokens,
		User:        req.User,
		LogitBias:   req.LogitBias,
		Seed:        req.Seed,
	}

	if req.PresencePenalty != nil {
		out.PresencePenalty = req.PresencePenalty
	}
	if req.FrequencyPenalty != nil {
		out.FrequencyPenalty = req.FrequencyPenalty
	}

	if len(req.ResponseFormat) > 0 {
		out.ResponseFormat = req.ResponseFormat
	}

	if req.StreamOptions != nil {
		out.StreamOptions = &openaiStreamOptions{
			IncludeUsage: req.StreamOptions.IncludeUsage,
		}
	}

	if req.Reasoning != nil {
		reasoning := map[string]interface{}{}
		if req.Reasoning.Effort != "" {
			reasoning["effort"] = req.Reasoning.Effort
		}
		if req.Reasoning.MaxTokens != nil {
			reasoning["max_tokens"] = *req.Reasoning.MaxTokens
		}
		if req.Reasoning.Exclude != nil {
			reasoning["exclude"] = *req.Reasoning.Exclude
		}
		if req.Reasoning.Enabled != nil {
			reasoning["enabled"] = *req.Reasoning.Enabled
		}
		if req.Reasoning.Summary != "" {
			reasoning["summary"] = req.Reasoning.Summary
		}
		out.Reasoning = reasoning
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

	out.Messages = make([]openaiMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, mapMessage(m))
	}

	if len(req.Tools) > 0 {
		out.Tools = make([]openaiTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			out.Tools = append(out.Tools, mapTool(t))
		}
	}

	if req.ToolChoice != nil {
		out.ToolChoice = mapToolChoice(req.ToolChoice)
	}

	return out
}

func mapMessage(m apitypes.Message) openaiMessage {
	msg := openaiMessage{
		Role:             m.Role,
		Name:             m.Name,
		Reasoning:        m.Reasoning,
		ReasoningContent: m.ReasoningContent,
	}

	if m.Role == "developer" {
		msg.Role = "system"
	}

	switch v := m.Content.(type) {
	case string:
		msg.Content = v
	case []apitypes.ContentPart:
		parts := make([]openaiContentPart, 0, len(v))
		for _, p := range v {
			parts = append(parts, openaiContentPart{
				Type:     string(p.Type),
				Text:     p.Text,
				ImageURL: p.ImageURL,
			})
		}
		msg.Content = parts
	case nil:
		msg.Content = nil
	default:
		msg.Content = v
	}

	if len(m.ToolCalls) > 0 {
		msg.ToolCalls = make([]openaiToolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, openaiToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: openaiFunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	msg.ToolCallID = m.ToolCallID
	return msg
}

func mapTool(t apitypes.Tool) openaiTool {
	return openaiTool{
		Type: t.Type,
		Function: openaiFunctionDef{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		},
	}
}

func mapToolChoice(tc interface{}) interface{} {
	switch v := tc.(type) {
	case string:
		if v == "none" || v == "auto" {
			return v
		}
		return map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name": v,
			},
		}
	case map[string]interface{}:
		return v
	default:
		return tc
	}
}

// OpenAI-specific internal types (must not escape adapter boundary).

type openaiChatRequest struct {
	Model              string                 `json:"model"`
	Messages           []openaiMessage        `json:"messages"`
	Temperature        *float64               `json:"temperature,omitempty"`
	TopP               *float64               `json:"top_p,omitempty"`
	N                  *int                   `json:"n,omitempty"`
	Stream             bool                   `json:"stream,omitempty"`
	Stop               interface{}            `json:"stop,omitempty"`
	MaxTokens          *int                   `json:"max_tokens,omitempty"`
	PresencePenalty    *float64               `json:"presence_penalty,omitempty"`
	FrequencyPenalty   *float64               `json:"frequency_penalty,omitempty"`
	User               string                 `json:"user,omitempty"`
	LogitBias          map[string]int         `json:"logit_bias,omitempty"`
	ResponseFormat     map[string]interface{} `json:"response_format,omitempty"`
	Seed               *int                   `json:"seed,omitempty"`
	Tools              []openaiTool           `json:"tools,omitempty"`
	ToolChoice         interface{}            `json:"tool_choice,omitempty"`
	StreamOptions      *openaiStreamOptions   `json:"stream_options,omitempty"`
	Reasoning          map[string]interface{} `json:"reasoning,omitempty"`
	ReasoningEffort    string                 `json:"reasoning_effort,omitempty"`
	IncludeReasoning   *bool                  `json:"include_reasoning,omitempty"`
	ThinkingBudget     *int                   `json:"thinking_budget,omitempty"`
	ChatTemplateKwargs map[string]any         `json:"chat_template_kwargs,omitempty"`
}

type openaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type openaiMessage struct {
	Role             string              `json:"role"`
	Content          interface{}         `json:"content,omitempty"`
	Name             string              `json:"name,omitempty"`
	ToolCalls        []openaiToolCall    `json:"tool_calls,omitempty"`
	ToolCallID       string              `json:"tool_call_id,omitempty"`
	Reasoning        string              `json:"reasoning,omitempty"`
	ReasoningContent string              `json:"reasoning_content,omitempty"`
}

type openaiContentPart struct {
	Type     string                  `json:"type"`
	Text     string                  `json:"text,omitempty"`
	ImageURL *apitypes.ImageURLContent `json:"image_url,omitempty"`
}

type openaiTool struct {
	Type     string            `json:"type"`
	Function openaiFunctionDef `json:"function"`
}

type openaiFunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiFunctionCall `json:"function"`
}

type openaiFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiChatResponse struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	Choices           []openaiChoice `json:"choices"`
	Usage             *openaiUsage   `json:"usage,omitempty"`
	SystemFingerprint string         `json:"system_fingerprint,omitempty"`
}

type openaiChoice struct {
	Index        int           `json:"index"`
	Message      openaiMessage `json:"message"`
	Delta        openaiMessage `json:"delta"`
	FinishReason *string       `json:"finish_reason,omitempty"`
	LogProbs     *openaiLogProbs `json:"logprobs,omitempty"`
}

type openaiLogProbs struct {
	TextOffset    []int                `json:"text_offset,omitempty"`
	TokenLogProbs []float64            `json:"token_logprobs,omitempty"`
	Tokens        []string             `json:"tokens,omitempty"`
	TopLogProbs   []map[string]float64 `json:"top_logprobs,omitempty"`
}

type openaiUsage struct {
	PromptTokens              int                `json:"prompt_tokens"`
	CompletionTokens          int                `json:"completion_tokens"`
	TotalTokens               int                `json:"total_tokens"`
	PromptTokensDetails       *openaiTokenDetails `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails   *openaiTokenDetails `json:"completion_tokens_details,omitempty"`
}

type openaiTokenDetails struct {
	CachedTokens               int `json:"cached_tokens,omitempty"`
	AudioTokens                int `json:"audio_tokens,omitempty"`
	ReasoningTokens            int `json:"reasoning_tokens,omitempty"`
	AcceptedPredictionTokens   int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens   int `json:"rejected_prediction_tokens,omitempty"`
}

type openaiStreamChunk struct {
	ID      string       `json:"id,omitempty"`
	Object  string       `json:"object,omitempty"`
	Created int64        `json:"created,omitempty"`
	Model   string       `json:"model,omitempty"`
	Choices []openaiChoice `json:"choices"`
	Usage   *openaiUsage `json:"usage,omitempty"`
}

func ensureStreamUsage(req *openaiChatRequest) {
	if req.StreamOptions == nil {
		req.StreamOptions = &openaiStreamOptions{}
	}
	req.StreamOptions.IncludeUsage = true
}
