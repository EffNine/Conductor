package requesty_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/requesty"
)

var _ provider.Provider = (*requesty.Provider)(nil)

func TestProviderDefaults(t *testing.T) {
	p := requesty.NewProvider("test-key", "", 10*time.Second)
	if p.Name() != "requesty" {
		t.Fatalf("Name() = %q, want requesty", p.Name())
	}
	if got := p.GetMetadata().BaseURL; got != "https://router.requesty.ai/v1" {
		t.Fatalf("default base URL = %q, want https://router.requesty.ai/v1", got)
	}

	custom := requesty.NewProvider("test-key", "http://example.com/v1", 10*time.Second)
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
		if req.Model != "openai/gpt-4o-mini" {
			t.Fatalf("model = %q, want openai/gpt-4o-mini", req.Model)
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
					Message:      &apitypes.Message{Role: "assistant", Content: "Hello from Requesty"},
					FinishReason: str("stop"),
				},
			},
			Usage: &apitypes.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		})
	}))
	defer server.Close()

	p := requesty.NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "openai/gpt-4o-mini",
		Messages: []apitypes.Message{{Role: "user", Content: "Hello!"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Model != "openai/gpt-4o-mini" {
		t.Fatalf("resp.Model = %q, want openai/gpt-4o-mini", resp.Model)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
	}
}

func TestChatCompletionReturnsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "invalid api key", "type": "authentication_error"},
		})
	}))
	defer server.Close()

	p := requesty.NewProvider("bad-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "openai/gpt-4o-mini",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	provErr, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if provErr.Type != provider.ErrorTypeAuthentication {
		t.Fatalf("error type = %q, want authentication_error", provErr.Type)
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
				{ID: "openai/gpt-4o-mini", Object: "model", OwnedBy: "openai"},
			},
		})
	}))
	defer server.Close()

	p := requesty.NewProvider("test-key", server.URL, 10*time.Second)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ProviderModelID != "openai/gpt-4o-mini" {
		t.Fatalf("models = %v", models)
	}
}

func str(s string) *string { return &s }
