package openrouter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/provider/openrouter"
)

func TestChatCompletionForwardsOpenAIRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req apitypes.ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
			Model: req.Model,
			Choices: []apitypes.Choice{
				{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}, FinishReason: str("stop")},
			},
			Usage: &apitypes.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
	}))
	defer server.Close()

	p := openrouter.NewProvider("key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "openai/gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Model != "openai/gpt-4o" {
		t.Fatalf("model = %q", resp.Model)
	}
}

func str(s string) *string { return &s }
