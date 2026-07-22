package anthropic_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/anthropic"
)

func TestChatCompletionTransformsToMessagesAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key = %q, want test-key", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("anthropic-version = %q, want 2023-06-01", got)
		}

		var req messagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "claude-3-5-sonnet-20241022" {
			t.Fatalf("model = %q", req.Model)
		}
		if req.MaxTokens != 1024 {
			t.Fatalf("max_tokens = %d, want 1024", req.MaxTokens)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("messages = %v", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(messagesResponse{
			ID:         "msg_1",
			Type:       "message",
			Role:       "assistant",
			Model:      req.Model,
			StopReason: "end_turn",
			Content:    []contentBlock{{Type: "text", Text: "Hello from Claude"}},
			Usage:      usage{InputTokens: 10, OutputTokens: 5},
		})
	}))
	defer server.Close()

	p := anthropic.NewProvider("test-key", server.URL, 10*time.Second)
	maxTokens := 1024
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:     "claude-3-5-sonnet-20241022",
		MaxTokens: &maxTokens,
		Messages: []apitypes.Message{
			{Role: "user", Content: "Hello!"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Choices[0].Message.Content != "Hello from Claude" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
	}
}

func TestEmbeddingsNotSupported(t *testing.T) {
	p := anthropic.NewProvider("test-key", "https://api.anthropic.com/v1", 10*time.Second)
	_, err := p.Embeddings(context.Background(), &apitypes.EmbeddingRequest{
		Model: "claude-embed",
		Input: "hello",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	provErr, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if provErr.Type != provider.ErrorTypeInvalidRequest {
		t.Fatalf("error type = %q, want invalid_request", provErr.Type)
	}
}

func TestListModelsReturnsStaticCatalog(t *testing.T) {
	p := anthropic.NewProvider("test-key", "https://api.anthropic.com/v1", 10*time.Second)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected models")
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

	p := anthropic.NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "claude-3-5-sonnet-20241022",
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

type messagesRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type messagesResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Content    []contentBlock `json:"content"`
	Usage      usage          `json:"usage"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
