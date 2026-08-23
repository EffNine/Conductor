package kilocode_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/kilocode"
)

var _ provider.Provider = (*kilocode.Provider)(nil)

func TestProviderDefaults(t *testing.T) {
	p := kilocode.NewProvider("test-key", "", 10*time.Second)
	if p.Name() != "kilocode" {
		t.Fatalf("Name() = %q, want kilocode", p.Name())
	}
	if got := p.GetMetadata().BaseURL; got != "https://api.kilo.ai/api/gateway" {
		t.Fatalf("default base URL = %q, want https://api.kilo.ai/api/gateway", got)
	}

	custom := kilocode.NewProvider("test-key", "http://example.com/gateway", 10*time.Second)
	if got := custom.GetMetadata().BaseURL; got != "http://example.com/gateway" {
		t.Fatalf("base URL = %q, want http://example.com/gateway", got)
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
		if req.Model != "claude-sonnet-4-5" {
			t.Fatalf("model = %q, want claude-sonnet-4-5", req.Model)
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
					Message:      &apitypes.Message{Role: "assistant", Content: "Hello from KiloCode"},
					FinishReason: str("stop"),
				},
			},
			Usage: &apitypes.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		})
	}))
	defer server.Close()

	p := kilocode.NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []apitypes.Message{{Role: "user", Content: "Hello!"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Model != "claude-sonnet-4-5" {
		t.Fatalf("resp.Model = %q, want claude-sonnet-4-5", resp.Model)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
	}
}

func TestListModelsSendsBearerAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apitypes.ModelList{
			Object: "list",
			Data: []apitypes.ModelInfo{
				{ID: "claude-sonnet-4-5", Object: "model", OwnedBy: "anthropic"},
			},
		})
	}))
	defer server.Close()

	p := kilocode.NewProvider("test-key", server.URL, 10*time.Second)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ProviderModelID != "claude-sonnet-4-5" {
		t.Fatalf("models = %v", models)
	}
}

func TestHealthCheckReportsUnhealthyOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid key"})
	}))
	defer server.Close()

	p := kilocode.NewProvider("bad-key", server.URL, 10*time.Second)
	status, err := p.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if status.IsHealthy {
		t.Fatal("expected unhealthy")
	}
}

func str(s string) *string { return &s }
