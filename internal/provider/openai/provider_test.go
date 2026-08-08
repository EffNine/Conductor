package openai

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
	p := NewProvider("test-key", "https://api.openai.com", 30*time.Second)
	if p.Name() != "openai" {
		t.Fatalf("Name() = %q, want openai", p.Name())
	}
	meta := p.GetMetadata()
	if meta.DisplayName != "OpenAI" {
		t.Fatalf("DisplayName = %q, want OpenAI", meta.DisplayName)
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
	if !meta.Capabilities.Structured {
		t.Fatal("Structured capability missing")
	}
}

func TestChatCompletionBasic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}

		var req openaiChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "gpt-4o" {
			t.Fatalf("model = %q, want gpt-4o", req.Model)
		}
		if len(req.Messages) != 1 {
			t.Fatalf("messages len = %d, want 1", len(req.Messages))
		}
		if req.Messages[0].Role != "user" {
			t.Fatalf("message role = %q, want user", req.Messages[0].Role)
		}
		if req.Messages[0].Content != "Hello!" {
			t.Fatalf("content = %q, want Hello!", req.Messages[0].Content)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_123",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-4o",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "Hello from OpenAI"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "Hello!"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Choices[0].Message.Content != "Hello from OpenAI" {
		t.Fatalf("content = %q, want Hello from OpenAI", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
	}
	if *resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop", *resp.Choices[0].FinishReason)
	}
}

func TestChatCompletionSystemAndDeveloperMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Messages) != 2 {
			t.Fatalf("messages len = %d, want 2", len(req.Messages))
		}
		if req.Messages[0].Role != "system" || req.Messages[0].Content != "You are helpful." {
			t.Fatalf("system message = %+v, want role system", req.Messages[0])
		}
		// A canonical developer role maps to the OpenAI system role.
		if req.Messages[1].Role != "system" || req.Messages[1].Content != "Be concise." {
			t.Fatalf("developer message = %+v, want role system", req.Messages[1])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_sys",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-4o",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "OK"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 1, "total_tokens": 4},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "developer", Content: "Be concise."},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
}

func TestChatCompletionMultimodalContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type     string `json:"type"`
					Text     string `json:"text,omitempty"`
					ImageURL *struct {
						URL    string `json:"url"`
						Detail string `json:"detail,omitempty"`
					} `json:"image_url,omitempty"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Messages) != 1 {
			t.Fatalf("messages len = %d, want 1", len(req.Messages))
		}
		parts := req.Messages[0].Content
		if len(parts) != 2 {
			t.Fatalf("content parts = %d, want 2", len(parts))
		}
		if parts[0].Type != "image_url" || parts[0].ImageURL == nil || parts[0].ImageURL.URL != "https://example.com/cat.png" {
			t.Fatalf("unexpected image part: %+v", parts[0])
		}
		if parts[1].Type != "text" || parts[1].Text != "What is this?" {
			t.Fatalf("unexpected text part: %+v", parts[1])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_vis",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-4o",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "A cat"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 3, "total_tokens": 23},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
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
				Type     string `json:"type"`
				Function struct {
					Name        string                 `json:"name"`
					Description string                 `json:"description"`
					Parameters  map[string]interface{} `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Tools) != 1 {
			t.Fatalf("tools len = %d, want 1", len(req.Tools))
		}
		if req.Tools[0].Type != "function" {
			t.Fatalf("tool type = %q, want function", req.Tools[0].Type)
		}
		if req.Tools[0].Function.Name != "get_weather" {
			t.Fatalf("tool name = %q, want get_weather", req.Tools[0].Function.Name)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_tool",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-4o",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Let me check",
					"tool_calls": []map[string]any{{
						"id":       "call_01",
						"type":     "function",
						"function": map[string]any{"name": "get_weather", "arguments": `{"city":"London"}`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"prompt_tokens": 15, "completion_tokens": 8, "total_tokens": 23},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "Weather in London?"}},
		Tools: []apitypes.Tool{{
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
		}},
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
	tc := resp.Choices[0].Message.ToolCalls[0]
	if tc.ID != "call_01" || tc.Function.Name != "get_weather" || tc.Function.Arguments != `{"city":"London"}` {
		t.Fatalf("unexpected tool call: %+v", tc)
	}
}

func TestChatCompletionParallelToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_par",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-4o",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": nil,
					"tool_calls": []map[string]any{
						{"id": "call_01", "type": "function", "function": map[string]any{"name": "get_weather", "arguments": `{"city":"London"}`}},
						{"id": "call_02", "type": "function", "function": map[string]any{"name": "get_time", "arguments": `{"city":"London"}`}},
					},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"prompt_tokens": 15, "completion_tokens": 12, "total_tokens": 27},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "Weather and time in London?"}},
		Tools: []apitypes.Tool{
			{Type: "function", Function: apitypes.FunctionDef{Name: "get_weather"}},
			{Type: "function", Function: apitypes.FunctionDef{Name: "get_time"}},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	calls := resp.Choices[0].Message.ToolCalls
	if len(calls) != 2 {
		t.Fatalf("tool_calls len = %d, want 2", len(calls))
	}
	if calls[0].ID != "call_01" || calls[0].Function.Name != "get_weather" {
		t.Fatalf("call[0] = %+v", calls[0])
	}
	if calls[1].ID != "call_02" || calls[1].Function.Name != "get_time" {
		t.Fatalf("call[1] = %+v", calls[1])
	}
	if resp.Choices[0].Message.Content != "" {
		if s, ok := resp.Choices[0].Message.Content.(string); ok {
			t.Fatalf("expected empty content for tool-only response, got %q", s)
		}
	}
}

func TestChatCompletionToolChoiceAndResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ToolChoice interface{} `json:"tool_choice"`
			Messages   []struct {
				Role        string `json:"role"`
				Content     string `json:"content"`
				ToolCallID  string `json:"tool_call_id"`
				ToolCalls   []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		tc, ok := req.ToolChoice.(map[string]interface{})
		if !ok {
			t.Fatalf("tool_choice should be map, got %T", req.ToolChoice)
		}
		if tc["type"] != "function" {
			t.Fatalf("tool_choice type = %v, want function", tc["type"])
		}
		// The third message must carry the tool result.
		if len(req.Messages) != 3 {
			t.Fatalf("messages len = %d, want 3", len(req.Messages))
		}
		last := req.Messages[2]
		if last.Role != "tool" || last.ToolCallID != "call_01" || last.Content != "Sunny, 20C" {
			t.Fatalf("tool result message = %+v", last)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_tr",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-4o",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "It is sunny"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 5, "total_tokens": 35},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:      "gpt-4o",
		ToolChoice: map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "get_weather"}},
		Messages: []apitypes.Message{
			{Role: "user", Content: "Weather in London?"},
			{Role: "assistant", ToolCalls: []apitypes.ToolCall{{
				ID: "call_01", Type: "function",
				Function: apitypes.FunctionCall{Name: "get_weather", Arguments: `{"city":"London"}`},
			}}},
			{Role: "tool", ToolCallID: "call_01", Content: "Sunny, 20C"},
		},
		Tools: []apitypes.Tool{{
			Type: "function",
			Function: apitypes.FunctionDef{
				Name: "get_weather",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"city": map[string]interface{}{"type": "string"},
					},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
}

func TestChatCompletionStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ResponseFormat map[string]interface{} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.ResponseFormat == nil || req.ResponseFormat["type"] != "json_schema" {
			t.Fatalf("response_format = %+v, want json_schema", req.ResponseFormat)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_json",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-4o",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": `{"name":"Conductor","version":1}`},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 6, "total_tokens": 18},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "Return a JSON object"}},
		ResponseFormat: map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name": "answer",
				"strict": true,
				"schema": map[string]interface{}{"type": "object"},
			},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Choices[0].Message.Content != `{"name":"Conductor","version":1}` {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
}

func TestChatCompletionReasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ReasoningEffort string `json:"reasoning_effort"`
			Reasoning       map[string]interface{} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		// The shorthand reasoning_effort stays empty when the canonical
		// ReasoningConfig is used; the effort travels in the reasoning map.
		if req.Reasoning == nil || req.Reasoning["effort"] != "medium" {
			t.Fatalf("reasoning = %+v, want effort medium", req.Reasoning)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_reason",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "o3",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":             "assistant",
					"content":          "The answer is 42",
					"reasoning":        "Let me think carefully.",
					"reasoning_content": "Let me think carefully.",
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 8, "total_tokens": 18},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "o3",
		Messages: []apitypes.Message{{Role: "user", Content: "Think hard"}},
		Reasoning: &apitypes.ReasoningConfig{
			Effort: "medium",
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	msg := resp.Choices[0].Message
	if msg.Reasoning != "Let me think carefully." && msg.ReasoningContent != "Let me think carefully." {
		t.Fatalf("expected reasoning content, got reasoning=%q reasoning_content=%q", msg.Reasoning, msg.ReasoningContent)
	}
	if msg.Content != "The answer is 42" {
		t.Fatalf("content = %q, want The answer is 42", msg.Content)
	}
}

func TestMapRequestBasic(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:      "gpt-4o",
		Temperature: float64Ptr(0.7),
		MaxTokens:  intPtr(1024),
		Messages:   []apitypes.Message{{Role: "user", Content: "Hi"}},
	}
	mapped := MapRequest(req)
	if mapped.Model != "gpt-4o" {
		t.Fatalf("model = %q", mapped.Model)
	}
	if mapped.Temperature == nil || *mapped.Temperature != 0.7 {
		t.Fatalf("temperature = %v, want 0.7", mapped.Temperature)
	}
	if mapped.MaxTokens == nil || *mapped.MaxTokens != 1024 {
		t.Fatalf("max_tokens = %v, want 1024", mapped.MaxTokens)
	}
}

func TestMapRequestDeveloperRoleBecomesSystem(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "developer", Content: "Be concise."}},
	}
	mapped := MapRequest(req)
	if len(mapped.Messages) != 1 {
		t.Fatalf("messages len = %d", len(mapped.Messages))
	}
	if mapped.Messages[0].Role != "system" {
		t.Fatalf("role = %q, want system", mapped.Messages[0].Role)
	}
}

func TestMapRequestToolChoiceString(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:      "gpt-4o",
		Messages:   []apitypes.Message{{Role: "user", Content: "Hi"}},
		ToolChoice: "get_weather",
	}
	mapped := MapRequest(req)
	tc, ok := mapped.ToolChoice.(map[string]interface{})
	if !ok {
		t.Fatalf("tool_choice should be map, got %T", mapped.ToolChoice)
	}
	if tc["type"] != "function" {
		t.Fatalf("tool_choice type = %v, want function", tc["type"])
	}
	fn, ok := tc["function"].(map[string]interface{})
	if !ok || fn["name"] != "get_weather" {
		t.Fatalf("tool_choice function = %v, want name get_weather", tc["function"])
	}
}

func TestMapRequestToolChoicePreserved(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:      "gpt-4o",
		Messages:   []apitypes.Message{{Role: "user", Content: "Hi"}},
		ToolChoice: "auto",
	}
	mapped := MapRequest(req)
	if mapped.ToolChoice != "auto" {
		t.Fatalf("tool_choice = %v, want auto", mapped.ToolChoice)
	}
}

func TestMapResponseStructuredUsageAndFinishReason(t *testing.T) {
	stop := "stop"
	usage := &openaiUsage{
		PromptTokens:     20,
		CompletionTokens: 7,
		TotalTokens:      27,
		PromptTokensDetails: &openaiTokenDetails{
			CachedTokens: 5,
		},
		CompletionTokensDetails: &openaiTokenDetails{
			ReasoningTokens: 3,
		},
	}
	resp := &openaiChatResponse{
		ID:      "chatcmpl_usage",
		Object:  "chat.completion",
		Created: 1700000000,
		Model:   "gpt-4o",
		Choices: []openaiChoice{{
			Index: 0,
			Message: openaiMessage{
				Role:    "assistant",
				Content: "Hello",
			},
			FinishReason: &stop,
		}},
		Usage: usage,
	}
	canonical := MapResponse("gpt-4o", resp)
	if canonical.Model != "gpt-4o" {
		t.Fatalf("model = %q, want gpt-4o", canonical.Model)
	}
	if *canonical.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop", *canonical.Choices[0].FinishReason)
	}
	if canonical.Usage.PromptTokens != 20 || canonical.Usage.CompletionTokens != 7 || canonical.Usage.TotalTokens != 27 {
		t.Fatalf("usage = %+v", canonical.Usage)
	}
	if canonical.Usage.PromptTokensDetails == nil || canonical.Usage.PromptTokensDetails.CachedTokens != 5 {
		t.Fatalf("prompt_tokens_details = %+v, want cached_tokens 5", canonical.Usage.PromptTokensDetails)
	}
	if canonical.Usage.CompletionTokensDetails == nil || canonical.Usage.CompletionTokensDetails.ReasoningTokens != 3 {
		t.Fatalf("completion_tokens_details = %+v, want reasoning_tokens 3", canonical.Usage.CompletionTokensDetails)
	}
}

func TestMapResponseToolCalls(t *testing.T) {
	toolCalls := []openaiToolCall{{
		ID:   "call_01",
		Type: "function",
		Function: openaiFunctionCall{
			Name:      "get_weather",
			Arguments: `{"city":"London"}`,
		},
	}}
	finish := "tool_calls"
	resp := &openaiChatResponse{
		ID:     "chatcmpl_tool",
		Object: "chat.completion",
		Choices: []openaiChoice{{
			Index: 0,
			Message: openaiMessage{
				Role:      "assistant",
				ToolCalls: toolCalls,
			},
			FinishReason: &finish,
		}},
		Usage: &openaiUsage{},
	}
	canonical := MapResponse("gpt-4o", resp)
	msg := canonical.Choices[0].Message
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_01" || tc.Function.Name != "get_weather" || tc.Function.Arguments != `{"city":"London"}` {
		t.Fatalf("unexpected tool call: %+v", tc)
	}
	if s, ok := msg.Content.(string); ok && s != "" {
		t.Fatalf("expected empty content, got %q", s)
	}
}

func TestStreamingTextChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
			`{"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
			`[DONE]`,
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
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "Hello!"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var text string
	var id, model string
	var finalUsage *apitypes.Usage
	var done, sawFinish bool
	for c := range ch {
		if c.Error != nil {
			t.Fatalf("stream error: %v", c.Error)
		}
		if c.Done {
			done = true
			continue
		}
		if c.ID != "" {
			id = c.ID
		}
		if c.Model != "" {
			model = c.Model
		}
		if c.Usage != nil {
			finalUsage = c.Usage
		}
		for _, choice := range c.Choices {
			if choice.FinishReason != nil {
				sawFinish = true
			}
			if s, ok := choice.Delta.Content.(string); ok {
				text += s
			}
		}
	}
	if !done {
		t.Fatal("expected done chunk")
	}
	if !sawFinish {
		t.Fatal("expected finish_reason chunk")
	}
	if text != "Hello world" {
		t.Fatalf("text = %q, want Hello world", text)
	}
	if id != "chatcmpl_stream" {
		t.Fatalf("id = %q, want chatcmpl_stream", id)
	}
	if model != "gpt-4o" {
		t.Fatalf("model = %q, want gpt-4o", model)
	}
	if finalUsage == nil || finalUsage.TotalTokens != 8 {
		t.Fatalf("final usage = %+v, want total_tokens 8", finalUsage)
	}
}

func TestStreamingParallelToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"id":"chatcmpl_tc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_01","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl_tc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ci"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl_tc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"Lond"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl_tc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"on\"}"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl_tc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":1,"id":"call_02","type":"function","function":{"name":"get_time","arguments":""}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl_tc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"ci"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl_tc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"ty\":\"Lond"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl_tc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"on\"}"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl_tc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
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
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "Weather and time in London?"}},
		Stream:   true,
		Tools: []apitypes.Tool{
			{Type: "function", Function: apitypes.FunctionDef{Name: "get_weather"}},
			{Type: "function", Function: apitypes.FunctionDef{Name: "get_time"}},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	// Accumulate argument fragments grouped by the tool call they belong to.
	// Canonical ToolCall has no OpenAI stream index, so rely on the ordering:
	// a chunk that carries an ID/name starts the next call; fragments that
	// carry only arguments belong to the most recently started call.
	type call struct {
		id   string
		name string
		args string
	}
	var calls []call
	appendArgs := func(args string) {
		if len(calls) == 0 {
			calls = append(calls, call{})
		}
		calls[len(calls)-1].args += args
	}
	appendMeta := func(id, name string) {
		calls = append(calls, call{id: id, name: name})
	}
	var sawFinish, done bool
	for c := range ch {
		if c.Error != nil {
			t.Fatalf("stream error: %v", c.Error)
		}
		if c.Done {
			done = true
			continue
		}
		for _, choice := range c.Choices {
			if choice.FinishReason != nil && *choice.FinishReason == "tool_calls" {
				sawFinish = true
			}
			for _, tc := range choice.Delta.ToolCalls {
				if tc.ID != "" || tc.Function.Name != "" {
					appendMeta(tc.ID, tc.Function.Name)
				}
				if tc.Function.Arguments != "" {
					appendArgs(tc.Function.Arguments)
				}
			}
		}
	}
	if !done {
		t.Fatal("expected done chunk")
	}
	if !sawFinish {
		t.Fatal("expected tool_calls finish reason")
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].id != "call_01" || calls[0].name != "get_weather" {
		t.Fatalf("call[0] = %+v", calls[0])
	}
	if calls[1].id != "call_02" || calls[1].name != "get_time" {
		t.Fatalf("call[1] = %+v", calls[1])
	}
	if calls[0].args != `{"city":"London"}` {
		t.Fatalf("call[0] args = %q, want %q", calls[0].args, `{"city":"London"}`)
	}
	if calls[1].args != `{"city":"London"}` {
		t.Fatalf("call[1] args = %q, want %q", calls[1].args, `{"city":"London"}`)
	}
}

func TestMultiTurnAgentWorkflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		// If the conversation already contains a tool result, answer finally.
		for _, m := range req.Messages {
			if m.Role == "tool" && m.ToolCallID == "call_01" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":      "chatcmpl_multi",
					"object":  "chat.completion",
					"created": 1700000000,
					"model":   "gpt-4o",
					"choices": []map[string]any{{
						"index":         0,
						"message":       map[string]any{"role": "assistant", "content": "It's sunny and 20C in London"},
						"finish_reason": "stop",
					}},
					"usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 8, "total_tokens": 38},
				})
				return
			}
		}

		// Otherwise return a tool call for the assistant.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_multi_1",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-4o",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": nil,
					"tool_calls": []map[string]any{{
						"id":       "call_01",
						"type":     "function",
						"function": map[string]any{"name": "get_weather", "arguments": `{"city":"London"}`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"prompt_tokens": 15, "completion_tokens": 8, "total_tokens": 23},
		})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	tools := []apitypes.Tool{{
		Type: "function",
		Function: apitypes.FunctionDef{
			Name: "get_weather",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{"type": "string"},
				},
			},
		},
	}}

	resp1, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "Weather in London?"}},
		Tools:    tools,
	})
	if err != nil {
		t.Fatalf("first turn ChatCompletion: %v", err)
	}
	if len(resp1.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("first turn: expected 1 tool call, got %d", len(resp1.Choices[0].Message.ToolCalls))
	}

	resp2, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "Weather in London?"},
			{Role: "assistant", ToolCalls: []apitypes.ToolCall{{
				ID:       "call_01",
				Type:     "function",
				Function: apitypes.FunctionCall{Name: "get_weather", Arguments: `{"city":"London"}`},
			}}},
			{Role: "tool", ToolCallID: "call_01", Content: "Sunny, 20C"},
		},
		Tools: tools,
	})
	if err != nil {
		t.Fatalf("second turn ChatCompletion: %v", err)
	}
	if resp2.Choices[0].Message.Content != "It's sunny and 20C in London" {
		t.Fatalf("content = %q, want It's sunny and 20C in London", resp2.Choices[0].Message.Content)
	}
}

func TestChatCompletionAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","code":"invalid_api_key"}}`))
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
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

func TestChatCompletionRateLimitError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached","type":"rate_limit_error","code":"rate_limit_exceeded"}}`))
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
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

func TestChatCompletionContextLengthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"This model's maximum context length is 128000 tokens","type":"invalid_request_error","code":"context_length_exceeded"}}`))
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
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
	if provErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", provErr.StatusCode)
	}
}

func TestChatCompletionServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"Internal server error","type":"server_error","code":"server_error"}}`))
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	provErr, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if provErr.Type != provider.ErrorTypeServerError {
		t.Fatalf("error type = %q, want server_error", provErr.Type)
	}
}

func TestChatCompletionTransportTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "late"})
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, 100*time.Millisecond)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	provErr, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if provErr.Type != provider.ErrorTypeProviderUnavailable {
		t.Fatalf("error type = %q, want provider_unavailable", provErr.Type)
	}
}

func TestMapErrorRateLimit(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)),
	}
	err := MapError("openai", resp)
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
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Invalid API Key","type":"authentication_error","code":"invalid_api_key"}}`)),
	}
	err := MapError("openai", resp)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Type != provider.ErrorTypeAuthentication {
		t.Fatalf("error type = %q, want authentication", err.Type)
	}
}

func TestMapErrorContextLength(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"context length exceeded","code":"context_length_exceeded"}}`)),
	}
	err := MapError("openai", resp)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Type != provider.ErrorTypeContextLength {
		t.Fatalf("error type = %q, want context_length_exceeded", err.Type)
	}
}

func TestMapErrorServerError(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"boom","type":"server_error"}}`)),
	}
	err := MapError("openai", resp)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Type != provider.ErrorTypeServerError {
		t.Fatalf("error type = %q, want server_error", err.Type)
	}
}

func TestMapErrorOverloaded(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"The server is overloaded"}}`)),
	}
	err := MapError("openai", resp)
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
	if !isRetryableError(&provider.ProviderError{Type: provider.ErrorTypeProviderUnavailable}) {
		t.Fatal("provider unavailable should be retryable")
	}
	if isRetryableError(&provider.ProviderError{Type: provider.ErrorTypeAuthentication}) {
		t.Fatal("auth error should not be retryable")
	}
}

func TestBuildAuthHeader(t *testing.T) {
	if got := BuildAuthHeader("sk-123"); got != "Bearer sk-123" {
		t.Fatalf("auth header = %q, want Bearer sk-123", got)
	}
}

func TestParseAuthError(t *testing.T) {
	msg, code := ParseAuthError([]byte(`{"error":{"message":"Invalid API key","type":"invalid_request_error","code":"invalid_api_key"}}`))
	if msg != "Invalid API key" {
		t.Fatalf("message = %q", msg)
	}
	if code != "invalid_api_key" {
		t.Fatalf("code = %q", code)
	}
}

func TestMapStreamChunkPreservesOrder(t *testing.T) {
	reason := "tool_calls"
	in := &apitypes.StreamChunk{
		ID:      "chunk_1",
		Object:  "chat.completion.chunk",
		Created: 1700000000,
		Model:   "gpt-4o",
		Choices: []apitypes.Choice{{
			Index: 0,
			Delta: &apitypes.Message{
				Role:      "assistant",
				ToolCalls: []apitypes.ToolCall{
					{ID: "call_01", Type: "function", Function: apitypes.FunctionCall{Name: "get_weather", Arguments: `{"city":"London"}`}},
					{ID: "call_02", Type: "function", Function: apitypes.FunctionCall{Name: "get_time", Arguments: `{"city":"London"}`}},
				},
			},
			FinishReason: &reason,
		}},
	}
	out := MapStreamChunk(in)
	if len(out.Choices[0].Delta.ToolCalls) != 2 {
		t.Fatalf("tool_calls len = %d, want 2", len(out.Choices[0].Delta.ToolCalls))
	}
	if out.Choices[0].Delta.ToolCalls[0].ID != "call_01" {
		t.Fatalf("call[0] id = %q", out.Choices[0].Delta.ToolCalls[0].ID)
	}
	if out.Choices[0].Delta.ToolCalls[1].ID != "call_02" {
		t.Fatalf("call[1] id = %q", out.Choices[0].Delta.ToolCalls[1].ID)
	}
}

func float64Ptr(v float64) *float64 { return &v }

func intPtr(v int) *int { return &v }
