package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
)

// Provider implements the provider.Provider interface for Anthropic\'s Messages API.
type Provider struct {
	name    string
	apiKey  string
	baseURL string
	client  *http.Client
	auth    *AuthConfig
}

// NewProvider creates a new Anthropic provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	p := &Provider{
		name:    "anthropic",
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
		auth:    NewAuthConfig(apiKey),
	}
	return p
}

// Name returns the provider name.
func (p *Provider) Name() string { return p.name }

// GetMetadata returns metadata for this provider.
func (p *Provider) GetMetadata() provider.Metadata {
	meta := provider.NewMetadata(p.name, provider.Capabilities{
		Streaming:   true,
		Reasoning:   true,
		ToolCalling: true,
		LongContext: true,
		Images:      true,
	})
	meta.BaseURL = p.baseURL
	meta.DisplayName = "Anthropic"
	meta.Description = "Anthropic Claude messages API"
	return meta
}

// ChatCompletion converts an OpenAI request to Anthropic Messages format.
func (p *Provider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	anthropicReq := MapRequest(req)
	if anthropicReq == nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadRequest,
			provider.ErrorTypeInvalidRequest, "Anthropic does not support structured output (response_format)", nil)
	}

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/messages", body)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, "provider request failed: "+err.Error(), err)
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

	return MapResponse(req.Model, &msgResp), nil
}

// ChatCompletionStream converts an OpenAI streaming request to Anthropic streaming format.
func (p *Provider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	anthropicReq := MapRequest(req)
	if anthropicReq == nil {
		errChan := make(chan apitypes.StreamChunk, 1)
		errChan <- apitypes.StreamChunk{Error: provider.NewProviderError(p.name, http.StatusBadRequest,
			provider.ErrorTypeInvalidRequest, "Anthropic does not support structured output (response_format)", nil)}
		close(errChan)
		return errChan, nil
	}
	anthropicReq.Stream = true

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/messages", body)
	if err != nil {
		return nil, err
	}

	streamClient := &http.Client{Transport: p.client.Transport}
	if streamClient.Transport == nil {
		streamClient.Transport = http.DefaultTransport
	} else if t, ok := streamClient.Transport.(*http.Transport); ok && t.DialContext == nil {
		cloned := t.Clone()
		cloned.DialContext = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
		streamClient.Transport = cloned
	}

	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, "stream request failed: "+err.Error(), err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, p.handleErrorResponse(resp)
	}

	ch := make(chan apitypes.StreamChunk)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		accum := NewStreamAccumulator()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			line = strings.TrimPrefix(line, "data: ")
			if strings.TrimSpace(line) == "" {
				continue
			}
			var raw json.RawMessage = json.RawMessage(line)
			var event anthropicStreamEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				ch <- apitypes.StreamChunk{Error: err}
				return
			}

			chunk := MapStreamChunk(&event, accum)
			if chunk != nil {
				ch <- *chunk
			}
		}

		ch <- apitypes.StreamChunk{Done: true}
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

// HealthCheck checks provider health via a minimal Anthropic API call.
func (p *Provider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	start := time.Now()

	body, _ := json.Marshal(&anthropicMessagesRequest{
		Model:     "claude-3-5-haiku-20241022",
		MaxTokens: 1,
		Messages:  []anthropicMessage{{Role: "user", Content: "hi"}},
	})
	httpReq, err := p.newRequest(ctx, http.MethodPost, "/messages", body)
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
		status.LastError = "HTTP " + strconv.Itoa(resp.StatusCode) + ": " + string(body)
	}
	return status, nil
}

// SupportsModel returns true for known Anthropic model IDs.
func (p *Provider) SupportsModel(modelID string) bool {
	switch modelID {
	case "claude-3-7-sonnet-20250219", "claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022", "claude-3-opus-20240229":
		return true
	}
	return false
}
