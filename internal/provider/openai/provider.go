package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/provider"
	"github.com/novexa/gateway/pkg/sse"
)

// Provider implements the provider.Provider interface for OpenAI-compatible APIs
type Provider struct {
	name    string
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewProvider creates a new OpenAI provider
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		name:    "openai",
		apiKey:  apiKey,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return p.name
}

// ChatCompletion sends a non-streaming chat completion request
func (p *Provider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to create request", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, fmt.Sprintf("provider request failed: %v", err), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, p.handleErrorResponse(resp)
	}

	var chatResp apitypes.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to decode response", err)
	}

	return &chatResp, nil
}

// ChatCompletionStream sends a streaming chat completion request
func (p *Provider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to create request", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

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
		for event := range eventCh {
			if event.Data == "[DONE]" {
				ch <- apitypes.StreamChunk{Done: true}
				return
			}

			var chunk apitypes.StreamChunk
			if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
				ch <- apitypes.StreamChunk{Error: fmt.Errorf("failed to parse stream chunk: %w", err)}
				return
			}

			ch <- chunk
		}
	}()

	return ch, nil
}

// Embeddings sends an embeddings request
func (p *Provider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to create request", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, fmt.Sprintf("embeddings request failed: %v", err), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, p.handleErrorResponse(resp)
	}

	var embedResp apitypes.EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to decode response", err)
	}

	return &embedResp, nil
}

// ListModels returns available models from the OpenAI API
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.baseURL+"/models", nil)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to create request", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, fmt.Sprintf("models request failed: %v", err), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, p.handleErrorResponse(resp)
	}

	var apiResp struct {
		Object string               `json:"object"`
		Data   []apitypes.ModelInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
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

// GetPricing returns pricing information for OpenAI models.
// Uses a manual fallback map for known models; returns empty map for unknown models.
func (p *Provider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
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

// HealthCheck checks provider health
func (p *Provider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	start := time.Now()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

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

// SupportsModel checks if this provider supports a model
func (p *Provider) SupportsModel(modelID string) bool {
	// OpenAI provider supports all models by default
	// Specific model validation can be added here
	return true
}

// handleErrorResponse parses an error response from the provider
func (p *Provider) handleErrorResponse(resp *http.Response) *provider.ProviderError {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.NewProviderError(p.name, resp.StatusCode,
			provider.ErrorTypeServerError, "failed to read error response", err)
	}

	// Try to parse OpenAI error format
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
		return provider.NewProviderError(p.name, resp.StatusCode, errType, openAIErr.Error.Message, nil)
	}

	// Fallback: use status code
	return provider.NewProviderError(p.name, resp.StatusCode,
		mapErrorType(resp.StatusCode),
		fmt.Sprintf("provider returned status %d", resp.StatusCode), nil)
}

// mapErrorType maps HTTP status codes to error types
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
