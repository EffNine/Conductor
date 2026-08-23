package mistral_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/mistral"
)

var _ provider.Provider = (*mistral.Provider)(nil)

func TestProviderDefaults(t *testing.T) {
	p := mistral.NewProvider("test-key", "", 10*time.Second)
	if p.Name() != "mistral" {
		t.Fatalf("Name() = %q, want mistral", p.Name())
	}
	if got := p.GetMetadata().BaseURL; got != "https://api.mistral.ai/v1" {
		t.Fatalf("default base URL = %q, want https://api.mistral.ai/v1", got)
	}

	custom := mistral.NewProvider("test-key", "http://example.com/v1", 10*time.Second)
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
		if req.Model != "mistral-large-latest" {
			t.Fatalf("model = %q, want mistral-large-latest", req.Model)
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
					Message:      &apitypes.Message{Role: "assistant", Content: "Hello from Mistral"},
					FinishReason: str("stop"),
				},
			},
			Usage: &apitypes.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		})
	}))
	defer server.Close()

	p := mistral.NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "mistral-large-latest",
		Messages: []apitypes.Message{{Role: "user", Content: "Hello!"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Model != "mistral-large-latest" {
		t.Fatalf("resp.Model = %q, want mistral-large-latest", resp.Model)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
	}
}

func TestEmbeddingsForwardsOpenAIRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apitypes.EmbeddingResponse{
			Object: "list",
			Data: []apitypes.EmbeddingData{
				{Object: "embedding", Embedding: []float64{0.1, 0.2}, Index: 0},
			},
			Model: "mistral-embed",
			Usage: &apitypes.Usage{PromptTokens: 4, TotalTokens: 4},
		})
	}))
	defer server.Close()

	p := mistral.NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.Embeddings(context.Background(), &apitypes.EmbeddingRequest{
		Model: "mistral-embed",
		Input: "hello",
	})
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(resp.Data))
	}
}

func TestGetPricingReturnsKnownModels(t *testing.T) {
	tests := []struct {
		model       string
		inputPrice  float64
		outputPrice float64
	}{
		{"mistral-large-latest", 0.0005, 0.0015},
		{"mistral-small-latest", 0.0001, 0.0003},
		{"ministral-3b-latest", 0.00004, 0.00004},
		{"ministral-8b-latest", 0.0001, 0.0001},
		{"codestral-latest", 0.0003, 0.0009},
		{"codestral-2508", 0.0003, 0.0009},
		{"mistral-embed", 0.0001, 0.0},
	}

	p := mistral.NewProvider("test-key", "", 10*time.Second)
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
	p := mistral.NewProvider("test-key", "", 10*time.Second)
	if !p.SupportsModel("mistral-large-latest") {
		t.Fatal("expected SupportsModel(mistral-large-latest) = true")
	}
}

func str(s string) *string { return &s }
