// Package openaibase provides a reusable OpenAI-compatible provider implementation.
// Provider-specific packages can embed Base and override pricing or health behavior.
package openaibase

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

// Base implements provider.Provider for OpenAI-compatible upstreams.
// It forwards chat completions, embeddings, and model listing, plus SSE streaming.
type Base struct {
	name        string
	apiKey      string
	baseURL     string
	client      *http.Client
	pricingFunc func(ctx context.Context) (map[string]provider.PricingInfo, error)
	healthPath  string
}

// Option configures a Base provider.
type Option func(*Base)

// WithPricing sets the pricing function. Defaults to an empty map.
func WithPricing(fn func(ctx context.Context) (map[string]provider.PricingInfo, error)) Option {
	return func(b *Base) { b.pricingFunc = fn }
}

// WithHealthPath sets the path used for health checks. Defaults to "/models".
func WithHealthPath(path string) Option {
	return func(b *Base) { b.healthPath = path }
}

// New creates a reusable OpenAI-compatible provider base.
func New(name, apiKey, baseURL string, timeout time.Duration, opts ...Option) *Base {
	b := &Base{
		name:    name,
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
		pricingFunc: func(ctx context.Context) (map[string]provider.PricingInfo, error) {
			return map[string]provider.PricingInfo{}, nil
		},
		healthPath: "/models",
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Name returns the provider name.
func (b *Base) Name() string { return b.name }

// ChatCompletion sends a non-streaming chat completion request.
func (b *Base) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := b.newRequest(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return nil, err
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, fmt.Sprintf("provider request failed: %v", err), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, b.handleErrorResponse(resp)
	}

	var chatResp apitypes.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to decode response", err)
	}
	apitypes.NormalizeChoices(chatResp.Choices)
	return &chatResp, nil
}

// ChatCompletionStream sends a streaming chat completion request.
func (b *Base) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (
	<-chan apitypes.StreamChunk, error) {
	req.Stream = true
	req.EnsureStreamUsage()

	body, err := json.Marshal(req)
	if err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := b.newRequest(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return nil, err
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, fmt.Sprintf("stream request failed: %v", err), err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, b.handleErrorResponse(resp)
	}

	ch := make(chan apitypes.StreamChunk)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		eventCh := sse.NewStreamReader(resp.Body)
		for event := range eventCh {
			// Upstream proxies (e.g. OpenRouter via OpenCode Zen) emit SSE
			// comments like ": OPENROUTER PROCESSING" which become empty
			// events. Skip them — unmarshaling "" aborts the whole stream.
			if strings.TrimSpace(event.Data) == "" {
				continue
			}
			if event.Data == "[DONE]" {
				ch <- apitypes.StreamChunk{Done: true}
				return
			}
			var chunk apitypes.StreamChunk
			if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
				ch <- apitypes.StreamChunk{Error: fmt.Errorf("failed to parse stream chunk: %w", err)}
				return
			}
			// Do not promote reasoning→content on stream deltas: models like
			// Nemotron emit reasoning chunks before content, and promoting
			// would concatenate thinking into the visible reply. Non-stream
			// responses still normalize; the handler flushes reasoning as
			// content only if the stream never sent any content.
			ch <- chunk
		}
	}()
	return ch, nil
}

// Embeddings sends an embeddings request.
func (b *Base) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := b.newRequest(ctx, http.MethodPost, "/embeddings", body)
	if err != nil {
		return nil, err
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, fmt.Sprintf("embeddings request failed: %v", err), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, b.handleErrorResponse(resp)
	}

	var embedResp apitypes.EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to decode response", err)
	}
	return &embedResp, nil
}

// ListModels returns available models from the upstream API.
func (b *Base) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	httpReq, err := b.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, fmt.Sprintf("models request failed: %v", err), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, b.handleErrorResponse(resp)
	}

	var apiResp apitypes.ModelList
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to decode response", err)
	}

	models := make([]provider.ModelInfo, 0, len(apiResp.Data))
	for _, m := range apiResp.Data {
		models = append(models, provider.ModelInfo{
			ProviderModelID: m.ID,
			ModelID:         m.ID,
			OwnedBy:         m.OwnedBy,
		})
	}
	return models, nil
}

// GetPricing returns the configured pricing map.
func (b *Base) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return b.pricingFunc(ctx)
}

// HealthCheck checks provider health by calling the configured health path.
func (b *Base) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	start := time.Now()

	httpReq, err := b.newRequest(ctx, http.MethodGet, b.healthPath, nil)
	if err != nil {
		return nil, err
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return &provider.HealthStatus{
			Provider:  b.name,
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
		Provider:  b.name,
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

// SupportsModel returns true for all models by default.
func (b *Base) SupportsModel(modelID string) bool { return true }

func (b *Base) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, bodyReader)
	if err != nil {
		return nil, provider.NewProviderError(b.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to create request", err)
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Authorization", "Bearer "+b.apiKey)
	return httpReq, nil
}

func (b *Base) handleErrorResponse(resp *http.Response) *provider.ProviderError {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.NewProviderError(b.name, resp.StatusCode,
			provider.ErrorTypeServerError, "failed to read error response", err)
	}

	var openAIErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &openAIErr); err == nil && openAIErr.Error.Message != "" {
		errType := openAIErr.Error.Type
		if errType == "" {
			errType = mapErrorType(resp.StatusCode)
		}
		return provider.NewProviderError(b.name, resp.StatusCode, errType, openAIErr.Error.Message, nil)
	}

	return provider.NewProviderError(b.name, resp.StatusCode,
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
