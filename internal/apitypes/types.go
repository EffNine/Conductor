package apitypes

import (
	"encoding/json"
	"strings"
)

// ChatCompletionRequest represents an OpenAI-compatible chat completion request
type ChatCompletionRequest struct {
	Model            string                 `json:"model"`
	Messages         []Message              `json:"messages"`
	Temperature      *float64               `json:"temperature,omitempty"`
	TopP             *float64               `json:"top_p,omitempty"`
	N                *int                   `json:"n,omitempty"`
	Stream           bool                   `json:"stream,omitempty"`
	Stop             interface{}            `json:"stop,omitempty"`
	MaxTokens        *int                   `json:"max_tokens,omitempty"`
	PresencePenalty  *float64               `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64               `json:"frequency_penalty,omitempty"`
	User             string                 `json:"user,omitempty"`
	LogitBias        map[string]int         `json:"logit_bias,omitempty"`
	ResponseFormat   map[string]interface{} `json:"response_format,omitempty"`
	Seed             *int                   `json:"seed,omitempty"`
	Tools            []Tool                 `json:"tools,omitempty"`
	ToolChoice       interface{}            `json:"tool_choice,omitempty"`
	StreamOptions    *StreamOptions         `json:"stream_options,omitempty"`

	// Reasoning controls (forwarded when the upstream model/provider supports them).
	// Prefer Reasoning (OpenRouter-style); ReasoningEffort is the OpenAI shorthand.
	Reasoning        *ReasoningConfig `json:"reasoning,omitempty"`
	ReasoningEffort  string           `json:"reasoning_effort,omitempty"` // max|xhigh|high|medium|low|minimal|none
	IncludeReasoning *bool            `json:"include_reasoning,omitempty"`

	// ThinkingBudget is a Seed-OSS / NVIDIA NIM chat-template token budget for
	// internal reasoning. Multiples of 512 are recommended; 0 skips thinking.
	ThinkingBudget *int `json:"thinking_budget,omitempty"`

	// ChatTemplateKwargs are provider-specific chat-template options (NVIDIA NIM
	// DeepSeek V4, vLLM, etc.). Forwarded as a top-level JSON object when set.
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`

	// Mode is an explicit routing mode override. When set, it takes precedence
	// over classifier-derived intent. Supported values: auto, coding, planning,
	// agentic, reasoning, long_horizon, fast, vision.
	Mode string `json:"mode,omitempty"`
}

// StreamOptions configures streaming behavior (OpenAI-compatible).
type StreamOptions struct {
	// IncludeUsage asks the upstream to emit a final chunk with token usage.
	// Without this, many providers omit usage and clients show completion_tokens: 0.
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ReasoningConfig controls reasoning/thinking for models that support it
// (OpenRouter, OpenCode Zen, OpenAI o-series / GPT-5, etc.).
type ReasoningConfig struct {
	// Effort is OpenAI-style effort: max, xhigh, high, medium, low, minimal, none.
	Effort string `json:"effort,omitempty"`
	// MaxTokens is an Anthropic-style reasoning token budget.
	MaxTokens *int `json:"max_tokens,omitempty"`
	// Exclude omits reasoning tokens from the response when true.
	Exclude *bool `json:"exclude,omitempty"`
	// Enabled turns reasoning on with provider defaults when effort/max_tokens unset.
	Enabled *bool `json:"enabled,omitempty"`
	// Summary controls reasoning summary verbosity: auto, concise, detailed.
	Summary string `json:"summary,omitempty"`
}

// SupportsReasoningParams reports whether this request asks for reasoning controls.
func (r *ChatCompletionRequest) SupportsReasoningParams() bool {
	if r == nil {
		return false
	}
	if r.ReasoningEffort != "" || r.IncludeReasoning != nil {
		return true
	}
	return r.Reasoning != nil
}

// EnsureStreamUsage enables stream_options.include_usage so upstreams emit a
// final usage chunk. Without it, OpenAI-compatible providers often omit usage
// and clients report completion_tokens: 0 despite non-empty content.
func (r *ChatCompletionRequest) EnsureStreamUsage() {
	if r == nil {
		return
	}
	if r.StreamOptions == nil {
		r.StreamOptions = &StreamOptions{}
	}
	r.StreamOptions.IncludeUsage = true
}

// ContentPartType represents the type of a content part (multimodal).
type ContentPartType string

const (
	// ContentPartText represents a text content part.
	ContentPartText ContentPartType = "text"
	// ContentPartImageURL represents an image URL content part.
	ContentPartImageURL ContentPartType = "image_url"
)

// ContentPart represents a single part of a multimodal message.
type ContentPart struct {
	Type     ContentPartType  `json:"type"`
	Text     string           `json:"text,omitempty"`
	ImageURL *ImageURLContent `json:"image_url,omitempty"`
}

// ImageURLContent represents an image URL in a multimodal content part.
type ImageURLContent struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// Message represents a chat message
type Message struct {
	// Role and Content use omitempty so stream deltas do not emit empty
	// strings. OpenCode's custom OpenAI client rejects delta.role:"" (Zod) and
	// trailing {} chunks that re-marshal as empty model/content wipe the reply.
	Role             string      `json:"role,omitempty"`
	Content          interface{} `json:"content,omitempty"` // string or []ContentPart for multimodal
	Name             string      `json:"name,omitempty"`
	ToolCalls        []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID       string      `json:"tool_call_id,omitempty"`
	Reasoning        string      `json:"reasoning,omitempty"`         // OpenRouter / Xiaomi-style reasoning
	ReasoningContent string      `json:"reasoning_content,omitempty"` // DeepSeek-style reasoning
}

// ContentString returns the content as a plain string.
// For text-only messages this returns the string directly.
// For multimodal messages ([]ContentPart) it concatenates all text parts.
func (m *Message) ContentString() string {
	if m == nil || m.Content == nil {
		return ""
	}
	switch v := m.Content.(type) {
	case string:
		return v
	case []ContentPart:
		var b strings.Builder
		for _, p := range v {
			if p.Type == ContentPartText && p.Text != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

// HasContentParts reports whether the message contains multimodal content parts.
func (m *Message) HasContentParts() bool {
	if m == nil || m.Content == nil {
		return false
	}
	parts, ok := m.Content.([]ContentPart)
	return ok && len(parts) > 0
}

// UnmarshalJSON decodes Message and normalizes multimodal content arrays to
// []ContentPart. encoding/json would otherwise store arrays in interface{} as
// []interface{}, which ContentString/HasContentParts would not recognize.
func (m *Message) UnmarshalJSON(data []byte) error {
	type messageWire struct {
		Role             string          `json:"role,omitempty"`
		Content          json.RawMessage `json:"content,omitempty"`
		Name             string          `json:"name,omitempty"`
		ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
		ToolCallID       string          `json:"tool_call_id,omitempty"`
		Reasoning        string          `json:"reasoning,omitempty"`
		ReasoningContent string          `json:"reasoning_content,omitempty"`
	}
	var wire messageWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	m.Role = wire.Role
	m.Name = wire.Name
	m.ToolCalls = wire.ToolCalls
	m.ToolCallID = wire.ToolCallID
	m.Reasoning = wire.Reasoning
	m.ReasoningContent = wire.ReasoningContent

	if len(wire.Content) == 0 || string(wire.Content) == "null" {
		m.Content = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(wire.Content, &s); err == nil {
		m.Content = s
		return nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(wire.Content, &parts); err == nil {
		m.Content = parts
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(wire.Content, &v); err != nil {
		return err
	}
	m.Content = v
	return nil
}

// MarshalJSON implements json.Marshaler for Message. It mirrors the struct
// tags but handles Content being interface{}: nil or empty string are omitted
// so streaming deltas keep backward compatibility.
func (m *Message) MarshalJSON() ([]byte, error) {
	// Quick check: if Content is nil or empty string, omit it entirely
	// by falling through to a type that omits content.
	type message Message // prevent recursion
	type messageOmitted struct {
		Role             string     `json:"role,omitempty"`
		Name             string     `json:"name,omitempty"`
		ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID       string     `json:"tool_call_id,omitempty"`
		Reasoning        string     `json:"reasoning,omitempty"`
		ReasoningContent string     `json:"reasoning_content,omitempty"`
	}

	if m.Content == nil {
		return json.Marshal(messageOmitted{
			Role:             m.Role,
			Name:             m.Name,
			ToolCalls:        m.ToolCalls,
			ToolCallID:       m.ToolCallID,
			Reasoning:        m.Reasoning,
			ReasoningContent: m.ReasoningContent,
		})
	}
	if s, ok := m.Content.(string); ok && s == "" {
		return json.Marshal(messageOmitted{
			Role:             m.Role,
			Name:             m.Name,
			ToolCalls:        m.ToolCalls,
			ToolCallID:       m.ToolCallID,
			Reasoning:        m.Reasoning,
			ReasoningContent: m.ReasoningContent,
		})
	}
	// Non-empty content — marshal normally using the struct tags.
	return json.Marshal((*message)(m))
}

// Normalize fills Content from reasoning fields when upstream returns empty
// content (common for reasoning models like big-pickle / mimo-v2.5). Chat apps
// that only read message.content otherwise show a blank reply.
func (m *Message) Normalize() {
	if m == nil {
		return
	}
	// Preserve existing content. Multimodal arrays can yield an empty
	// ContentString (e.g. image-only) and must not be replaced by reasoning.
	if m.ContentString() != "" || m.HasContentParts() {
		return
	}
	if m.ReasoningContent != "" {
		m.Content = m.ReasoningContent
		return
	}
	if m.Reasoning != "" {
		m.Content = m.Reasoning
	}
}

// NormalizeChoices normalizes message/delta content on each choice.
func NormalizeChoices(choices []Choice) {
	for i := range choices {
		choices[i].Message.Normalize()
		choices[i].Delta.Normalize()
	}
}

// Tool represents a tool/function definition
type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef represents a function definition
type FunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// ToolCall represents a tool call in a message
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function call
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatCompletionResponse represents an OpenAI-compatible chat completion response
type ChatCompletionResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

// Choice represents a choice in a chat completion response
type Choice struct {
	Index        int       `json:"index"`
	Message      *Message  `json:"message,omitempty"`
	Delta        *Message  `json:"delta,omitempty"`
	FinishReason *string   `json:"finish_reason,omitempty"`
	LogProbs     *LogProbs `json:"logprobs,omitempty"`
}

// LogProbs represents log probabilities
type LogProbs struct {
	TextOffset    []int                `json:"text_offset,omitempty"`
	TokenLogProbs []float64            `json:"token_logprobs,omitempty"`
	Tokens        []string             `json:"tokens,omitempty"`
	TopLogProbs   []map[string]float64 `json:"top_logprobs,omitempty"`
}

// Usage represents token usage
type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails breaks down prompt token usage.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
	AudioTokens  int `json:"audio_tokens,omitempty"`
}

// CompletionTokensDetails breaks down completion token usage.
type CompletionTokensDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	AudioTokens              int `json:"audio_tokens,omitempty"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

// StreamChunk represents a streaming chunk
type StreamChunk struct {
	ID      string   `json:"id,omitempty"`
	Object  string   `json:"object,omitempty"`
	Created int64    `json:"created,omitempty"`
	Model   string   `json:"model,omitempty"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
	Done    bool     `json:"-"` // True if this is the [DONE] sentinel
	Error   error    `json:"-"` // Non-nil if streaming failed
}

// IsEmpty reports whether the chunk carries no client-visible payload.
// Upstream proxies sometimes emit data: {} before [DONE]; forwarding those
// zero-value chunks clears model/id in aggregating clients (e.g. OpenCode).
func (c StreamChunk) IsEmpty() bool {
	if c.Done || c.Error != nil || c.Usage != nil {
		return false
	}
	if c.ID != "" || c.Object != "" || c.Model != "" || c.Created != 0 {
		return false
	}
	return len(c.Choices) == 0
}

// EmbeddingRequest represents an OpenAI-compatible embedding request
type EmbeddingRequest struct {
	Model string      `json:"model"`
	Input interface{} `json:"input"` // string or []string
	User  string      `json:"user,omitempty"`
}

// EmbeddingResponse represents an OpenAI-compatible embedding response
type EmbeddingResponse struct {
	Object string          `json:"object"`
	Data   []EmbeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  *Usage          `json:"usage,omitempty"`
}

// EmbeddingData represents embedding data
type EmbeddingData struct {
	Object    string    `json:"object"`
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

// ModelInfo represents model information
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	// Name is an optional short display label for model pickers. Clients that
	// only show id are unchanged; chat requests must still use id.
	Name string `json:"name,omitempty"`
}

// ModelList represents a list of models
type ModelList struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// ErrorResponse represents an OpenAI-compatible error response
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail represents error details
type ErrorDetail struct {
	Message string      `json:"message"`
	Type    string      `json:"type"`
	Param   interface{} `json:"param,omitempty"`
	Code    interface{} `json:"code,omitempty"`
}

// HealthResponse represents a health check response
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
}

// ProviderHealthResponse represents provider health status
type ProviderHealthResponse struct {
	Providers []ProviderHealth `json:"providers"`
}

// ProviderHealth represents a single provider's health status
type ProviderHealth struct {
	Name      string  `json:"name"`
	Healthy   bool    `json:"healthy"`
	LatencyMs int64   `json:"latency_ms"`
	LastError *string `json:"last_error,omitempty"`
	CheckedAt string  `json:"checked_at"`
}
