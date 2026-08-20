package router

import (
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
)

func TestEstimateRequestTokensNil(t *testing.T) {
	if got := EstimateRequestTokens(nil); got != DefaultMaxTokens {
		t.Fatalf("expected %d, got %d", DefaultMaxTokens, got)
	}
}

func TestEstimateRequestTokensEmpty(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Messages: []apitypes.Message{{Role: "user", Content: ""}},
	}
	got := EstimateRequestTokens(req)
	// Empty content contributes only structural overhead + default output.
	if got < DefaultMaxTokens {
		t.Fatalf("expected at least %d, got %d", DefaultMaxTokens, got)
	}
}

func TestEstimateRequestTokensUsesMaxTokens(t *testing.T) {
	max := 8192
	req := &apitypes.ChatCompletionRequest{
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
		MaxTokens: &max,
	}
	got := EstimateRequestTokens(req)
	// Should be input_estimate + 8192.
	if got < 8192 {
		t.Fatalf("expected at least 8192, got %d", got)
	}
	if got > 8192+200 {
		t.Fatalf("expected close to 8192, got %d", got)
	}
}

func TestEstimateRequestTokensDefaultOutputWhenMaxTokensZero(t *testing.T) {
	zero := 0
	req := &apitypes.ChatCompletionRequest{
		Messages:  []apitypes.Message{{Role: "user", Content: "hi"}},
		MaxTokens: &zero,
	}
	got := EstimateRequestTokens(req)
	// Zero MaxTokens should fall back to DefaultMaxTokens.
	if got < DefaultMaxTokens {
		t.Fatalf("expected at least %d, got %d", DefaultMaxTokens, got)
	}
}

func TestEstimateRequestTokensDefaultOutputWhenMaxTokensAbsent(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	got := EstimateRequestTokens(req)
	if got < DefaultMaxTokens {
		t.Fatalf("expected at least %d, got %d", DefaultMaxTokens, got)
	}
}

func TestEstimateRequestTokensMultimodal(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartText, Text: "describe this"},
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
	got := EstimateRequestTokens(req)
	if got < 10 {
		t.Fatalf("expected non-trivial estimate, got %d", got)
	}
}

func TestEstimateRequestTokensWithTools(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Messages: []apitypes.Message{{Role: "user", Content: "call the tool"}},
		Tools: []apitypes.Tool{
			{Type: "function", Function: apitypes.FunctionDef{Name: "search", Description: "Search the web"}},
		},
	}
	got := EstimateRequestTokens(req)
	if got < 20 {
		t.Fatalf("expected tool overhead to increase estimate, got %d", got)
	}
}

func TestEstimateRequestTokensWithReasoningBudget(t *testing.T) {
	budget := 2048
	req := &apitypes.ChatCompletionRequest{
		Messages:       []apitypes.Message{{Role: "user", Content: "think deeply"}},
		ThinkingBudget: &budget,
	}
	got := EstimateRequestTokens(req)
	// Thinking budget is added directly.
	if got < 2048 {
		t.Fatalf("expected thinking budget to be included, got %d", got)
	}
}

func TestEstimateRequestTokensConservativeForLongInput(t *testing.T) {
	longText := ""
	for i := 0; i < 1000; i++ {
		longText += "word "
	}
	req := &apitypes.ChatCompletionRequest{
		Messages: []apitypes.Message{{Role: "user", Content: longText}},
	}
	got := EstimateRequestTokens(req)
	// 1000 words ~ 4000 chars ~ ~1000 tokens input + 4096 default output.
	if got < 1000 {
		t.Fatalf("expected estimate to scale with input, got %d", got)
	}
}

func TestConservativeTokenCount(t *testing.T) {
	if conservativeTokenCount("") != 0 {
		t.Fatal("empty string should yield 0")
	}
	if conservativeTokenCount("hello world") != 3 {
		t.Fatalf("expected 3, got %d", conservativeTokenCount("hello world"))
	}
}
