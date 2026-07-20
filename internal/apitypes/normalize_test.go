package apitypes_test

import (
	"encoding/json"
	"testing"

	"github.com/novexa/gateway/internal/apitypes"
)

func TestMessageNormalizePromotesReasoning(t *testing.T) {
	m := &apitypes.Message{Role: "assistant", Reasoning: "Hello from reasoning"}
	m.Normalize()
	if m.Content != "Hello from reasoning" {
		t.Fatalf("content = %q, want promoted reasoning", m.Content)
	}
	if m.Reasoning != "Hello from reasoning" {
		t.Fatalf("reasoning should be preserved, got %q", m.Reasoning)
	}
}

func TestMessageNormalizePrefersReasoningContent(t *testing.T) {
	m := &apitypes.Message{
		Role:             "assistant",
		Reasoning:        "ignored",
		ReasoningContent: "from deepseek",
	}
	m.Normalize()
	if m.Content != "from deepseek" {
		t.Fatalf("content = %q, want reasoning_content", m.Content)
	}
}

func TestMessageNormalizeKeepsExistingContent(t *testing.T) {
	m := &apitypes.Message{
		Role:      "assistant",
		Content:   "visible",
		Reasoning: "hidden thinking",
	}
	m.Normalize()
	if m.Content != "visible" {
		t.Fatalf("content = %q, want visible", m.Content)
	}
}

func TestNormalizeChoicesUpdatesMessageAndDelta(t *testing.T) {
	choices := []apitypes.Choice{
		{Message: &apitypes.Message{Reasoning: "msg"}},
		{Delta: &apitypes.Message{ReasoningContent: "delta"}},
	}
	apitypes.NormalizeChoices(choices)
	if choices[0].Message.Content != "msg" {
		t.Fatalf("message content = %q", choices[0].Message.Content)
	}
	if choices[1].Delta.Content != "delta" {
		t.Fatalf("delta content = %q", choices[1].Delta.Content)
	}
}

func TestChatCompletionRequestMarshalsReasoningEffort(t *testing.T) {
	include := true
	req := apitypes.ChatCompletionRequest{
		Model: "opencode/big-pickle",
		Messages: []apitypes.Message{
			{Role: "user", Content: "hi"},
		},
		ReasoningEffort:  "high",
		IncludeReasoning: &include,
		Reasoning: &apitypes.ReasoningConfig{
			Effort:  "medium",
			Enabled: boolPtr(true),
		},
	}
	if !req.SupportsReasoningParams() {
		t.Fatal("expected SupportsReasoningParams true")
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v", got["reasoning_effort"])
	}
	if got["include_reasoning"] != true {
		t.Fatalf("include_reasoning = %v", got["include_reasoning"])
	}
	reasoning, ok := got["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning missing: %v", got)
	}
	if reasoning["effort"] != "medium" {
		t.Fatalf("reasoning.effort = %v", reasoning["effort"])
	}
}

func TestChatCompletionRequestOmitsReasoningWhenUnset(t *testing.T) {
	req := apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	if req.SupportsReasoningParams() {
		t.Fatal("expected SupportsReasoningParams false")
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"reasoning", "reasoning_effort", "include_reasoning"} {
		if _, ok := got[key]; ok {
			t.Fatalf("unexpected key %q in %s", key, raw)
		}
	}
}

func TestUsagePreservesReasoningTokens(t *testing.T) {
	raw := []byte(`{
		"prompt_tokens":10,
		"completion_tokens":20,
		"total_tokens":30,
		"completion_tokens_details":{"reasoning_tokens":12}
	}`)
	var usage apitypes.Usage
	if err := json.Unmarshal(raw, &usage); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if usage.CompletionTokensDetails == nil || usage.CompletionTokensDetails.ReasoningTokens != 12 {
		t.Fatalf("reasoning_tokens not preserved: %+v", usage.CompletionTokensDetails)
	}
}

func TestEnsureStreamUsage(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{Model: "x"}
	req.EnsureStreamUsage()
	if req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
		t.Fatalf("StreamOptions = %+v", req.StreamOptions)
	}
	// Idempotent when already set
	req.EnsureStreamUsage()
	if !req.StreamOptions.IncludeUsage {
		t.Fatal("IncludeUsage cleared")
	}
}

func boolPtr(v bool) *bool { return &v }
