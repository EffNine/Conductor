package agnesai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/agnesai"
)

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
		if req.Model != "agnes-model" {
			t.Fatalf("model = %q, want agnes-model", req.Model)
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
					Index: 0,
					Message: &apitypes.Message{
						Role:    "assistant",
						Content: "Hello from Agnes AI",
					},
					FinishReason: str("stop"),
				},
			},
			Usage: &apitypes.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		})
	}))
	defer server.Close()

	p := agnesai.NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "agnes-model",
		Messages: []apitypes.Message{
			{Role: "user", Content: "Hello!"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Model != "agnes-model" {
		t.Fatalf("resp.Model = %q, want agnes-model", resp.Model)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
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
				{ID: "agnes-model", Object: "model", OwnedBy: "agnes"},
			},
		})
	}))
	defer server.Close()

	p := agnesai.NewProvider("test-key", server.URL, 10*time.Second)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ProviderModelID != "agnes-model" {
		t.Fatalf("models = %v", models)
	}
}

func TestHealthCheckReportsUnhealthyOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid key"})
	}))
	defer server.Close()

	p := agnesai.NewProvider("bad-key", server.URL, 10*time.Second)
	status, err := p.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if status.IsHealthy {
		t.Fatal("expected unhealthy")
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
			Model: "agnes-embed",
			Usage: &apitypes.Usage{PromptTokens: 4, TotalTokens: 4},
		})
	}))
	defer server.Close()

	p := agnesai.NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.Embeddings(context.Background(), &apitypes.EmbeddingRequest{
		Model: "agnes-embed",
		Input: "hello",
	})
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(resp.Data))
	}
}

func TestChatCompletionReturnsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "rate limit exceeded",
				"type":    "rate_limit_error",
			},
		})
	}))
	defer server.Close()

	p := agnesai.NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "agnes-model",
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
		t.Fatalf("error type = %q, want rate_limit", provErr.Type)
	}
}

func TestGetPricingReturnsEmptyMap(t *testing.T) {
	p := agnesai.NewProvider("test-key", "https://apihub.agnes-ai.com/v1", 10*time.Second)
	prices, err := p.GetPricing(context.Background())
	if err != nil {
		t.Fatalf("GetPricing: %v", err)
	}
	if len(prices) != 0 {
		t.Fatalf("expected empty pricing map, got %d entries", len(prices))
	}
}

func TestSupportsModelAcceptsAnyID(t *testing.T) {
	p := agnesai.NewProvider("test-key", "https://apihub.agnes-ai.com/v1", 10*time.Second)
	if !p.SupportsModel("agnes-model") {
		t.Fatal("expected SupportsModel(agnes-model) = true")
	}
}

func str(s string) *string { return &s }
