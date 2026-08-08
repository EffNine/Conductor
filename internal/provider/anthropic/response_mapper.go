package anthropic

import (
	"encoding/json"

	"github.com/EffNine/conductor/internal/apitypes"
)

// MapResponse converts an Anthropic message response to the canonical format.
func MapResponse(modelID string, resp *anthropicMessageResponse) *apitypes.ChatCompletionResponse {
	if resp == nil {
		return nil
	}

	var textContent string
	var toolCalls []apitypes.ToolCall
	var thinkingContent string

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			textContent += block.Text
		case "tool_use":
			toolCalls = append(toolCalls, apitypes.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: apitypes.FunctionCall{
					Name:      block.Name,
					Arguments: mapToolOutput(block.Input),
				},
			})
		case "thinking":
			thinkingContent += block.Thinking
		}
	}

	finishReason := mapStopReason(resp.StopReason)

	msg := &apitypes.Message{
		Role:    "assistant",
		Content: textContent,
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	if thinkingContent != "" {
		msg.Reasoning = thinkingContent
	}

	usage := mapUsage(&resp.Usage)

	return &apitypes.ChatCompletionResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: 0,
		Model:   modelID,
		Choices: []apitypes.Choice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: &finishReason,
			},
		},
		Usage: usage,
	}
}

func mapStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens", "stop_sequence":
		return reason
	default:
		return reason
	}
}

func mapUsage(u *usage) *apitypes.Usage {
	if u == nil {
		return nil
	}
	return &apitypes.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.InputTokens + u.OutputTokens,
	}
}

func mapToolOutput(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	data, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return string(data)
}
