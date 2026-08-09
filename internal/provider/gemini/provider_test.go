package gemini

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

// --- helpers ----------------------------------------------------------------

func newChatServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func newProvider(t *testing.T, srv *httptest.Server) *Provider {
	t.Helper()
	return NewProvider("test-key", srv.URL, 10*time.Second)
}

func writeJSON(t *testing.T, w http.ResponseWriter, v interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func writeSSE(t *testing.T, w http.ResponseWriter, events []string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		_, _ = w.Write([]byte("data: " + e + "\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func basicResponse(model string) map[string]interface{} {
	return map[string]interface{}{
		"candidates": []map[string]interface{}{{
			"content":      map[string]interface{}{"role": "model", "parts": []map[string]string{{"text": "Hello from Gemini"}}},
			"finishReason": "STOP",
			"index":        0,
		}},
		"usageMetadata": map[string]int{"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
		"modelVersion":  model,
	}
}

func chatReq(model string, messages ...apitypes.Message) *apitypes.ChatCompletionRequest {
	return &apitypes.ChatCompletionRequest{
		Model:    model,
		Messages: messages,
	}
}

func testTool() []apitypes.Tool {
	return []apitypes.Tool{{
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
}

func assertProviderErrorType(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	pe, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if pe.Type != want {
		t.Fatalf("error type = %q, want %q (msg=%s)", pe.Type, want, pe.Message)
	}
}

// --- 1. basic chat ----------------------------------------------------------

func TestChatCompletionBasic(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Fatalf("x-goog-api-key = %q, want test-key", got)
		}
		var req generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Contents[0].Role != "user" || req.Contents[0].Parts[0].Text != "Hello!" {
			t.Fatalf("unexpected contents: %+v", req.Contents)
		}
		writeJSON(t, w, basicResponse("gemini-2.5-flash"))
	})

	p := newProvider(t, srv)
	resp, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "Hello!"}))
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Choices[0].Message.Content != "Hello from Gemini" {
		t.Fatalf("content = %q, want Hello from Gemini", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
	}
	if *resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop", *resp.Choices[0].FinishReason)
	}
}

// --- 2. system instruction --------------------------------------------------

func TestChatCompletionSystemInstruction(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.SystemInstruction == nil {
			t.Fatal("expected systemInstruction")
		}
		if len(req.SystemInstruction.Parts) == 0 || req.SystemInstruction.Parts[0].Text != "You are a helpful assistant." {
			t.Fatalf("systemInstruction = %+v", req.SystemInstruction)
		}
		// System must not appear in contents.
		for _, c := range req.Contents {
			if c.Role == "system" {
				t.Fatalf("system message leaked into contents: %+v", req.Contents)
			}
		}
		writeJSON(t, w, basicResponse("gemini-2.5-flash"))
	})

	p := newProvider(t, srv)
	_, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "system", Content: "You are a helpful assistant."},
		apitypes.Message{Role: "user", Content: "Hi"}))
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
}

// --- 3. multimodal input ----------------------------------------------------

func TestChatCompletionMultimodalImage(t *testing.T) {
	dataURL := "data:image/png;base64,iVBORw0KGgo="
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Contents) != 1 {
			t.Fatalf("contents len = %d, want 1", len(req.Contents))
		}
		parts := req.Contents[0].Parts
		if len(parts) != 2 {
			t.Fatalf("parts len = %d, want 2", len(parts))
		}
		img := parts[0]
		if img.InlineData == nil {
			t.Fatalf("expected inlineData part, got %+v", img)
		}
		if img.InlineData.MimeType != "image/png" || img.InlineData.Data != "iVBORw0KGgo=" {
			t.Fatalf("inlineData = %+v", img.InlineData)
		}
		if parts[1].Text != "What is this?" {
			t.Fatalf("text part = %q", parts[1].Text)
		}
		writeJSON(t, w, basicResponse("gemini-2.5-flash"))
	})

	p := newProvider(t, srv)
	_, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash", apitypes.Message{
		Role: "user",
		Content: []apitypes.ContentPart{
			{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: dataURL}},
			{Type: apitypes.ContentPartText, Text: "What is this?"},
		},
	}))
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
}

// --- 4. single tool call ----------------------------------------------------

func TestChatCompletionSingleToolCall(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Tools) != 1 || len(req.Tools[0].FunctionDeclarations) != 1 {
			t.Fatalf("tools = %+v", req.Tools)
		}
		if req.Tools[0].FunctionDeclarations[0].Name != "get_weather" {
			t.Fatalf("tool name = %q, want get_weather", req.Tools[0].FunctionDeclarations[0].Name)
		}
		writeJSON(t, w, map[string]interface{}{
			"candidates": []map[string]interface{}{{
				"content": map[string]interface{}{"role": "model", "parts": []map[string]interface{}{
					{"functionCall": map[string]interface{}{"name": "get_weather", "args": map[string]string{"city": "London"}}},
				}},
				"finishReason": "STOP",
				"index":        0,
			}},
			"usageMetadata": map[string]int{"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
			"modelVersion":  "gemini-2.5-flash",
		})
	})

	p := newProvider(t, srv)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gemini-2.5-flash",
		Tools:    testTool(),
		Messages: []apitypes.Message{{Role: "user", Content: "Weather in London?"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(resp.Choices[0].Message.ToolCalls))
	}
	tc := resp.Choices[0].Message.ToolCalls[0]
	if tc.Function.Name != "get_weather" {
		t.Fatalf("tool name = %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"London"}` {
		t.Fatalf("arguments = %q", tc.Function.Arguments)
	}
	if tc.ID == "" {
		t.Fatal("expected synthesized tool call id")
	}
	if *resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", *resp.Choices[0].FinishReason)
	}
}

// --- 5. multiple tool calls -------------------------------------------------

func TestChatCompletionMultipleToolCalls(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]interface{}{
			"candidates": []map[string]interface{}{{
				"content": map[string]interface{}{"role": "model", "parts": []map[string]interface{}{
					{"functionCall": map[string]interface{}{"name": "get_weather", "args": map[string]string{"city": "London"}}},
					{"functionCall": map[string]interface{}{"name": "get_time", "args": map[string]string{"city": "London"}}},
				}},
				"finishReason": "STOP",
				"index":        0,
			}},
			"usageMetadata": map[string]int{"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
			"modelVersion":  "gemini-2.5-flash",
		})
	})

	p := newProvider(t, srv)
	resp, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "Weather and time in London?"}))
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	tcs := resp.Choices[0].Message.ToolCalls
	if len(tcs) != 2 {
		t.Fatalf("tool_calls len = %d, want 2", len(tcs))
	}
	if tcs[0].Function.Name != "get_weather" || tcs[1].Function.Name != "get_time" {
		t.Fatalf("tool order not preserved: %+v", tcs)
	}
	if tcs[0].ID == tcs[1].ID {
		t.Fatalf("tool call ids must differ: %q", tcs[0].ID)
	}
}

// --- 6. tool result ---------------------------------------------------------

func TestChatCompletionToolResult(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		// user -> model(functionCall) -> user(functionResponse)
		if len(req.Contents) != 3 {
			t.Fatalf("contents len = %d, want 3", len(req.Contents))
		}
		if req.Contents[1].Role != "model" {
			t.Fatalf("contents[1].role = %q, want model", req.Contents[1].Role)
		}
		if req.Contents[1].Parts[0].FunctionCall == nil || req.Contents[1].Parts[0].FunctionCall.Name != "get_weather" {
			t.Fatalf("contents[1].parts = %+v", req.Contents[1].Parts)
		}
		fr := req.Contents[2].Parts[0].FunctionResponse
		if fr == nil {
			t.Fatalf("expected functionResponse part, got %+v", req.Contents[2].Parts)
		}
		if fr.Name != "get_weather" {
			t.Fatalf("functionResponse.name = %q, want get_weather", fr.Name)
		}
		writeJSON(t, w, basicResponse("gemini-2.5-flash"))
	})

	p := newProvider(t, srv)
	resp, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "Weather in London?"},
		apitypes.Message{Role: "assistant", ToolCalls: []apitypes.ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: apitypes.FunctionCall{Name: "get_weather", Arguments: `{"city":"London"}`},
		}}},
		apitypes.Message{Role: "tool", ToolCallID: "call_1", Content: "Sunny, 20C"},
	))
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Choices[0].Message.Content != "Hello from Gemini" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
}

// --- 7. multi-turn tool workflow --------------------------------------------

func TestMultiTurnToolWorkflow(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		hasToolResult := false
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				if p.FunctionResponse != nil {
					hasToolResult = true
				}
			}
		}
		if hasToolResult {
			writeJSON(t, w, map[string]interface{}{
				"candidates": []map[string]interface{}{{
					"content":      map[string]interface{}{"role": "model", "parts": []map[string]string{{"text": "It's sunny in London"}}},
					"finishReason": "STOP",
					"index":        0,
				}},
				"usageMetadata": map[string]int{"promptTokenCount": 20, "candidatesTokenCount": 6, "totalTokenCount": 26},
				"modelVersion":  "gemini-2.5-flash",
			})
			return
		}
		writeJSON(t, w, map[string]interface{}{
			"candidates": []map[string]interface{}{{
				"content": map[string]interface{}{"role": "model", "parts": []map[string]interface{}{
					{"functionCall": map[string]interface{}{"name": "get_weather", "args": map[string]string{"city": "London"}}},
				}},
				"finishReason": "STOP",
				"index":        0,
			}},
			"usageMetadata": map[string]int{"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
			"modelVersion":  "gemini-2.5-flash",
		})
	})

	p := newProvider(t, srv)

	// Turn 1: model calls the tool.
	turn1, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gemini-2.5-flash",
		Tools:    testTool(),
		Messages: []apitypes.Message{{Role: "user", Content: "Weather in London?"}},
	})
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if len(turn1.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("turn 1: expected 1 tool call, got %d", len(turn1.Choices[0].Message.ToolCalls))
	}

	// Turn 2: tool result is fed back; model answers.
	turn2, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "gemini-2.5-flash",
		Tools: testTool(),
		Messages: []apitypes.Message{
			{Role: "user", Content: "Weather in London?"},
			{Role: "assistant", ToolCalls: turn1.Choices[0].Message.ToolCalls},
			{Role: "tool", ToolCallID: turn1.Choices[0].Message.ToolCalls[0].ID, Content: "Sunny, 20C"},
		},
	})
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if turn2.Choices[0].Message.Content != "It's sunny in London" {
		t.Fatalf("turn 2 content = %q, want It's sunny in London", turn2.Choices[0].Message.Content)
	}
}

// --- 8. streaming text ------------------------------------------------------

func TestStreamingText(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},"index":0}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":" world"}]},"index":0}]}`,
		`{"candidates":[{"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":4,"totalTokenCount":9}}`,
	}
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":streamGenerateContent") || !strings.Contains(r.URL.RawQuery, "alt=sse") {
			t.Fatalf("unexpected stream path: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		writeSSE(t, w, events)
	})

	p := newProvider(t, srv)
	ch, err := p.ChatCompletionStream(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "Hello!"}))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var text strings.Builder
	var done, sawFinish, sawUsage bool
	for c := range ch {
		if c.Done {
			done = true
			continue
		}
		if c.Error != nil {
			t.Fatalf("stream error: %v", c.Error)
		}
		if c.Usage != nil {
			sawUsage = true
			if c.Usage.TotalTokens != 9 {
				t.Fatalf("usage total = %d, want 9", c.Usage.TotalTokens)
			}
		}
		for _, choice := range c.Choices {
			if choice.FinishReason != nil && *choice.FinishReason == "stop" {
				sawFinish = true
			}
			if s, ok := choice.Delta.Content.(string); ok {
				text.WriteString(s)
			}
		}
	}
	if !done {
		t.Fatal("expected done chunk")
	}
	if text.String() != "Hello world" {
		t.Fatalf("streamed text = %q, want Hello world", text.String())
	}
	if !sawFinish {
		t.Fatal("expected finish reason in stream")
	}
	if !sawUsage {
		t.Fatal("expected usage in stream")
	}
}

// --- 9. streaming tool calls ------------------------------------------------

func TestStreamingToolCallsFragmented(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","willContinue":true}}]},"index":0}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","partialArgs":[{"jsonPath":"$.city","stringValue":"Lond","willContinue":true}],"willContinue":true}}]},"index":0}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"partialArgs":[{"jsonPath":"$.city","stringValue":"on","willContinue":true}],"willContinue":true}}]},"index":0}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{}}]},"index":0}]}`,
		`{"candidates":[{"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":8,"totalTokenCount":13}}`,
	}
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w, events)
	})

	p := newProvider(t, srv)
	ch, err := p.ChatCompletionStream(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "Weather?"}))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var foundCall *apitypes.ToolCall
	var foundFinish bool
	for c := range ch {
		if c.Done || c.Error != nil {
			continue
		}
		for _, choice := range c.Choices {
			if choice.FinishReason != nil && *choice.FinishReason == "tool_calls" {
				foundFinish = true
			}
			for _, tc := range choice.Delta.ToolCalls {
				if tc.Function.Name == "get_weather" {
					cc := tc
					foundCall = &cc
				}
			}
		}
	}
	if foundCall == nil {
		t.Fatal("expected accumulated tool call in stream")
	}
	if foundCall.Function.Arguments != `{"city":"London"}` {
		t.Fatalf("accumulated args = %q, want {\"city\":\"London\"}", foundCall.Function.Arguments)
	}
	if !foundFinish {
		t.Fatal("expected finish reason tool_calls in stream")
	}
}

// --- 10. reasoning/thinking -------------------------------------------------

func TestChatCompletionReasoningThoughtParts(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]interface{}{
			"candidates": []map[string]interface{}{{
				"content": map[string]interface{}{"role": "model", "parts": []map[string]interface{}{
					{"text": "Let me think", "thought": true},
					{"text": "The answer is 42"},
				}},
				"finishReason": "STOP",
				"index":        0,
			}},
			"usageMetadata": map[string]int{
				"promptTokenCount": 10, "candidatesTokenCount": 7, "totalTokenCount": 17, "thoughtsTokenCount": 4,
			},
			"modelVersion": "gemini-2.5-flash",
		})
	})

	p := newProvider(t, srv)
	resp, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "What is 6*7?"}))
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	msg := resp.Choices[0].Message
	if msg.Content != "The answer is 42" {
		t.Fatalf("content = %q, want The answer is 42", msg.Content)
	}
	if msg.Reasoning != "Let me think" {
		t.Fatalf("reasoning = %q, want Let me think", msg.Reasoning)
	}
	if resp.Usage.CompletionTokensDetails == nil || resp.Usage.CompletionTokensDetails.ReasoningTokens != 4 {
		t.Fatalf("reasoning tokens = %+v, want 4", resp.Usage.CompletionTokensDetails)
	}
}

func TestStreamingThoughtDeltas(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"step one","thought":true}]},"index":0}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Answer"}]},"index":0}]}`,
		`{"candidates":[{"finishReason":"STOP","index":0}]}`,
	}
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w, events)
	})

	p := newProvider(t, srv)
	ch, err := p.ChatCompletionStream(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "think"}))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var reasoning strings.Builder
	var content strings.Builder
	for c := range ch {
		if c.Done || c.Error != nil {
			continue
		}
		for _, choice := range c.Choices {
			reasoning.WriteString(choice.Delta.Reasoning)
			if s, ok := choice.Delta.Content.(string); ok {
				content.WriteString(s)
			}
		}
	}
	if reasoning.String() != "step one" {
		t.Fatalf("reasoning = %q, want step one", reasoning.String())
	}
	if content.String() != "Answer" {
		t.Fatalf("content = %q, want Answer", content.String())
	}
}

// --- 11. structured output --------------------------------------------------

func TestChatCompletionStructuredOutput(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gc := req.GenerationConfig
		if gc == nil || gc.ResponseMimeType != "application/json" {
			t.Fatalf("responseMimeType = %+v, want application/json", gc)
		}
		if gc.ResponseSchema == nil {
			t.Fatal("expected responseSchema")
		}
		if gc.ResponseSchema["type"] != "object" {
			t.Fatalf("responseSchema = %+v", gc.ResponseSchema)
		}
		writeJSON(t, w, map[string]interface{}{
			"candidates": []map[string]interface{}{{
				"content":      map[string]interface{}{"role": "model", "parts": []map[string]string{{"text": `{"city":"London"}`}}},
				"finishReason": "STOP",
				"index":        0,
			}},
			"usageMetadata": map[string]int{"promptTokenCount": 5, "candidatesTokenCount": 2, "totalTokenCount": 7},
			"modelVersion":  "gemini-2.5-flash",
		})
	})

	p := newProvider(t, srv)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "gemini-2.5-flash",
		Messages: []apitypes.Message{{Role: "user", Content: "Extract city"}},
		ResponseFormat: map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "city",
				"schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"city": map[string]interface{}{"type": "string"}}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Choices[0].Message.Content != `{"city":"London"}` {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
}

// --- 12. usage --------------------------------------------------------------

func TestUsageMapping(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]interface{}{
			"candidates": []map[string]interface{}{{
				"content":      map[string]interface{}{"role": "model", "parts": []map[string]string{{"text": "ok"}}},
				"finishReason": "STOP",
				"index":        0,
			}},
			"usageMetadata": map[string]int{
				"promptTokenCount": 100, "candidatesTokenCount": 20, "totalTokenCount": 120,
				"thoughtsTokenCount": 5, "cachedContentTokenCount": 40,
			},
			"modelVersion": "gemini-2.5-flash",
		})
	})

	p := newProvider(t, srv)
	resp, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "hi"}))
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	u := resp.Usage
	if u.PromptTokens != 100 || u.CompletionTokens != 20 || u.TotalTokens != 120 {
		t.Fatalf("usage = %+v", u)
	}
	if u.PromptTokensDetails == nil || u.PromptTokensDetails.CachedTokens != 40 {
		t.Fatalf("cached tokens = %+v", u.PromptTokensDetails)
	}
	if u.CompletionTokensDetails == nil || u.CompletionTokensDetails.ReasoningTokens != 5 {
		t.Fatalf("reasoning tokens = %+v", u.CompletionTokensDetails)
	}
}

// --- 13. finish reason ------------------------------------------------------

func TestFinishReasonMapping(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]interface{}{
			"candidates": []map[string]interface{}{{
				"content":      map[string]interface{}{"role": "model", "parts": []map[string]string{{"text": "cut off"}}},
				"finishReason": "MAX_TOKENS",
				"index":        0,
			}},
			"usageMetadata": map[string]int{"promptTokenCount": 1, "candidatesTokenCount": 1, "totalTokenCount": 2},
			"modelVersion":  "gemini-2.5-flash",
		})
	})

	p := newProvider(t, srv)
	resp, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "hi"}))
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if *resp.Choices[0].FinishReason != "length" {
		t.Fatalf("finish_reason = %q, want length", *resp.Choices[0].FinishReason)
	}
}

// --- 14. authentication error ----------------------------------------------

func TestAuthenticationError(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(t, w, map[string]interface{}{
			"error": map[string]interface{}{"code": 401, "message": "API key not valid.", "status": "UNAUTHENTICATED"},
		})
	})

	p := newProvider(t, srv)
	_, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "hi"}))
	assertProviderErrorType(t, err, provider.ErrorTypeAuthentication)
}

// --- 15. rate limit / quota ------------------------------------------------

func TestRateLimitQuotaError(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		writeJSON(t, w, map[string]interface{}{
			"error": map[string]interface{}{"code": 429, "message": "Quota exceeded for quota metric.", "status": "RESOURCE_EXHAUSTED"},
		})
	})

	p := newProvider(t, srv)
	_, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "hi"}))
	assertProviderErrorType(t, err, provider.ErrorTypeRateLimit)
}

// --- 16. context error ------------------------------------------------------

func TestContextLengthError(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(t, w, map[string]interface{}{
			"error": map[string]interface{}{
				"code": 400, "message": "The model's context window is too small. Input token limit exceeded.",
				"status": "INVALID_ARGUMENT",
			},
		})
	})

	p := newProvider(t, srv)
	_, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "hi"}))
	assertProviderErrorType(t, err, provider.ErrorTypeContextLength)
}

// --- 17. server error -------------------------------------------------------

func TestServerError(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(t, w, map[string]interface{}{
			"error": map[string]interface{}{"code": 500, "message": "An internal error has occurred.", "status": "INTERNAL"},
		})
	})

	p := newProvider(t, srv)
	_, err := p.ChatCompletion(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "hi"}))
	pe, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if pe.Type != provider.ErrorTypeServerError {
		t.Fatalf("error type = %q, want server_error", pe.Type)
	}
	if !isRetryableError(pe) {
		t.Fatal("server error should be retryable")
	}
}

// --- 18. malformed stream event ---------------------------------------------

func TestStreamingMalformedEvent(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w, []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},"index":0}]}`,
			`this is not json{{{`,
		})
	})

	p := newProvider(t, srv)
	ch, err := p.ChatCompletionStream(context.Background(), chatReq("gemini-2.5-flash",
		apitypes.Message{Role: "user", Content: "hi"}))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var sawError bool
	for c := range ch {
		if c.Error != nil {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected error chunk for malformed event")
	}
}

// --- mapper unit tests ------------------------------------------------------

func TestMapRequestToolChoice(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:      "gemini-2.5-flash",
		Messages:   []apitypes.Message{{Role: "user", Content: "hi"}},
		Tools:      testTool(),
		ToolChoice: map[string]interface{}{"type": "tool", "function": map[string]interface{}{"name": "get_weather"}},
	}
	mapped := MapRequest(req)
	if mapped.ToolConfig == nil || mapped.ToolConfig.FunctionCallingConfig == nil {
		t.Fatal("expected toolConfig")
	}
	cfg := mapped.ToolConfig.FunctionCallingConfig
	if cfg.Mode != "ANY" {
		t.Fatalf("mode = %q, want ANY", cfg.Mode)
	}
	if len(cfg.AllowedFunctionNames) != 1 || cfg.AllowedFunctionNames[0] != "get_weather" {
		t.Fatalf("allowedFunctionNames = %+v", cfg.AllowedFunctionNames)
	}
}

func TestMapRequestThinkingBudget(t *testing.T) {
	budget := 1024
	req := &apitypes.ChatCompletionRequest{
		Model:          "gemini-2.5-flash",
		Messages:       []apitypes.Message{{Role: "user", Content: "hi"}},
		ThinkingBudget: &budget,
	}
	mapped := MapRequest(req)
	if mapped.GenerationConfig == nil || mapped.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("expected thinkingConfig")
	}
	if mapped.GenerationConfig.ThinkingConfig["thinkingBudget"] != 1024 {
		t.Fatalf("thinkingBudget = %v, want 1024", mapped.GenerationConfig.ThinkingConfig["thinkingBudget"])
	}
}

func TestMapRequestMergesConsecutiveToolAndUser(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model: "gemini-2.5-flash",
		Messages: []apitypes.Message{
			{Role: "user", Content: "Weather?"},
			{Role: "assistant", ToolCalls: []apitypes.ToolCall{{ID: "c1", Type: "function", Function: apitypes.FunctionCall{Name: "get_weather", Arguments: `{"city":"SF"}`}}}},
			{Role: "tool", ToolCallID: "c1", Content: "Sunny"},
			{Role: "user", Content: "And time?"},
		},
	}
	mapped := MapRequest(req)
	if len(mapped.Contents) != 3 {
		t.Fatalf("contents len = %d, want 3 (merged tool+user)", len(mapped.Contents))
	}
	last := mapped.Contents[2]
	if last.Role != "user" {
		t.Fatalf("last role = %q, want user", last.Role)
	}
	if len(last.Parts) != 2 {
		t.Fatalf("last parts len = %d, want 2 (functionResponse + text)", len(last.Parts))
	}
	if last.Parts[0].FunctionResponse == nil || last.Parts[1].Text != "And time?" {
		t.Fatalf("last parts = %+v", last.Parts)
	}
}

func TestMapResponseToolCalls(t *testing.T) {
	resp := &generateContentResponse{
		Candidates: []geminiCandidate{{
			Index: 0,
			Content: &geminiResponseContent{Parts: []geminiResponsePart{
				{FunctionCall: &geminiFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"London"}`)}},
			}},
			FinishReason: "STOP",
		}},
		UsageMetadata: &geminiUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 5, TotalTokenCount: 15},
	}
	canonical := MapResponse("gemini-2.5-flash", "resp-1", resp)
	if len(canonical.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d, want 1", len(canonical.Choices[0].Message.ToolCalls))
	}
	if canonical.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"city":"London"}` {
		t.Fatalf("args = %q", canonical.Choices[0].Message.ToolCalls[0].Function.Arguments)
	}
	if *canonical.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", *canonical.Choices[0].FinishReason)
	}
}

func TestSupportsModel(t *testing.T) {
	p := NewProvider("k", "https://generativelanguage.googleapis.com/v1beta", 10*time.Second)
	if !p.SupportsModel("gemini-2.5-flash") {
		t.Fatal("should support gemini-2.5-flash")
	}
	if !p.SupportsModel("models/gemini-2.0-flash") {
		t.Fatal("should support models/gemini-2.0-flash")
	}
	if !p.SupportsModel("text-embedding-004") {
		t.Fatal("should support text-embedding-004")
	}
	if p.SupportsModel("gpt-4o") {
		t.Fatal("should not support gpt-4o")
	}
}

func TestProviderMetadata(t *testing.T) {
	p := NewProvider("k", "https://generativelanguage.googleapis.com/v1beta", 10*time.Second)
	meta := p.GetMetadata()
	if meta.DisplayName != "Google Gemini" {
		t.Fatalf("DisplayName = %q", meta.DisplayName)
	}
	if !meta.Capabilities.Streaming || !meta.Capabilities.ToolCalling || !meta.Capabilities.Reasoning || !meta.Capabilities.Structured {
		t.Fatalf("capabilities = %+v", meta.Capabilities)
	}
}

func TestEmbeddings(t *testing.T) {
	srv := newChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":embedContent") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		writeJSON(t, w, map[string]interface{}{
			"embedding": map[string]interface{}{"values": []float64{0.1, 0.2, 0.3}},
		})
	})

	p := newProvider(t, srv)
	resp, err := p.Embeddings(context.Background(), &apitypes.EmbeddingRequest{
		Model: "text-embedding-004",
		Input: "hello",
	})
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	if len(resp.Data) != 1 || len(resp.Data[0].Embedding) != 3 {
		t.Fatalf("data = %+v", resp.Data)
	}
}

// --- error mapper unit tests ------------------------------------------------

func TestMapErrorUnauthenticated(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":401,"message":"bad key","status":"UNAUTHENTICATED"}}`)),
	}
	err := MapError("gemini", resp)
	if err == nil || err.Type != provider.ErrorTypeAuthentication {
		t.Fatalf("err = %+v", err)
	}
	if isRetryableError(err) {
		t.Fatal("auth error should not be retryable")
	}
}

func TestMapErrorUnavailable(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":503,"message":"Overloaded","status":"UNAVAILABLE"}}`)),
	}
	err := MapError("gemini", resp)
	if err == nil || err.Type != provider.ErrorTypeProviderUnavailable {
		t.Fatalf("err = %+v", err)
	}
	if !isRetryableError(err) {
		t.Fatal("unavailable error should be retryable")
	}
}

func TestValidateAuth(t *testing.T) {
	if ValidateAuth("") == nil {
		t.Fatal("expected error for empty key")
	}
	if ValidateAuth("key") != nil {
		t.Fatal("expected nil for valid key")
	}
}
