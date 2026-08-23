package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
)

// Provider implements the provider.Provider interface for Cloudflare Workers AI.
// Workers AI uses a custom REST API, not OpenAI-compatible.
type Provider struct {
	name      string
	apiToken  string
	accountID string
	baseURL   string
	client    *http.Client
}

// NewProvider creates a new Cloudflare Workers AI provider.
// apiKey is the Cloudflare API token. baseURL should contain the account ID
// in the path (e.g. "https://api.cloudflare.com/client/v4/accounts/ABC123").
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	if baseURL == "" {
		baseURL = "https://api.cloudflare.com/client/v4"
	}
	accountID := extractCloudflareAccountID(baseURL)
	return &Provider{
		name:      "cloudflare",
		apiToken:  apiKey,
		accountID: accountID,
		baseURL:   baseURL,
		client:    &http.Client{Timeout: timeout},
	}
}

// accountsBase returns the URL prefix that already includes the account
// segment. If baseURL carries the account ID in its path (the documented
// configuration), it is returned as-is; otherwise the account segment is
// appended.
func (p *Provider) accountsBase() string {
	if p.accountID != "" && strings.Contains(p.baseURL, "/accounts/"+p.accountID) {
		return p.baseURL
	}
	return fmt.Sprintf("%s/account/%s", p.baseURL, p.accountID)
}

func extractCloudflareAccountID(url string) string {
	marker := "/accounts/"
	for i := 0; i+len(marker) <= len(url); i++ {
		if url[i:i+len(marker)] == marker {
			j := i + len(marker)
			for j < len(url) && url[j] != '/' && url[j] != '?' {
				j++
			}
			if j > i+len(marker) {
				return url[i+len(marker) : j]
			}
		}
	}
	return ""
}

// Name returns the provider name.
func (p *Provider) Name() string { return p.name }

// GetMetadata returns metadata for the Cloudflare provider.
func (p *Provider) GetMetadata() provider.Metadata {
	meta := provider.NewMetadata(p.name, provider.Capabilities{
		Streaming:   false,
		Reasoning:   true,
		ToolCalling: false,
		LongContext: true,
	})
	meta.BaseURL = p.baseURL
	meta.DisplayName = "Cloudflare Workers AI"
	meta.Description = "Cloudflare Workers AI inference on the edge"
	meta.DocumentationURL = "https://developers.cloudflare.com/workers-ai/"
	return meta
}

// ChatCompletion sends a chat request to Cloudflare Workers AI.
func (p *Provider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	model := req.Model
	if model == "" {
		model = "@hf/meta-llama/llama-3-8b-instruct"
	}
	prompt := extractLastUserMessage(req.Messages)
	if prompt == "" {
		return nil, provider.NewProviderError(p.name, http.StatusBadRequest,
			provider.ErrorTypeInvalidRequest, "no user message found", nil)
	}

	url := fmt.Sprintf("%s/ai/run/%s", p.accountsBase(), model)

	body := map[string]any{
		"prompt": prompt,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, "failed to create request: "+err.Error(), err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiToken)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, "request failed: "+err.Error(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, provider.NewProviderError(p.name, resp.StatusCode,
			provider.ErrorTypeServerError, fmt.Sprintf("workers ai error: %s", string(respBody)), nil)
	}

	var result struct {
		Result struct {
			Response string `json:"response"`
		} `json:"result"`
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to decode response", err)
	}
	if !result.Success {
		msg := "workers ai request failed"
		if len(result.Errors) > 0 {
			msg = result.Errors[0].Message
		}
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, msg, nil)
	}

	respID := fmt.Sprintf("chatcmpl-%s", time.Now().Format("20060102150405"))
	now := time.Now()
	stopReason := "stop"
	msg := &apitypes.Message{
		Role:    "assistant",
		Content: result.Result.Response,
	}
	return &apitypes.ChatCompletionResponse{
		ID:      respID,
		Object:  "chat.completion",
		Created: now.Unix(),
		Model:   req.Model,
		Choices: []apitypes.Choice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: &stopReason,
			},
		},
		Usage: &apitypes.Usage{
			PromptTokens:     estimateTokens(prompt),
			CompletionTokens: estimateTokens(result.Result.Response),
			TotalTokens:      estimateTokens(prompt) + estimateTokens(result.Result.Response),
		},
	}, nil
}

// ChatCompletionStream is not supported by Workers AI chat endpoints.
func (p *Provider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.NewProviderError(p.name, http.StatusNotImplemented,
		provider.ErrorTypeServerError, "streaming is not supported by Cloudflare Workers AI chat endpoints", nil)
}

// Embeddings is not supported by Workers AI.
func (p *Provider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.NewProviderError(p.name, http.StatusNotImplemented,
		provider.ErrorTypeServerError, "embeddings are not supported by Cloudflare Workers AI", nil)
}

// ListModels returns known Workers AI text generation models.
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	url := fmt.Sprintf("%s/ai/models", p.accountsBase())
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiToken)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, "failed to list models: "+err.Error(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, provider.NewProviderError(p.name, resp.StatusCode,
			provider.ErrorTypeServerError, "failed to list models", nil)
	}

	var result struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]provider.ModelInfo, 0, len(result.Result))
	for _, m := range result.Result {
		models = append(models, provider.ModelInfo{
			ProviderModelID: "@" + m.ID,
			ModelID:         "@" + m.ID,
			OwnedBy:         "cloudflare",
		})
	}
	return models, nil
}

// GetPricing returns pricing for Workers AI (Neurons-based billing).
func (p *Provider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"@hf/meta-llama/llama-3-8b-instruct": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.000011,
			OutputPrice: 0.000011,
			Currency:    "USD",
		},
		"@hf/nousresearch/hermes-3-llama-3.1-405b": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.000022,
			OutputPrice: 0.000022,
			Currency:    "USD",
		},
	}, nil
}

// HealthCheck pings the Models endpoint.
func (p *Provider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	start := time.Now()
	url := fmt.Sprintf("%s/ai/models", p.accountsBase())
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiToken)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, "health check failed: "+err.Error(), err)
	}
	defer resp.Body.Close()

	latency := time.Since(start).Milliseconds()
	isHealthy := resp.StatusCode == http.StatusOK

	return &provider.HealthStatus{
		Provider:  p.name,
		IsHealthy: isHealthy,
		LatencyMs: latency,
		LastError: "",
		CheckedAt: time.Now(),
	}, nil
}

// SupportsModel returns true for any model (Workers AI uses @ prefixed IDs).
func (p *Provider) SupportsModel(modelID string) bool {
	return len(modelID) > 0
}

// --- helpers ---

func extractLastUserMessage(messages []apitypes.Message) string {
	var last string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			switch v := messages[i].Content.(type) {
			case string:
				return v
			case []apitypes.ContentPart:
				for _, part := range v {
					if part.Type == "text" {
						last = part.Text
					}
				}
			}
		}
	}
	return last
}

func estimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return len(s) / 4
}
