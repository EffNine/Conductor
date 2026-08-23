package cerebras_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/cerebras"
)

var _ provider.Provider = (*cerebras.Provider)(nil)

func TestProviderDefaults(t *testing.T) {
	p := cerebras.NewProvider("test-key", "", 10*time.Second)
	if p.Name() != "cerebras" {
		t.Fatalf("Name() = %q, want cerebras", p.Name())
	}
	if got := p.GetMetadata().BaseURL; got != "https://api.cerebras.ai/v1" {
		t.Fatalf("default base URL = %q, want https://api.cerebras.ai/v1", got)
	}

	custom := cerebras.NewProvider("test-key", "http://example.com/v1", 10*time.Second)
	if got := custom.GetMetadata().BaseURL; got != "http://example.com/v1" {
		t.Fatalf("base URL = %q, want http://example.com/v1", got)
	}
}

func TestChatCompletionForwardsOpenAIRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}

		var req apitypes.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "llama3.1-8b" {
			t.Fatalf("model = %q, want llama3.1-8b", req.Model)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
			ID:      "chatcmpl-1",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []apitypes.Choice{
				{
					Index:        0,
					Message:      &apitypes.Message{Role: "assistant", Content: "Hello from Cerebras"},
					FinishReason: str("stop"),
				},
			},
			Usage: &apitypes.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		})
	}))
	defer server.Close()

	p := cerebras.NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "llama3.1-8b",
		Messages: []apitypes.Message{{Role: "user", Content: "Hello!"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Model != "llama3.1-8b" {
		t.Fatalf("resp.Model = %q, want llama3.1-8b", resp.Model)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
	}
}

func TestChatCompletionReturnsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "rate limit exceeded", "type": "rate_limit_error"},
		})
	}))
	defer server.Close()

	p := cerebras.NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "llama3.1-8b",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	provErr, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if provErr.Type != provider.ErrorTypeRateLimit {
		t.Fatalf("error type = %q, want rate_limit_error", provErr.Type)
	}
}

func TestListModelsParsesOpenAIResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apitypes.ModelList{
			Object: "list",
			Data: []apitypes.ModelInfo{
				{ID: "llama3.1-8b", Object: "model", OwnedBy: "Meta"},
			},
		})
	}))
	defer server.Close()

	p := cerebras.NewProvider("test-key", server.URL, 10*time.Second)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ProviderModelID != "llama3.1-8b" {
		t.Fatalf("models = %v", models)
	}
}

func TestGetPricingReturnsKnownModels(t *testing.T) {
	tests := []struct {
		model       string
		inputPrice  float64
		outputPrice float64
	}{
		{"gpt-oss-120b", 0.0006, 0.0006},
		{"gemma-4-31b", 0.0004, 0.0004},
		{"zai-glm-4.7", 0.00005, 0.00005},
	}

	p := cerebras.NewProvider("test-key", "", 10*time.Second)
	prices, err := p.GetPricing(context.Background())
	if err != nil {
		t.Fatalf("GetPricing: %v", err)
	}
	if len(prices) != len(tests) {
		t.Fatalf("len(prices) = %d, want %d", len(prices), len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, ok := prices[tt.model]
			if !ok {
				t.Fatalf("missing pricing for %q in %v", tt.model, prices)
			}
			if got.UnitType != provider.UnitToken {
				t.Fatalf("unit type = %q, want token", got.UnitType)
			}
			if got.UnitSize != 1000 {
				t.Fatalf("unit size = %d, want 1000", got.UnitSize)
			}
			if got.InputPrice != tt.inputPrice {
				t.Fatalf("input price = %v, want %v", got.InputPrice, tt.inputPrice)
			}
			if got.OutputPrice != tt.outputPrice {
				t.Fatalf("output price = %v, want %v", got.OutputPrice, tt.outputPrice)
			}
			if got.Currency != "USD" {
				t.Fatalf("currency = %q, want USD", got.Currency)
			}
		})
	}
}

func TestSupportsModelAcceptsAnyID(t *testing.T) {
	p := cerebras.NewProvider("test-key", "", 10*time.Second)
	if !p.SupportsModel("llama3.1-8b") {
		t.Fatal("expected SupportsModel(llama3.1-8b) = true")
	}
}

func str(s string) *string { return &s }
