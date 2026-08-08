package openai

import "github.com/EffNine/conductor/internal/apitypes"

// MapStreamChunk converts an OpenAI stream chunk to the canonical format.
func MapStreamChunk(chunk *apitypes.StreamChunk) apitypes.StreamChunk {
	c := apitypes.StreamChunk{
		ID:      chunk.ID,
		Object:  chunk.Object,
		Created: chunk.Created,
		Model:   chunk.Model,
		Usage:   chunk.Usage,
	}

	c.Choices = make([]apitypes.Choice, 0, len(chunk.Choices))
	for _, choice := range chunk.Choices {
		c.Choices = append(c.Choices, apitypes.Choice{
			Index:        choice.Index,
			Delta:        mapDeltaFromStream(choice.Delta),
			FinishReason: choice.FinishReason,
			LogProbs:     choice.LogProbs,
		})
	}

	return c
}

func mapDeltaFromStream(d *apitypes.Message) *apitypes.Message {
	if d == nil {
		return nil
	}
	msg := &apitypes.Message{
		Role:             d.Role,
		Reasoning:        d.Reasoning,
		ReasoningContent: d.ReasoningContent,
		ToolCallID:       d.ToolCallID,
	}

	switch v := d.Content.(type) {
	case string:
		msg.Content = v
	case []apitypes.ContentPart:
		parts := make([]apitypes.ContentPart, 0, len(v))
		for _, p := range v {
			parts = append(parts, apitypes.ContentPart{
				Type:     p.Type,
				Text:     p.Text,
				ImageURL: p.ImageURL,
			})
		}
		msg.Content = parts
	}

	if len(d.ToolCalls) > 0 {
		msg.ToolCalls = make([]apitypes.ToolCall, 0, len(d.ToolCalls))
		for _, tc := range d.ToolCalls {
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

	return msg
}
