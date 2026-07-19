package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/provider"
	"github.com/novexa/gateway/pkg/sse"
)

// Provider implements the provider.Provider interface for Anthropic's Messages API.
type Provider struct {
	name    string
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewProvider creates a new Anthropic provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		name:    "anthropic",
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

// Name returns the provider name.
func (p *Provider) Name() string { return p.name }

// ChatCompletion converts an OpenAI request to Anthropic Messages format.
func (p *Provider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	anthropicReq, err := p.toMessagesRequest(req)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := p.newRequest(ctx, "/messages", body)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, fmt.Sprintf("provider request failed: %v", err), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, p.handleErrorResponse(resp)
	}

	var msgResp anthropicMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&msgResp); err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to decode response", err)
	}

	return p.toChatCompletionResponse(req.Model, &msgResp), nil
}

// ChatCompletionStream converts an OpenAI streaming request to Anthropic streaming format.
func (p *Provider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	anthropicReq, err := p.toMessagesRequest(req)
	if err != nil {
		return nil, err
	}
	anthropicReq.Stream = true

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := p.newRequest(ctx, "/messages", body)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, fmt.Sprintf("stream request failed: %v", err), err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, p.handleErrorResponse(resp)
	}

	ch := make(chan apitypes.StreamChunk)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		eventCh := sse.NewStreamReader(resp.Body)
		var id, model string
		var created int64
		contentBuffer := &strings.Builder{}

		for event := range eventCh {
			if event.Data == "[DONE]" {
				ch <- apitypes.StreamChunk{Done: true}
				return
			}

			var eventObj map[string]json.RawMessage
			if err := json.Unmarshal([]byte(event.Data), &eventObj); err != nil {
				ch <- apitypes.StreamChunk{Error: fmt.Errorf("failed to parse stream event: %w", err)}
				return
			}

			if msgStart, ok := eventObj["message_start"]; ok {
				var ms struct {
					Message struct {
						ID      string `json:"id"`
						Model   string `json:"model"`
						Usage   usage  `json:"usage"`
					} `json:"message"`
				}
				_ = json.Unmarshal(msgStart, &ms)
				id = ms.Message.ID
				model = ms.Message.Model
				created = time.Now().Unix()
			}

			if delta, ok := eventObj["content_block_delta"]; ok {
				var d struct {
					Index int `json:"index"`
					Delta struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"delta"`
				}
				_ = json.Unmarshal(delta, &d)
				if d.Delta.Type == "text_delta" {
					contentBuffer.WriteString(d.Delta.Text)
					ch <- apitypes.StreamChunk{
						ID:      id,
						Object:  "chat.completion.chunk",
						Created: created,
						Model:   model,
						Choices: []apitypes.Choice{
							{
								Index: d.Index,
								Delta: &apitypes.Message{Role: "assistant", Content: d.Delta.Text},
							},
						},
					}
				}
			}

			if msgDelta, ok := eventObj["message_delta"]; ok {
				var md struct {
					Delta struct {
						StopReason   string `json:"stop_reason"`
						StopSequence string `json:"stop_sequence"`
					} `json:"delta"`
					Usage usage `json:"usage"`
				}
				_ = json.Unmarshal(msgDelta, &md)
				finishReason := md.Delta.StopReason
				ch <- apitypes.StreamChunk{
					ID:      id,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []apitypes.Choice{
						{
							Index:        0,
							Delta:        &apitypes.Message{},
							FinishReason: &finishReason,
						},
					},
					Usage: &apitypes.Usage{
						PromptTokens:     md.Usage.InputTokens,
						CompletionTokens: md.Usage.OutputTokens,
						TotalTokens:      md.Usage.InputTokens + md.Usage.OutputTokens,
					},
				}
			}
		}
	}()
	return ch, nil
}

// Embeddings is not supported by Anthropic.
func (p *Provider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.NewProviderError(p.name, http.StatusBadRequest,
		provider.ErrorTypeInvalidRequest, "Anthropic does not provide embeddings", nil)
}

// ListModels returns a static catalog of known Anthropic models.
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{
		{ProviderModelID: "claude-3-7-sonnet-20250219", ModelID: "claude-3-7-sonnet-20250219", OwnedBy: "anthropic"},
		{ProviderModelID: "claude-3-5-sonnet-20241022", ModelID: "claude-3-5-sonnet-20241022", OwnedBy: "anthropic"},
		{ProviderModelID: "claude-3-5-haiku-20241022", ModelID: "claude-3-5-haiku-20241022", OwnedBy: "anthropic"},
		{ProviderModelID: "claude-3-opus-20240229", ModelID: "claude-3-opus-20240229", OwnedBy: "anthropic"},
	}, nil
}

// GetPricing returns a static pricing map for known Anthropic models.
func (p *Provider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"claude-3-7-sonnet-20250219": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.003,
			OutputPrice: 0.015,
			Currency:    "USD",
		},
		"claude-3-5-sonnet-20241022": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.003,
			OutputPrice: 0.015,
			Currency:    "USD",
		},
		"claude-3-5-haiku-20241022": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0008,
			OutputPrice: 0.004,
			Currency:    "USD",
		},
		"claude-3-opus-20240229": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.015,
			OutputPrice: 0.075,
			Currency:    "USD",
		},
	}, nil
}

// HealthCheck checks provider health via the Anthropic API.
func (p *Provider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	start := time.Now()

	body, _ := json.Marshal(anthropicMessagesRequest{
		Model:     "claude-3-5-haiku-20241022",
		MaxTokens: 1,
		Messages:  []anthropicMessage{{Role: "user", Content: "hi"}},
	})
	httpReq, err := p.newRequest(ctx, "/messages", body)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return &provider.HealthStatus{
			Provider:  p.name,
			IsHealthy: false,
			LatencyMs: time.Since(start).Milliseconds(),
			LastError: err.Error(),
			CheckedAt: time.Now(),
		}, nil
	}
	defer resp.Body.Close()

	latency := time.Since(start).Milliseconds()
	isHealthy := resp.StatusCode == http.StatusOK

	status := &provider.HealthStatus{
		Provider:  p.name,
		IsHealthy: isHealthy,
		LatencyMs: latency,
		CheckedAt: time.Now(),
	}
	if !isHealthy {
		body, _ := io.ReadAll(resp.Body)
		status.LastError = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return status, nil
}

// SupportsModel returns true for all known Anthropic model IDs.
func (p *Provider) SupportsModel(modelID string) bool {
	switch modelID {
	case "claude-3-7-sonnet-20250219", "claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022", "claude-3-opus-20240229":
		return true
	}
	return false
}

func (p *Provider) toMessagesRequest(req *apitypes.ChatCompletionRequest) (*anthropicMessagesRequest, error) {
	if req.Model == "" {
		return nil, provider.NewProviderError(p.name, http.StatusBadRequest,
			provider.ErrorTypeInvalidRequest, "model is required", nil)
	}

	anthropicReq := &anthropicMessagesRequest{
		Model:     req.Model,
		MaxTokens: 4096,
		Messages:  make([]anthropicMessage, 0, len(req.Messages)),
	}

	if req.MaxTokens != nil {
		anthropicReq.MaxTokens = *req.MaxTokens
	}
	if req.Temperature != nil {
		anthropicReq.Temperature = req.Temperature
	}
	if req.TopP != nil {
		anthropicReq.TopP = req.TopP
	}
	if req.Stop != nil {
		switch v := req.Stop.(type) {
		case string:
			anthropicReq.StopSequences = []string{v}
		case []string:
			anthropicReq.StopSequences = v
		}
	}

	for _, m := range req.Messages {
		role := m.Role
		if role == "system" {
			if anthropicReq.System == "" {
				anthropicReq.System = m.Content
			}
			continue
		}
		if role == "assistant" {
			role = "assistant"
		} else if role == "user" {
			role = "user"
		}
		anthropicReq.Messages = append(anthropicReq.Messages, anthropicMessage{
			Role:    role,
			Content: m.Content,
		})
	}

	if len(anthropicReq.Messages) == 0 {
		return nil, provider.NewProviderError(p.name, http.StatusBadRequest,
			provider.ErrorTypeInvalidRequest, "at least one user/assistant message is required", nil)
	}

	return anthropicReq, nil
}

func (p *Provider) toChatCompletionResponse(modelID string, msg *anthropicMessageResponse) *apitypes.ChatCompletionResponse {
	content := ""
	for _, c := range msg.Content {
		if c.Type == "text" {
			content += c.Text
		}
	}

	finishReason := msg.StopReason
	if finishReason == "end_turn" {
		finishReason = "stop"
	}

	return &apitypes.ChatCompletionResponse{
		ID:      msg.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelID,
		Choices: []apitypes.Choice{
			{
				Index: 0,
				Message: &apitypes.Message{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: &finishReason,
			},
		},
		Usage: &apitypes.Usage{
			PromptTokens:     msg.Usage.InputTokens,
			CompletionTokens: msg.Usage.OutputTokens,
			TotalTokens:      msg.Usage.InputTokens + msg.Usage.OutputTokens,
		},
	}
}

func (p *Provider) newRequest(ctx context.Context, path string, body []byte) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to create request", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	return httpReq, nil
}

func (p *Provider) handleErrorResponse(resp *http.Response) *provider.ProviderError {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.NewProviderError(p.name, resp.StatusCode,
			provider.ErrorTypeServerError, "failed to read error response", err)
	}

	var anthropicErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &anthropicErr); err == nil && anthropicErr.Error.Message != "" {
		errType := anthropicErr.Error.Type
		if errType == "" {
			errType = mapErrorType(resp.StatusCode)
		}
		return provider.NewProviderError(p.name, resp.StatusCode, errType, anthropicErr.Error.Message, nil)
	}

	return provider.NewProviderError(p.name, resp.StatusCode,
		mapErrorType(resp.StatusCode),
		fmt.Sprintf("provider returned status %d", resp.StatusCode), nil)
}

func mapErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized:
		return provider.ErrorTypeAuthentication
	case http.StatusTooManyRequests:
		return provider.ErrorTypeRateLimit
	case http.StatusBadRequest:
		return provider.ErrorTypeInvalidRequest
	default:
		return provider.ErrorTypeServerError
	}
}

type anthropicMessagesRequest struct {
	Model         string              `json:"model"`
	MaxTokens     int                 `json:"max_tokens"`
	System        string              `json:"system,omitempty"`
	Messages      []anthropicMessage  `json:"messages"`
	Temperature   *float64            `json:"temperature,omitempty"`
	TopP          *float64            `json:"top_p,omitempty"`
	StopSequences []string            `json:"stop_sequences,omitempty"`
	Stream        bool                `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicMessageResponse struct {
	ID         string                `json:"id"`
	Type       string                `json:"type"`
	Role       string                `json:"role"`
	Model      string                `json:"model"`
	Content    []anthropicContent    `json:"content"`
	StopReason string                `json:"stop_reason"`
	Usage      usage                 `json:"usage"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
