package nvidianim_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/nvidianim"
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
		if req.Model != "meta/llama-3.1-70b-instruct" {
			t.Fatalf("model = %q, want meta/llama-3.1-70b-instruct", req.Model)
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
						Content: "Hello from NIM",
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

	p := nvidianim.NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "meta/llama-3.1-70b-instruct",
		Messages: []apitypes.Message{
			{Role: "user", Content: "Hello!"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Model != "meta/llama-3.1-70b-instruct" {
		t.Fatalf("resp.Model = %q", resp.Model)
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
				{ID: "meta/llama-3.1-70b-instruct", Object: "model", OwnedBy: "meta"},
			},
		})
	}))
	defer server.Close()

	p := nvidianim.NewProvider("test-key", server.URL, 10*time.Second)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ProviderModelID != "meta/llama-3.1-70b-instruct" {
		t.Fatalf("models = %v", models)
	}
}

func TestHealthCheckReportsUnhealthyOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid key"})
	}))
	defer server.Close()

	p := nvidianim.NewProvider("bad-key", server.URL, 10*time.Second)
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
			Model: "nvidia/nv-embed-v2",
			Usage: &apitypes.Usage{PromptTokens: 4, TotalTokens: 4},
		})
	}))
	defer server.Close()

	p := nvidianim.NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.Embeddings(context.Background(), &apitypes.EmbeddingRequest{
		Model: "nvidia/nv-embed-v2",
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

	p := nvidianim.NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "meta/llama-3.1-70b-instruct",
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

func TestIsThinkingModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"deepseek-ai/deepseek-v4-flash", true},
		{"deepseek-ai/deepseek-v3.2", true},
		{"deepseek-ai/deepseek-r1-distill-qwen-32b", true},
		{"z-ai/glm5", true},
		{"moonshotai/kimi-k2.6", true},
		{"qwen/qwen3-235b-a22b", true},
		{"qwen/qwen3-next-80b-a3b-thinking", true},
		{"qwen/qwen3-next-80b-a3b-instruct", false},
		{"nvidia/llama-3.3-nemotron-super-49b-v1.5", true},
		{"nvidia/nemotron-3-super-120b-a12b", true},
		{"nvidia/llama-3.1-nemotron-70b-reward", false},
		{"meta/llama-3.1-8b-instruct", false},
		{"minimaxai/minimax-m3", true},
		{"thinkingmachines/inkling", true},
		{"microsoft/phi-4-mini-flash-reasoning", true},
		{"mistralai/magistral-small-2506", true},
	}
	for _, tc := range cases {
		if got := nvidianim.IsThinkingModel(tc.model); got != tc.want {
			t.Errorf("IsThinkingModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestChatCompletionInjectsThinkingKwargsByFamily(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		effort string
		check  func(t *testing.T, body map[string]any)
	}{
		{
			name:  "deepseek-v4",
			model: "deepseek-ai/deepseek-v4-flash",
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				if body["reasoning_effort"] != "high" {
					t.Fatalf("reasoning_effort = %v", body["reasoning_effort"])
				}
				kwargs := mustKwargs(t, body)
				if kwargs["thinking"] != true || kwargs["reasoning_effort"] != "high" {
					t.Fatalf("kwargs = %v", kwargs)
				}
			},
		},
		{
			name:  "deepseek-v3",
			model: "deepseek-ai/deepseek-v3.2",
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				kwargs := mustKwargs(t, body)
				if kwargs["thinking"] != true {
					t.Fatalf("kwargs = %v", kwargs)
				}
				if _, ok := kwargs["reasoning_effort"]; ok {
					t.Fatalf("v3 should not set kwargs.reasoning_effort: %v", kwargs)
				}
			},
		},
		{
			name:  "glm5",
			model: "z-ai/glm5",
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				kwargs := mustKwargs(t, body)
				if kwargs["enable_thinking"] != true || kwargs["clear_thinking"] != false {
					t.Fatalf("kwargs = %v", kwargs)
				}
			},
		},
		{
			name:  "kimi",
			model: "moonshotai/kimi-k2.6",
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				kwargs := mustKwargs(t, body)
				if kwargs["thinking"] != true {
					t.Fatalf("kwargs = %v", kwargs)
				}
			},
		},
		{
			name:  "qwen3-thinking",
			model: "qwen/qwen3-next-80b-a3b-thinking",
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				kwargs := mustKwargs(t, body)
				if kwargs["enable_thinking"] != true {
					t.Fatalf("kwargs = %v", kwargs)
				}
			},
		},
		{
			name:  "nemotron-super",
			model: "nvidia/llama-3.3-nemotron-super-49b-v1.5",
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				kwargs := mustKwargs(t, body)
				if kwargs["thinking"] != true {
					t.Fatalf("kwargs = %v", kwargs)
				}
			},
		},
		{
			name:  "nemotron-3",
			model: "nvidia/nemotron-3-super-120b-a12b",
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				kwargs := mustKwargs(t, body)
				if kwargs["enable_thinking"] != true {
					t.Fatalf("kwargs = %v", kwargs)
				}
			},
		},
		{
			name:   "minimax-disabled",
			model:  "minimaxai/minimax-m3",
			effort: "none",
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				kwargs := mustKwargs(t, body)
				if kwargs["thinking_mode"] != "disabled" {
					t.Fatalf("kwargs = %v", kwargs)
				}
			},
		},
		{
			name:  "inkling",
			model: "thinkingmachines/inkling",
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				kwargs := mustKwargs(t, body)
				if kwargs["reasoning_effort"] != "high" {
					t.Fatalf("kwargs = %v", kwargs)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&gotBody)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
					ID: "1", Object: "chat.completion", Model: tc.model,
					Choices: []apitypes.Choice{{
						Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}, FinishReason: str("stop"),
					}},
				})
			}))
			defer server.Close()

			p := nvidianim.NewProvider("test-key", server.URL, 10*time.Second)
			req := &apitypes.ChatCompletionRequest{
				Model:           tc.model,
				Messages:        []apitypes.Message{{Role: "user", Content: "hi"}},
				ReasoningEffort: tc.effort,
			}
			if _, err := p.ChatCompletion(context.Background(), req); err != nil {
				t.Fatalf("ChatCompletion: %v", err)
			}
			tc.check(t, gotBody)
		})
	}
}

func TestChatCompletionDeepSeekV4RespectsNoneEffort(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
			ID: "1", Object: "chat.completion", Model: "deepseek-ai/deepseek-v4-pro",
			Choices: []apitypes.Choice{{
				Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}, FinishReason: str("stop"),
			}},
		})
	}))
	defer server.Close()

	p := nvidianim.NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:           "deepseek-ai/deepseek-v4-pro",
		Messages:        []apitypes.Message{{Role: "user", Content: "hi"}},
		ReasoningEffort: "none",
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	kwargs, _ := gotBody["chat_template_kwargs"].(map[string]any)
	if kwargs["thinking"] != false {
		t.Fatalf("thinking = %v, want false", kwargs["thinking"])
	}
	if _, ok := kwargs["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be omitted for none, got %v", kwargs["reasoning_effort"])
	}
}

func TestChatCompletionDeepSeekV4PreservesClientKwargs(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
			ID: "1", Object: "chat.completion", Model: "deepseek-ai/deepseek-v4-flash",
			Choices: []apitypes.Choice{{
				Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}, FinishReason: str("stop"),
			}},
		})
	}))
	defer server.Close()

	p := nvidianim.NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "deepseek-ai/deepseek-v4-flash",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
		ChatTemplateKwargs: map[string]any{
			"thinking":         true,
			"reasoning_effort": "max",
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	kwargs, _ := gotBody["chat_template_kwargs"].(map[string]any)
	if kwargs["reasoning_effort"] != "max" {
		t.Fatalf("kwargs.reasoning_effort = %v, want max", kwargs["reasoning_effort"])
	}
}

func TestChatCompletionRemapsDeveloperRole(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
			ID: "1", Object: "chat.completion", Model: "meta/llama-3.1-8b-instruct",
			Choices: []apitypes.Choice{{
				Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}, FinishReason: str("stop"),
			}},
		})
	}))
	defer server.Close()

	p := nvidianim.NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "meta/llama-3.1-8b-instruct",
		Messages: []apitypes.Message{
			{Role: "developer", Content: "be brief"},
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", msgs)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("role = %v, want system", first["role"])
	}
	if _, ok := gotBody["chat_template_kwargs"]; ok {
		t.Fatalf("non-thinking model should not get chat_template_kwargs: %v", gotBody["chat_template_kwargs"])
	}
}

func TestChatCompletionSkipsInstructOnlyQwen3(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
			ID: "1", Object: "chat.completion", Model: "qwen/qwen3-next-80b-a3b-instruct",
			Choices: []apitypes.Choice{{
				Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}, FinishReason: str("stop"),
			}},
		})
	}))
	defer server.Close()

	p := nvidianim.NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "qwen/qwen3-next-80b-a3b-instruct",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if _, ok := gotBody["chat_template_kwargs"]; ok {
		t.Fatalf("qwen3 instruct-only should not get kwargs: %v", gotBody["chat_template_kwargs"])
	}
}

func TestChatCompletionStreamInjectsDeepSeekV4ChatTemplateKwargs(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := nvidianim.NewProvider("test-key", server.URL, 10*time.Second)
	ch, err := p.ChatCompletionStream(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "deepseek-ai/deepseek-v4-flash",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	for range ch {
	}
	kwargs, ok := gotBody["chat_template_kwargs"].(map[string]any)
	if !ok || kwargs["thinking"] != true {
		t.Fatalf("stream chat_template_kwargs = %v", gotBody["chat_template_kwargs"])
	}
}

func mustKwargs(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	kwargs, ok := body["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("chat_template_kwargs missing: %v", body)
	}
	return kwargs
}

func str(s string) *string { return &s }
