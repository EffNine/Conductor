package apitypes

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

// Message represents a chat message
type Message struct {
	// Role and Content use omitempty so stream deltas do not emit empty
	// strings. OpenCode's custom OpenAI client rejects delta.role:"" (Zod) and
	// trailing {} chunks that re-marshal as empty model/content wipe the reply.
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	Name             string     `json:"name,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Reasoning        string     `json:"reasoning,omitempty"`         // OpenRouter / Xiaomi-style reasoning
	ReasoningContent string     `json:"reasoning_content,omitempty"` // DeepSeek-style reasoning
}

// Normalize fills Content from reasoning fields when upstream returns empty
// content (common for reasoning models like big-pickle / mimo-v2.5). Chat apps
// that only read message.content otherwise show a blank reply.
func (m *Message) Normalize() {
	if m == nil || m.Content != "" {
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
	Status string `json:"status"`
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
