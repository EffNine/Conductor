package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
)

func TestNewProvider(t *testing.T) {
	p := NewProvider("test-key", "https://api.anthropic.com", 30*time.Second)
	if p.Name() != "anthropic" {
		t.Fatalf("Name() = %q, want anthropic", p.Name())
	}
	meta := p.GetMetadata()
	if meta.DisplayName != "Anthropic" {
		t.Fatalf("DisplayName = %q, want Anthropic", meta.DisplayName)
	}
	if !meta.Capabilities.Streaming {
		t.Fatal("Streaming capability missing")
	}
	if !meta.Capabilities.ToolCalling {
		t.Fatal("ToolCalling capability missing")
	}
	if !meta.Capabilities.Reasoning {
		t.Fatal("Reasoning capability missing")
	}
	if !meta.Capabilities.Images {
		t.Fatal("Images capability missing")
	}
}

func TestChatCompletionBasic(t *testing.T) {
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

		var req struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "claude-3-5-sonnet-20241022" {
			t.Fatalf("model = %q", req.Model)
		}
		if req.MaxTokens != 1024 {
			t.Fatalf("max_tokens = %d, want 1024", req.MaxTokens)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_123",
			"type":        "message",
			"role":        "assistant",
			"model":       req.Model,
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "Hello from Claude"}},
			"usage":       map[string]int{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
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
		t.Fatalf("content = %q, want Hello from Claude", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
	}
	if *resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop", *resp.Choices[0].FinishReason)
	}
}

func TestChatCompletionPreservesSystemPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			System string `json:"system"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.System != "You are a helpful assistant." {
			t.Fatalf("system = %q, want You are a helpful assistant.", req.System)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_sys",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-3-5-sonnet-20241022",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "OK"}},
			"usage":       map[string]int{"input_tokens": 5, "output_tokens": 2},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []apitypes.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
}

func TestChatCompletionPreservesMultimodalImages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Messages) != 1 {
			t.Fatalf("messages len = %d, want 1", len(req.Messages))
		}
		var blocks []struct {
			Type   string `json:"type"`
			Text   string `json:"text,omitempty"`
			Source *struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type,omitempty"`
				Data      string `json:"data,omitempty"`
				URL       string `json:"url,omitempty"`
			} `json:"source,omitempty"`
		}
		if err := json.Unmarshal(req.Messages[0].Content, &blocks); err != nil {
			t.Fatalf("content should be multimodal blocks: %v", err)
		}
		if len(blocks) != 2 {
			t.Fatalf("content blocks = %d, want 2", len(blocks))
		}
		if blocks[0].Type != "image" || blocks[0].Source == nil || blocks[0].Source.Type != "url" || blocks[0].Source.URL != "https://example.com/cat.png" {
			t.Fatalf("unexpected image block: %+v", blocks[0])
		}
		if blocks[1].Type != "text" || blocks[1].Text != "What is this?" {
			t.Fatalf("unexpected text block: %+v", blocks[1])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_vision",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-3-5-sonnet-20241022",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "A cat"}},
			"usage":       map[string]int{"input_tokens": 20, "output_tokens": 3},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/cat.png"}},
				{Type: apitypes.ContentPartText, Text: "What is this?"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
}

func TestChatCompletionWithTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []struct {
				Name        string                 `json:"name"`
				Description string                 `json:"description,omitempty"`
				InputSchema map[string]interface{} `json:"input_schema"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Tools) != 1 {
			t.Fatalf("tools len = %d, want 1", len(req.Tools))
		}
		if req.Tools[0].Name != "get_weather" {
			t.Fatalf("tool name = %q, want get_weather", req.Tools[0].Name)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_tool",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-3-5-sonnet-20241022",
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "text", "text": "Let me check"},
				{"type": "tool_use", "id": "toolu_01", "name": "get_weather", "input": map[string]any{"city": "London"}},
			},
			"usage": map[string]int{"input_tokens": 15, "output_tokens": 8},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	tools := []apitypes.Tool{{
		Type: "function",
		Function: apitypes.FunctionDef{
			Name:        "get_weather",
			Description: "Get weather for a city",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{"type": "string"},
				},
			},
		},
	}}
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Tools:    tools,
		Messages: []apitypes.Message{{Role: "user", Content: "Weather in London?"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if *resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", *resp.Choices[0].FinishReason)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(resp.Choices[0].Message.ToolCalls))
	}
	if resp.Choices[0].Message.ToolCalls[0].ID != "toolu_01" {
		t.Fatalf("tool_call id = %q, want toolu_01", resp.Choices[0].Message.ToolCalls[0].ID)
	}
	if resp.Choices[0].Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("tool_call name = %q, want get_weather", resp.Choices[0].Message.ToolCalls[0].Function.Name)
	}
}

func TestChatCompletionWithToolResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		var found bool
		for _, m := range req.Messages {
			if m.Role == "tool" {
				found = true
				var blocks []struct {
					Type      string `json:"type"`
					ToolUseID string `json:"tool_use_id"`
					Content   string `json:"content"`
				}
				if err := json.Unmarshal(m.Content, &blocks); err != nil {
					t.Fatalf("decode tool content: %v", err)
				}
				if len(blocks) != 1 || blocks[0].Type != "tool_result" {
					t.Fatalf("unexpected block type: %s", blocks[0].Type)
				}
				if blocks[0].ToolUseID != "toolu_01" {
					t.Fatalf("tool_use_id = %q, want toolu_01", blocks[0].ToolUseID)
				}
				if blocks[0].Content != "Sunny, 20C" {
					t.Fatalf("content = %q, want Sunny, 20C", blocks[0].Content)
				}
			}
		}
		if !found {
			t.Fatal("expected tool role message")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_tool_resp",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-3-5-sonnet-20241022",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "It's sunny in London"}},
			"usage":       map[string]int{"input_tokens": 25, "output_tokens": 6},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []apitypes.Message{
			{Role: "user", Content: "Weather in London?"},
			{Role: "assistant", ToolCalls: []apitypes.ToolCall{{
				ID:       "toolu_01",
				Type:     "function",
				Function: apitypes.FunctionCall{Name: "get_weather", Arguments: `{"city":"London"}`},
			}}},
			{Role: "tool", ToolCallID: "toolu_01", Content: "Sunny, 20C"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Choices[0].Message.Content != "It's sunny in London" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
}

func TestEmbeddingsNotSupported(t *testing.T) {
	p := NewProvider("test-key", "https://api.anthropic.com", 10*time.Second)
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
	p := NewProvider("test-key", "https://api.anthropic.com", 10*time.Second)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected models")
	}
	if models[0].ProviderModelID != "claude-3-7-sonnet-20250219" {
		t.Fatalf("first model = %q", models[0].ProviderModelID)
	}
}

func TestSupportsModel(t *testing.T) {
	p := NewProvider("test-key", "https://api.anthropic.com", 10*time.Second)
	if !p.SupportsModel("claude-3-5-sonnet-20241022") {
		t.Fatal("should support claude-3-5-sonnet")
	}
	if p.SupportsModel("unknown-model") {
		t.Fatal("should not support unknown model")
	}
}

func TestChatCompletionReturnsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "rate limit exceeded",
				"type":    "rate_limit_error",
				"code":    "rate_limit_error",
			},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
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

func TestChatCompletionAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Invalid API Key",
				"type":    "authentication_error",
				"code":    "authentication_error",
			},
		})
	}))
	defer server.Close()

	p := NewProvider("bad-key", server.URL, 10*time.Second)
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
	if provErr.Type != provider.ErrorTypeAuthentication {
		t.Fatalf("error type = %q, want authentication", provErr.Type)
	}
}

func TestStreamingBasic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"message_start","message":{"id":"msg_stream1","type":"message","role":"assistant","model":"claude-3-5-sonnet-20241022","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0},"created":1234567890}}`,
			`{"type":"content_block_start","index":0,"start":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":10,"output_tokens":5}}`,
			`{"type":"message_stop"}`,
		}
		for _, e := range events {
			_, _ = w.Write([]byte("data: " + e + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	ch, err := p.ChatCompletionStream(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []apitypes.Message{
			{Role: "user", Content: "Hello!"},
		},
		Stream: true,
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var chunks []apitypes.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	if len(chunks) == 0 {
		t.Fatal("expected stream chunks")
	}

	var textChunks int
	var done bool
	for _, c := range chunks {
		if c.Done {
			done = true
			continue
		}
		for _, choice := range c.Choices {
			if s, ok := choice.Delta.Content.(string); ok && s != "" {
				textChunks++
			}
		}
	}
	if textChunks < 2 {
		t.Fatalf("expected at least 2 text delta chunks, got %d", textChunks)
	}
	if !done {
		t.Fatal("expected done chunk")
	}
}

func TestStreamingWithToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"message_start","message":{"id":"msg_tool_stream","type":"message","role":"assistant","model":"claude-3-5-sonnet-20241022","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0},"created":1234567890}}`,
			`{"type":"content_block_start","index":0,"start":{"type":"tool_use","id":"toolu_stream1","name":"get_weather","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"ci"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"ty\":\"Lond"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"on\"}"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":10,"output_tokens":8}}`,
			`{"type":"message_stop"}`,
		}
		for _, e := range events {
			_, _ = w.Write([]byte("data: " + e + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	ch, err := p.ChatCompletionStream(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []apitypes.Message{
			{Role: "user", Content: "Weather in London?"},
		},
		Stream: true,
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var chunks []apitypes.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	var foundToolCall, foundFinishReason bool
	for _, c := range chunks {
		if c.Done {
			continue
		}
		for _, choice := range c.Choices {
			if choice.FinishReason != nil && *choice.FinishReason == "tool_calls" {
				foundFinishReason = true
			}
			for _, tc := range choice.Delta.ToolCalls {
				if tc.ID == "toolu_stream1" && tc.Function.Name == "get_weather" {
					foundToolCall = true
				}
			}
		}
	}
	if !foundToolCall {
		t.Fatal("expected tool call in stream")
	}
	if !foundFinishReason {
		t.Fatal("expected tool_calls finish reason in stream")
	}
}

func TestStreamingWithThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"message_start","message":{"id":"msg_think","type":"message","role":"assistant","model":"claude-3-7-sonnet-20250219","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0},"created":1234567890}}`,
			`{"type":"content_block_start","index":0,"start":{"type":"thinking","thinking":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"start":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Answer"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":10,"output_tokens":5}}`,
			`{"type":"message_stop"}`,
		}
		for _, e := range events {
			_, _ = w.Write([]byte("data: " + e + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	ch, err := p.ChatCompletionStream(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "claude-3-7-sonnet-20250219",
		Messages: []apitypes.Message{
			{Role: "user", Content: "Think about this"},
		},
		Stream: true,
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var chunks []apitypes.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	var hasReasoning bool
	for _, c := range chunks {
		if c.Done {
			continue
		}
		for _, choice := range c.Choices {
			if choice.Delta.Reasoning != "" {
				hasReasoning = true
			}
		}
	}
	if !hasReasoning {
		t.Fatal("expected reasoning content in stream")
	}
}

func TestMapRequestSystemPrompt(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []apitypes.Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hi"},
		},
	}
	mapped := MapRequest(req)
	if mapped.System != "You are helpful." {
		t.Fatalf("system = %q, want You are helpful.", mapped.System)
	}
	if len(mapped.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(mapped.Messages))
	}
	if mapped.Messages[0].Role != "user" {
		t.Fatalf("message role = %q, want user", mapped.Messages[0].Role)
	}
}

func TestMapRequestStopSequences(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model: "claude-3-5-sonnet-20241022",
		Stop:  []string{"\n\n", "USER:"},
		Messages: []apitypes.Message{
			{Role: "user", Content: "Hi"},
		},
	}
	mapped := MapRequest(req)
	if len(mapped.StopSequences) != 2 {
		t.Fatalf("stop_sequences len = %d, want 2", len(mapped.StopSequences))
	}
	if mapped.StopSequences[0] != "\n\n" {
		t.Fatalf("stop_sequences[0] = %q", mapped.StopSequences[0])
	}
}

func TestMapRequestToolChoice(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:      "claude-3-5-sonnet-20241022",
		Messages:   []apitypes.Message{{Role: "user", Content: "Hi"}},
		ToolChoice: map[string]interface{}{"type": "tool", "name": "get_weather"},
	}
	mapped := MapRequest(req)
	tc, ok := mapped.ToolChoice.(map[string]interface{})
	if !ok {
		t.Fatalf("tool_choice should be map, got %T", mapped.ToolChoice)
	}
	if tc["type"] != "tool" {
		t.Fatalf("tool_choice type = %v, want tool", tc["type"])
	}
	if tc["name"] != "get_weather" {
		t.Fatalf("tool_choice name = %v, want get_weather", tc["name"])
	}
}

func TestMapRequestReasoning(t *testing.T) {
	maxTokens := 1024
	req := &apitypes.ChatCompletionRequest{
		Model:     "claude-3-7-sonnet-20250219",
		Messages:  []apitypes.Message{{Role: "user", Content: "Think"}},
		Reasoning: &apitypes.ReasoningConfig{MaxTokens: &maxTokens},
	}
	mapped := MapRequest(req)
	if mapped.Thinking == nil {
		t.Fatal("expected thinking config")
	}
	if mapped.Thinking["budget_tokens"] != 1024 {
		t.Fatalf("budget_tokens = %v, want 1024", mapped.Thinking["budget_tokens"])
	}
}

func TestMapResponseWithToolCalls(t *testing.T) {
	resp := &anthropicMessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-3-5-sonnet-20241022",
		StopReason: "tool_use",
		Content: []anthropicContent{
			{Type: "text", Text: "Let me check"},
			{Type: "tool_use", ID: "toolu_01", Name: "get_weather", Input: map[string]any{"city": "London"}},
		},
		Usage: usage{InputTokens: 15, OutputTokens: 8},
	}
	canonical := MapResponse("claude-3-5-sonnet-20241022", resp)
	if canonical.Choices[0].Message.Content != "Let me check" {
		t.Fatalf("content = %q", canonical.Choices[0].Message.Content)
	}
	if len(canonical.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(canonical.Choices[0].Message.ToolCalls))
	}
	if canonical.Choices[0].Message.ToolCalls[0].ID != "toolu_01" {
		t.Fatalf("tool_call id = %q", canonical.Choices[0].Message.ToolCalls[0].ID)
	}
	if *canonical.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", *canonical.Choices[0].FinishReason)
	}
}

func TestMapResponseWithThinking(t *testing.T) {
	resp := &anthropicMessageResponse{
		ID:         "msg_think",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-3-7-sonnet-20250219",
		StopReason: "end_turn",
		Content: []anthropicContent{
			{Type: "thinking", Thinking: "Let me think about this..."},
			{Type: "text", Text: "The answer is 42"},
		},
		Usage: usage{InputTokens: 20, OutputTokens: 5},
	}
	canonical := MapResponse("claude-3-7-sonnet-20250219", resp)
	if canonical.Choices[0].Message.Reasoning != "Let me think about this..." {
		t.Fatalf("reasoning = %q", canonical.Choices[0].Message.Reasoning)
	}
	if canonical.Choices[0].Message.Content != "The answer is 42" {
		t.Fatalf("content = %q", canonical.Choices[0].Message.Content)
	}
}

func TestAuthHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "my-secret-key" {
			t.Fatalf("x-api-key = %q, want my-secret-key", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("anthropic-version = %q, want 2023-06-01", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_auth",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-3-5-sonnet-20241022",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"usage":       map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	p := NewProvider("my-secret-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
}

func TestRequestMapperDataURLEncoding(t *testing.T) {
	dataURL := "data:image/png;base64,iVBORw0KGgo="
	block := imageURLToAnthropicBlock(dataURL)
	if block.Type != "image" {
		t.Fatalf("block type = %q, want image", block.Type)
	}
	if block.Source == nil || block.Source.Type != "base64" {
		t.Fatalf("source type = %v, want base64", block.Source)
	}
	if block.Source.MediaType != "image/png" {
		t.Fatalf("media_type = %q, want image/png", block.Source.MediaType)
	}
	if block.Source.Data != "iVBORw0KGgo=" {
		t.Fatalf("data = %q, want iVBORw0KGgo=", block.Source.Data)
	}
}

func TestRequestMapperURLImage(t *testing.T) {
	block := imageURLToAnthropicBlock("https://example.com/cat.png")
	if block.Type != "image" {
		t.Fatalf("block type = %q, want image", block.Type)
	}
	if block.Source == nil || block.Source.Type != "url" {
		t.Fatalf("source type = %v, want url", block.Source)
	}
	if block.Source.URL != "https://example.com/cat.png" {
		t.Fatalf("url = %q, want https://example.com/cat.png", block.Source.URL)
	}
}

func TestMapErrorRateLimit(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limit exceeded","type":"rate_limit_error","code":"rate_limit_error"}}`)),
	}
	err := MapError("anthropic", resp)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Type != provider.ErrorTypeRateLimit {
		t.Fatalf("error type = %q, want rate_limit", err.Type)
	}
}

func TestMapErrorAuth(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Invalid API Key","type":"authentication_error","code":"authentication_error"}}`)),
	}
	err := MapError("anthropic", resp)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Type != provider.ErrorTypeAuthentication {
		t.Fatalf("error type = %q, want authentication", err.Type)
	}
}

func TestMapErrorOverloaded(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Service unavailable","type":"overloaded_error","code":"overloaded_error"}}`)),
	}
	err := MapError("anthropic", resp)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Type != provider.ErrorTypeProviderUnavailable {
		t.Fatalf("error type = %q, want provider_unavailable", err.Type)
	}
}

func TestValidateAuth(t *testing.T) {
	if ValidateAuth("") == nil {
		t.Fatal("expected error for empty key")
	}
	if ValidateAuth("some-key") != nil {
		t.Fatal("expected nil for valid key")
	}
}

func TestIsRetryableError(t *testing.T) {
	if isRetryableError(nil) {
		t.Fatal("nil error should not be retryable")
	}
	if !isRetryableError(&provider.ProviderError{Type: provider.ErrorTypeRateLimit}) {
		t.Fatal("rate limit should be retryable")
	}
	if !isRetryableError(&provider.ProviderError{Type: provider.ErrorTypeServerError}) {
		t.Fatal("server error should be retryable")
	}
	if isRetryableError(&provider.ProviderError{Type: provider.ErrorTypeAuthentication}) {
		t.Fatal("auth error should not be retryable")
	}
}

func TestIsContextExceeded(t *testing.T) {
	if !isAnthropicContextExceeded(&provider.ProviderError{Message: "Context length exceeded"}) {
		t.Fatal("should detect context length exceeded")
	}
	if !isAnthropicContextExceeded(&provider.ProviderError{Message: "too many tokens in context"}) {
		t.Fatal("should detect too many tokens")
	}
	if isAnthropicContextExceeded(&provider.ProviderError{Message: "rate limited"}) {
		t.Fatal("should not detect rate limit as context exceeded")
	}
}

func TestStreamAccumulator(t *testing.T) {
	accum := NewStreamAccumulator()
	if accum.GetText() != "" {
		t.Fatal("expected empty text")
	}
	if accum.GetThinking() != "" {
		t.Fatal("expected empty thinking")
	}
	accum.Reset()
	if accum.id != "" {
		t.Fatal("expected reset id to be empty")
	}
}
