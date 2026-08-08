package openai

import "github.com/EffNine/conductor/internal/apitypes"

// MapResponse converts an OpenAI chat response to the canonical format.
func MapResponse(modelID string, resp *openaiChatResponse) *apitypes.ChatCompletionResponse {
	if resp == nil {
		return nil
	}

	choices := make([]apitypes.Choice, 0, len(resp.Choices))
	for _, c := range resp.Choices {
		choices = append(choices, apitypes.Choice{
			Index:        c.Index,
			Message:      mapMessageFromResponse(c.Message),
			FinishReason: c.FinishReason,
			LogProbs:     mapLogProbs(c.LogProbs),
		})
	}

	usage := mapUsage(resp.Usage)

	return &apitypes.ChatCompletionResponse{
		ID:                resp.ID,
		Object:            resp.Object,
		Created:           resp.Created,
		Model:             modelID,
		Choices:           choices,
		Usage:             usage,
		SystemFingerprint: resp.SystemFingerprint,
	}
}

func mapMessageFromResponse(m openaiMessage) *apitypes.Message {
	msg := &apitypes.Message{
		Role:             m.Role,
		Reasoning:        m.Reasoning,
		ReasoningContent: m.ReasoningContent,
	}

	switch v := m.Content.(type) {
	case string:
		msg.Content = v
	case []openaiContentPart:
		parts := make([]apitypes.ContentPart, 0, len(v))
		for _, p := range v {
			parts = append(parts, apitypes.ContentPart{
				Type:     apitypes.ContentPartType(p.Type),
				Text:     p.Text,
				ImageURL: p.ImageURL,
			})
		}
		msg.Content = parts
	case nil:
		msg.Content = nil
	default:
		msg.Content = v
	}

	if len(m.ToolCalls) > 0 {
		msg.ToolCalls = make([]apitypes.ToolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, apitypes.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: apitypes.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	msg.Normalize()
	return msg
}

func mapLogProbs(lp *openaiLogProbs) *apitypes.LogProbs {
	if lp == nil {
		return nil
	}
	return &apitypes.LogProbs{
		TextOffset:    lp.TextOffset,
		TokenLogProbs: lp.TokenLogProbs,
		Tokens:        lp.Tokens,
		TopLogProbs:   lp.TopLogProbs,
	}
}

func mapUsage(u *openaiUsage) *apitypes.Usage {
	if u == nil {
		return nil
	}
	usage := &apitypes.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.PromptTokensDetails != nil {
		usage.PromptTokensDetails = &apitypes.PromptTokensDetails{
			CachedTokens: u.PromptTokensDetails.CachedTokens,
			AudioTokens:  u.PromptTokensDetails.AudioTokens,
		}
	}
	if u.CompletionTokensDetails != nil {
		usage.CompletionTokensDetails = &apitypes.CompletionTokensDetails{
			ReasoningTokens:          u.CompletionTokensDetails.ReasoningTokens,
			AudioTokens:              u.CompletionTokensDetails.AudioTokens,
			AcceptedPredictionTokens: u.CompletionTokensDetails.AcceptedPredictionTokens,
			RejectedPredictionTokens: u.CompletionTokensDetails.RejectedPredictionTokens,
		}
	}
	return usage
}
