package gemini

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
)

// Provider implements the provider.Provider interface against Google Gemini's
// native generateContent API.
type Provider struct {
	name    string
	apiKey  string
	baseURL string
	client  *http.Client
	auth    *AuthConfig
}

// NewProvider creates a new native Gemini provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		name:    "gemini",
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
		auth:    NewAuthConfig(apiKey),
	}
}

// Name returns the provider name.
func (p *Provider) Name() string { return p.name }

// GetMetadata returns capability metadata for Gemini.
func (p *Provider) GetMetadata() provider.Metadata {
	meta := provider.NewMetadata(p.name, provider.Capabilities{
		Streaming:   true,
		Vision:      true,
		Reasoning:   true,
		ToolCalling: true,
		Structured:  true,
		LongContext: true,
		Embeddings:  true,
		Images:      true,
	})
	meta.BaseURL = p.baseURL
	meta.DisplayName = "Google Gemini"
	meta.Description = "Google Gemini native generateContent API"
	return meta
}

// ChatCompletion sends a non-streaming generateContent request.
func (p *Provider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	geminiReq := MapRequest(req)
	if geminiReq == nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadRequest,
			provider.ErrorTypeInvalidRequest, "failed to map request to Gemini format", nil)
	}

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, p.generateContentPath(req.Model), body)
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

	var genResp generateContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to decode response", err)
	}

	return MapResponse(req.Model, newResponseID(), &genResp), nil
}

// ChatCompletionStream sends a streaming generateContent request over SSE.
func (p *Provider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	geminiReq := MapRequest(req)
	if geminiReq == nil {
		errChan := make(chan apitypes.StreamChunk, 1)
		errChan <- apitypes.StreamChunk{Error: provider.NewProviderError(p.name, http.StatusBadRequest,
			provider.ErrorTypeInvalidRequest, "failed to translate Gemini request", nil)}
		close(errChan)
		return errChan, nil
	}

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to marshal request", err)
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, p.streamGenerateContentPath(req.Model), body)
	if err != nil {
		return nil, err
	}

	// Streaming must not be capped by the client timeout: long reasoning
	// budgets stream for minutes. Cancellation is contextual instead.
	// A dial timeout prevents stuck connections.
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if p.client.Transport == nil {
		p.client.Transport = &http.Transport{DialContext: dialer.DialContext}
	} else if t, ok := p.client.Transport.(*http.Transport); ok && t.DialContext == nil {
		cloned := t.Clone()
		cloned.DialContext = dialer.DialContext
		p.client.Transport = cloned
	}
	streamClient := &http.Client{Transport: p.client.Transport}

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

		accum := NewStreamAccumulator(req.Model, newResponseID())
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "data:") {
				line = strings.TrimPrefix(line, "data:")
				line = strings.TrimSpace(line)
			}
			if line == "" || line == "[DONE]" {
				continue
			}

			var event generateContentResponse
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				ch <- apitypes.StreamChunk{Error: fmt.Errorf("failed to parse Gemini stream event: %w", err)}
				return
			}

			if chunk := MapStreamChunk(&event, accum); len(chunk) > 0 {
				for _, c := range chunk {
					ch <- *c
				}
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- apitypes.StreamChunk{Error: err}
			return
		}

		ch <- apitypes.StreamChunk{Done: true}
	}()
	return ch, nil
}

// Embeddings embeds text via the native :embedContent endpoint. A single
// string is the canonical use; a slice is supported by issuing one request
// per input (Gemini's embedding API is single-content per call).
func (p *Provider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	inputs, err := extractEmbeddingInputs(req.Input)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadRequest,
			provider.ErrorTypeInvalidRequest, err.Error(), nil)
	}

	model := strings.TrimPrefix(req.Model, "models/")
	path := "/models/" + model + ":embedContent"

	data := make([]apitypes.EmbeddingData, 0, len(inputs))
	for i, input := range inputs {
		body, marshalErr := json.Marshal(map[string]interface{}{
			"content": map[string]interface{}{
				"parts": []map[string]string{{"text": input}},
			},
		})
		if marshalErr != nil {
			return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
				provider.ErrorTypeServerError, "failed to marshal embedding request", marshalErr)
		}

		httpReq, reqErr := p.newRequest(ctx, http.MethodPost, path, body)
		if reqErr != nil {
			return nil, reqErr
		}

		resp, doErr := p.client.Do(httpReq)
		if doErr != nil {
			return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
				provider.ErrorTypeProviderUnavailable, "embedding request failed: "+doErr.Error(), doErr)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, p.handleErrorResponse(resp)
		}

		var embedResp struct {
			Embedding struct {
				Values []float64 `json:"values"`
			} `json:"embedding"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&embedResp)
		if decodeErr != nil {
			return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
				provider.ErrorTypeServerError, "failed to decode embedding response", decodeErr)
		}
		data = append(data, apitypes.EmbeddingData{
			Object:    "embedding",
			Embedding: embedResp.Embedding.Values,
			Index:     i,
		})
	}

	return &apitypes.EmbeddingResponse{
		Object: "list",
		Data:   data,
		Model:  req.Model,
	}, nil
}

func extractEmbeddingInputs(input interface{}) ([]string, error) {
	switch v := input.(type) {
	case string:
		if v == "" {
			return nil, fmt.Errorf("empty embedding input")
		}
		return []string{v}, nil
	case []string:
		if len(v) == 0 {
			return nil, fmt.Errorf("empty embedding input")
		}
		return v, nil
	case []interface{}:
		vals := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("embedding input must be a string or []string")
			}
			vals = append(vals, s)
		}
		return vals, nil
	default:
		return nil, fmt.Errorf("embedding input must be a string or []string")
	}
}

// ListModels returns models from the native GET /models catalog.
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	httpReq, err := p.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusBadGateway,
			provider.ErrorTypeProviderUnavailable, "models request failed: "+err.Error(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, p.handleErrorResponse(resp)
	}

	var catalog struct {
		Models []struct {
			Name         string   `json:"name"`
			DisplayName  string   `json:"displayName,omitempty"`
			Version      string   `json:"version,omitempty"`
			SupportedGen []string `json:"supportedGenerationMethods,omitempty"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to decode models response", err)
	}

	models := make([]provider.ModelInfo, 0, len(catalog.Models))
	for _, m := range catalog.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		if id == "" {
			continue
		}
		models = append(models, provider.ModelInfo{
			ProviderModelID: id,
			ModelID:         id,
			OwnedBy:         "google",
		})
	}
	return models, nil
}

// GetPricing returns the static pricing map for known Gemini models.
func (p *Provider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"gemini-1.5-pro": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0035,
			OutputPrice: 0.0105,
			Currency:    "USD",
		},
		"gemini-1.5-flash": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00035,
			OutputPrice: 0.00105,
			Currency:    "USD",
		},
		"gemini-1.5-flash-8b": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.000075,
			OutputPrice: 0.0003,
			Currency:    "USD",
		},
	}, nil
}

// HealthCheck probes the models catalog endpoint and measures latency.
func (p *Provider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	start := time.Now()

	httpReq, err := p.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return &provider.HealthStatus{
			Provider:  p.name,
			IsHealthy: false,
			LatencyMs: int64(time.Since(start) / time.Millisecond),
			LastError: err.Error(),
			CheckedAt: time.Now(),
		}, nil
	}
	defer resp.Body.Close()

	latency := int64(time.Since(start) / time.Millisecond)
	status := &provider.HealthStatus{
		Provider:  p.name,
		IsHealthy: resp.StatusCode == http.StatusOK,
		LatencyMs: latency,
		CheckedAt: time.Now(),
	}
	if !status.IsHealthy {
		status.LastError = "HTTP " + strconv.Itoa(resp.StatusCode)
	}
	return status, nil
}

// SupportsModel returns true for Gemini model ids (gemini-* / models/* names).
func (p *Provider) SupportsModel(modelID string) bool {
	m := strings.TrimPrefix(modelID, "models/")
	switch m {
	case "gemini-1.5-pro", "gemini-1.5-flash", "gemini-1.5-flash-8b",
		"gemini-2.0-flash", "gemini-2.5-flash", "gemini-2.5-pro":
		return true
	}
	if strings.HasPrefix(m, "gemini-") || strings.HasPrefix(m, "text-embedding-") {
		return true
	}
	return false
}

// newResponseID synthesizes a stable-enough response id because generateContent
// does not return one.
func newResponseID() string {
	return "gemini-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func timeNowUnix() int64 {
	return time.Now().Unix()
}
