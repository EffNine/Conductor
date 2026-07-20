package openaibase_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/provider/openaibase"
)

func TestChatCompletionPromotesReasoningToContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Mimic OpenCode Zen / Xiaomi mimo: content null, text in reasoning.
		_, _ = w.Write([]byte(`{
			"id":"gen-1","object":"chat.completion","created":1,"model":"xiaomi/mimo-v2.5",
			"choices":[{
				"index":0,
				"finish_reason":"stop",
				"message":{"role":"assistant","content":null,"reasoning":"Hello"}
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	defer server.Close()

	p := openaibase.New("opencode", "key", server.URL, 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "big-pickle",
		Messages: []apitypes.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Choices[0].Message.Content != "Hello" {
		t.Fatalf("content = %q, want Hello", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].Message.Reasoning != "Hello" {
		t.Fatalf("reasoning = %q, want Hello", resp.Choices[0].Message.Reasoning)
	}
}

func TestChatCompletionStreamSkipsKeepalivesAndKeepsReasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		fmt.Fprintf(w, ": OPENROUTER PROCESSING\n\n")
		flusher.Flush()
		fmt.Fprintf(w, ": OPENROUTER PROCESSING\n\n")
		flusher.Flush()
		chunk := map[string]any{
			"id":      "gen-stream",
			"object":  "chat.completion.chunk",
			"created": 1,
			"model":   "xiaomi/mimo-v2.5",
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{
					"role":      "assistant",
					"content":   "",
					"reasoning": "Hi",
				},
				"finish_reason": nil,
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	p := openaibase.New("opencode", "key", server.URL, 10*time.Second)
	ch, err := p.ChatCompletionStream(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "big-pickle",
		Messages: []apitypes.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var reasoning []string
	var contents []string
	var sawDone bool
	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("stream error: %v", chunk.Error)
		}
		if chunk.Done {
			sawDone = true
			break
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			contents = append(contents, chunk.Choices[0].Delta.Content)
			reasoning = append(reasoning, chunk.Choices[0].Delta.Reasoning)
		}
	}
	if !sawDone {
		t.Fatal("expected [DONE]")
	}
	if strings.Join(contents, "") != "" {
		t.Fatalf("content should stay empty on deltas, got %q", strings.Join(contents, ""))
	}
	if strings.Join(reasoning, "") != "Hi" {
		t.Fatalf("reasoning = %q, want Hi", strings.Join(reasoning, ""))
	}
}

func TestChatCompletionForwardsReasoningEffort(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"gen-1","object":"chat.completion","created":1,"model":"xiaomi/mimo-v2.5",
			"choices":[{"index":0,"finish_reason":"stop",
				"message":{"role":"assistant","content":"4","reasoning":"simple math"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":8,"total_tokens":18,
				"completion_tokens_details":{"reasoning_tokens":5}}
		}`))
	}))
	defer server.Close()

	p := openaibase.New("opencode", "key", server.URL, 10*time.Second)
	include := true
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:            "big-pickle",
		Messages:         []apitypes.Message{{Role: "user", Content: "2+2?"}},
		ReasoningEffort:  "low",
		IncludeReasoning: &include,
		Reasoning:        &apitypes.ReasoningConfig{Effort: "low"},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if gotBody["reasoning_effort"] != "low" {
		t.Fatalf("upstream reasoning_effort = %v", gotBody["reasoning_effort"])
	}
	reasoning, ok := gotBody["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "low" {
		t.Fatalf("upstream reasoning = %v", gotBody["reasoning"])
	}
	if gotBody["include_reasoning"] != true {
		t.Fatalf("upstream include_reasoning = %v", gotBody["include_reasoning"])
	}
	if resp.Choices[0].Message.Reasoning != "simple math" {
		t.Fatalf("reasoning = %q", resp.Choices[0].Message.Reasoning)
	}
	if resp.Usage == nil || resp.Usage.CompletionTokensDetails == nil ||
		resp.Usage.CompletionTokensDetails.ReasoningTokens != 5 {
		t.Fatalf("usage details = %+v", resp.Usage)
	}
}

func TestChatCompletionStreamRequestsIncludeUsage(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	p := openaibase.New("nvidia_nim", "key", server.URL, 10*time.Second)
	ch, err := p.ChatCompletionStream(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "nvidia/nemotron-3-ultra-550b-a55b",
		Messages: []apitypes.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	opts, ok := gotBody["stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != true {
		t.Fatalf("stream_options = %v, want include_usage=true", gotBody["stream_options"])
	}

	var usage *apitypes.Usage
	var content string
	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("stream error: %v", chunk.Error)
		}
		if chunk.Done {
			break
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			content += chunk.Choices[0].Delta.Content
		}
	}
	if content != "Hi" {
		t.Fatalf("content = %q", content)
	}
	if usage == nil || usage.CompletionTokens != 2 {
		t.Fatalf("usage = %+v, want completion_tokens=2", usage)
	}
}
