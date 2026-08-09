package gemini

import (
	"github.com/EffNine/conductor/internal/apitypes"
)

// geminiCandidate is one generated candidate in a GenerateContentResponse.
type geminiCandidate struct {
	Content      *geminiResponseContent `json:"content,omitempty"`
	FinishReason string                 `json:"finishReason,omitempty"`
	Index        int                    `json:"index"`
}

type geminiResponseContent struct {
	Role  string               `json:"role,omitempty"`
	Parts []geminiResponsePart `json:"parts"`
}

type geminiResponsePart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

type generateContentResponse struct {
	Candidates    []geminiCandidate    `json:"candidates,omitempty"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string               `json:"modelVersion,omitempty"`
}

// MapResponse converts a Gemini generateContentResponse into the canonical
// ChatCompletionResponse. Gemini provides no response id, so the adapter
// synthesizes one.
func MapResponse(modelID, responseID string, resp *generateContentResponse) *apitypes.ChatCompletionResponse {
	if resp == nil {
		return nil
	}

	model := resp.ModelVersion
	if model == "" {
		model = modelID
	}

	choices := make([]apitypes.Choice, 0, len(resp.Candidates))
	for ci := range resp.Candidates {
		c := resp.Candidates[ci]
		msg := &apitypes.Message{Role: "assistant"}
		var textContent, thinkingContent string
		var toolCalls []apitypes.ToolCall

		if c.Content != nil {
			toolIdx := 0
			for _, p := range c.Content.Parts {
				switch {
				case p.Thought && p.Text != "":
					thinkingContent += p.Text
				case p.FunctionCall != nil:
					toolCalls = append(toolCalls, apitypes.ToolCall{
						ID:   makeToolCallID(ci, toolIdx),
						Type: "function",
						Function: apitypes.FunctionCall{
							Name:      p.FunctionCall.Name,
							Arguments: mapToolOutputToJSON(p.FunctionCall.Args),
						},
					})
					toolIdx++
				case p.FunctionResponse != nil:
					// Model candidates normally carry functionCall parts, not
					// functionResponse parts; ignore for the assistant turn.
				case p.Text != "":
					textContent += p.Text
				}
			}
		}

		msg.Content = textContent
		if len(toolCalls) > 0 {
			msg.ToolCalls = toolCalls
		}
		if thinkingContent != "" {
			msg.Reasoning = thinkingContent
		}
		msg.Normalize()

		finish := mapFinishReason(c.FinishReason, len(toolCalls) > 0)
		choices = append(choices, apitypes.Choice{
			Index:        c.Index,
			Message:      msg,
			FinishReason: &finish,
		})
	}

	usage := mapUsage(resp.UsageMetadata)

	return &apitypes.ChatCompletionResponse{
		ID:      responseID,
		Object:  "chat.completion",
		Created: timeNowUnix(),
		Model:   model,
		Choices: choices,
		Usage:   usage,
	}
}

// mapFinishReason maps Gemini finishReason to the canonical finish reason.
// Gemini reports STOP even when the candidate contains functionCall parts, so
// when tool calls are present the canonical reason is overridden to tool_calls.
func mapFinishReason(reason string, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}
	switch reason {
	case "STOP", "LANGUAGE", "OTHER":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII":
		return "content_filter"
	case "MALFORMED_FUNCTION_CALL":
		return "tool_calls"
	default:
		return "stop"
	}
}

// mapUsage converts Gemini usageMetadata into the canonical Usage object.
func mapUsage(u *geminiUsageMetadata) *apitypes.Usage {
	if u == nil {
		return nil
	}
	out := &apitypes.Usage{
		PromptTokens:     u.PromptTokenCount,
		CompletionTokens: u.CandidatesTokenCount,
	}
	if u.TotalTokenCount > 0 {
		out.TotalTokens = u.TotalTokenCount
	} else {
		out.TotalTokens = u.PromptTokenCount + u.CandidatesTokenCount
	}
	if u.CachedContentTokenCount > 0 {
		out.PromptTokensDetails = &apitypes.PromptTokensDetails{CachedTokens: u.CachedContentTokenCount}
	}
	if u.ThoughtsTokenCount > 0 {
		out.CompletionTokensDetails = &apitypes.CompletionTokensDetails{ReasoningTokens: u.ThoughtsTokenCount}
	}
	return out
}
