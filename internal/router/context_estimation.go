package router

import (
	"github.com/EffNine/conductor/internal/apitypes"
)

// DefaultMaxTokens is the conservative estimated output token budget used when
// req.MaxTokens is absent or zero. It is intentionally small enough to avoid
// falsely flagging long-but-valid requests while still leaving meaningful
// headroom for actual model outputs.
const DefaultMaxTokens = 4096

// estimateInputTokens returns a conservative estimate of the token count consumed
// by the request messages (input-side only). It uses a characters-per-token
// heuristic with per-message structural overhead and per-tool-definition overhead.
//
// This is an ESTIMATE, not an exact count. The true tokenizer varies by provider
// and model. Callers must treat the result as a lower-bound proxy and combine it
// with the expected output budget before comparing against model.MaxContext.
func estimateInputTokens(req *apitypes.ChatCompletionRequest) int {
	if req == nil {
		return 0
	}
	estimated := 0

	for _, m := range req.Messages {
		// Structural overhead per message: role prefix, delimiters, whitespace.
		estimated += 4

		switch v := m.Content.(type) {
		case string:
			estimated += conservativeTokenCount(v)
		case []apitypes.ContentPart:
			for _, p := range v {
				switch p.Type {
				case apitypes.ContentPartText:
					estimated += conservativeTokenCount(p.Text)
				case apitypes.ContentPartImageURL:
					// Images are not tokenized here; a few tokens are added to
					// acknowledge their presence without overstating context use.
					estimated += 5
				}
			}
		}

		// Reasoning fields contribute to context but are already counted above
		// when they appear as Content. When content is empty and only reasoning
		// is present, account for the reasoning text separately.
		if m.ContentString() == "" {
			estimated += conservativeTokenCount(m.Reasoning)
			estimated += conservativeTokenCount(m.ReasoningContent)
		}
	}

	// Tool/function definitions are part of the prompt context when present.
	for _, t := range req.Tools {
		// Structural overhead plus the function definition text.
		estimated += 8
		def := t.Function.Name
		if t.Function.Description != "" {
			def += " " + t.Function.Description
		}
		estimated += conservativeTokenCount(def)
	}

	// Reasoning control fields add to the prompt context when present.
	if req.ReasoningEffort != "" {
		estimated += 4
	}
	if req.Reasoning != nil {
		if req.Reasoning.Effort != "" {
			estimated += 4
		}
		if req.Reasoning.Summary != "" {
			estimated += 4
		}
	}
	if req.ThinkingBudget != nil && *req.ThinkingBudget > 0 {
		// Token budget for internal reasoning contributes linearly.
		estimated += *req.ThinkingBudget
	}

	return estimated
}

// EstimateRequestTokens returns the estimated total token requirement for a
// chat completion request: input tokens (messages + tools + reasoning controls)
// plus expected output tokens.
//
// Expected output tokens are taken from req.MaxTokens when explicitly provided
// and positive; otherwise DefaultMaxTokens is used as a conservative bound.
//
// The returned value is an estimate, not an exact tokenizer count.
func EstimateRequestTokens(req *apitypes.ChatCompletionRequest) int {
	if req == nil {
		return DefaultMaxTokens
	}
	input := estimateInputTokens(req)
	output := DefaultMaxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		output = *req.MaxTokens
	}
	return input + output
}

// conservativeTokenCount returns a conservative (typically over-)estimate of the
// number of tokens in s. The factor 4 characters per token is a widely used
// rough proxy for English text; it may under-count for dense multi-byte scripts
// but over-counts for them, keeping the bound safe in both directions.
func conservativeTokenCount(s string) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s) + 3) / 4
}
