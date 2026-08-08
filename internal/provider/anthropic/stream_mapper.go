package anthropic

import (
	"encoding/json"
	"strings"

	"github.com/EffNine/conductor/internal/apitypes"
)

// toolState holds the accumulated state for a single tool-use content block.
type toolState struct {
	id   string
	name string
	buf  *strings.Builder
}

// streamAccumulator tracks state across streaming events for a single message.
type streamAccumulator struct {
	id             string
	model          string
	created        int64
	usage          *apitypes.Usage
	textIndex      int
	textBuffer     *strings.Builder
	thinkingIndex  int
	thinkingBuffer *strings.Builder
	// toolStates is keyed by Anthropic content-block index for per-tool isolation.
	toolStates map[int]*toolState
	// toolOrder preserves the source-ordered sequence of tool block indices.
	toolOrder []int
	toolCalls []apitypes.ToolCall
}

// MapStreamChunk converts Anthropic streaming events into canonical StreamChunk.
func MapStreamChunk(event *anthropicStreamEvent, accum *streamAccumulator) *apitypes.StreamChunk {
	if event == nil {
		return nil
	}

	switch event.Type {
	case "message_start":
		return mapMessageStart(event, accum)
	case "content_block_start":
		return mapContentBlockStart(event, accum)
	case "content_block_delta":
		return mapContentBlockDelta(event, accum)
	case "content_block_stop":
		return mapContentBlockStop(event, accum)
	case "message_delta":
		return mapMessageDelta(event, accum)
	case "message_stop":
		accum.Reset()
		return nil
	case "ping":
		return nil
	default:
		return nil
	}
}

func mapMessageStart(event *anthropicStreamEvent, accum *streamAccumulator) *apitypes.StreamChunk {
	ms := event.Message
	if ms == nil {
		return nil
	}
	accum.id = ms.ID
	accum.model = ms.Model
	accum.created = ms.Created
	accum.usage = mapUsage(&ms.Usage)

	return &apitypes.StreamChunk{
		ID:      ms.ID,
		Object:  "chat.completion.chunk",
		Created: ms.Created,
		Model:   ms.Model,
		Usage:   mapUsage(&ms.Usage),
	}
}

func mapContentBlockStart(event *anthropicStreamEvent, accum *streamAccumulator) *apitypes.StreamChunk {
	cs := event.Start
	if cs == nil {
		return nil
	}
	idx := event.Index

	switch cs.Type {
	case "text":
		accum.textIndex = idx
		accum.textBuffer = &strings.Builder{}
		if cs.Text != "" {
			accum.textBuffer.WriteString(cs.Text)
			return &apitypes.StreamChunk{
				ID:      accum.id,
				Object:  "chat.completion.chunk",
				Created: accum.created,
				Model:   accum.model,
				Choices: []apitypes.Choice{{
					Index: idx,
					Delta: &apitypes.Message{Role: "assistant", Content: cs.Text},
				}},
			}
		}
		return &apitypes.StreamChunk{
			ID:      accum.id,
			Object:  "chat.completion.chunk",
			Created: accum.created,
			Model:   accum.model,
			Choices: []apitypes.Choice{{
				Index: idx,
				Delta: &apitypes.Message{Role: "assistant"},
			}},
		}
	case "tool_use":
		ts := &toolState{
			id:   cs.ID,
			name: cs.Name,
			buf:  &strings.Builder{},
		}
		if accum.toolStates == nil {
			accum.toolStates = make(map[int]*toolState)
		}
		accum.toolStates[idx] = ts
		accum.toolOrder = append(accum.toolOrder, idx)
		return nil
	case "thinking":
		accum.thinkingIndex = idx
		accum.thinkingBuffer = &strings.Builder{}
		if cs.Thinking != "" {
			accum.thinkingBuffer.WriteString(cs.Thinking)
			return &apitypes.StreamChunk{
				ID:      accum.id,
				Object:  "chat.completion.chunk",
				Created: accum.created,
				Model:   accum.model,
				Choices: []apitypes.Choice{{
					Index: idx,
					Delta: &apitypes.Message{Role: "assistant", Reasoning: cs.Thinking},
				}},
			}
		}
		return &apitypes.StreamChunk{
			ID:      accum.id,
			Object:  "chat.completion.chunk",
			Created: accum.created,
			Model:   accum.model,
			Choices: []apitypes.Choice{{
				Index: idx,
				Delta: &apitypes.Message{Role: "assistant", Reasoning: ""},
			}},
		}
	default:
		return nil
	}
}

func mapContentBlockDelta(event *anthropicStreamEvent, accum *streamAccumulator) *apitypes.StreamChunk {
	d := event.Delta
	if d == nil {
		return nil
	}
	idx := event.Index

	switch d.Type {
	case "text_delta":
		if accum.textBuffer != nil {
			accum.textBuffer.WriteString(d.Text)
		}
		return &apitypes.StreamChunk{
			ID:      accum.id,
			Object:  "chat.completion.chunk",
			Created: accum.created,
			Model:   accum.model,
			Choices: []apitypes.Choice{{
				Index: idx,
				Delta: &apitypes.Message{Role: "assistant", Content: d.Text},
			}},
		}
	case "input_json_delta":
		if ts, ok := accum.toolStates[idx]; ok && ts.buf != nil && d.PartialJSON != "" {
			ts.buf.WriteString(d.PartialJSON)
		}
		return nil
	case "thinking_delta":
		if accum.thinkingBuffer != nil {
			accum.thinkingBuffer.WriteString(d.Thinking)
		}
		return &apitypes.StreamChunk{
			ID:      accum.id,
			Object:  "chat.completion.chunk",
			Created: accum.created,
			Model:   accum.model,
			Choices: []apitypes.Choice{{
				Index: idx,
				Delta: &apitypes.Message{Role: "assistant", Reasoning: d.Thinking},
			}},
		}
	default:
		return nil
	}
}

func mapContentBlockStop(event *anthropicStreamEvent, accum *streamAccumulator) *apitypes.StreamChunk {
	idx := event.Index

	switch idx {
	case accum.textIndex:
		accum.textBuffer = nil
	case accum.thinkingIndex:
		accum.thinkingBuffer = nil
	default:
		// Tool block stop — finalize that tool's accumulated JSON.
		ts, ok := accum.toolStates[idx]
		if !ok {
			break
		}
		if ts.buf != nil && ts.buf.Len() > 0 {
			var input map[string]any
			if err := json.Unmarshal([]byte(ts.buf.String()), &input); err != nil {
				input = map[string]any{"_raw": ts.buf.String()}
			}
			accum.toolCalls = append(accum.toolCalls, apitypes.ToolCall{
				ID:   ts.id,
				Type: "function",
				Function: apitypes.FunctionCall{
					Name:      ts.name,
					Arguments: mapToolOutput(input),
				},
			})
		}
		delete(accum.toolStates, idx)
	}

	return nil
}

func mapMessageDelta(event *anthropicStreamEvent, accum *streamAccumulator) *apitypes.StreamChunk {
	if event.Delta == nil {
		return nil
	}

	finishReason := mapStopReason(event.Delta.StopReason)
	usage := mapUsage(&event.Usage)

	choices := []apitypes.Choice{{
		Index:        0,
		Delta:        &apitypes.Message{},
		FinishReason: &finishReason,
	}}

	if len(accum.toolCalls) > 0 {
		choices[0].Delta.ToolCalls = accum.toolCalls
	}

	chunk := &apitypes.StreamChunk{
		ID:      accum.id,
		Object:  "chat.completion.chunk",
		Created: accum.created,
		Model:   accum.model,
		Choices: choices,
	}

	if usage != nil {
		chunk.Usage = usage
		accum.usage = usage
	}

	return chunk
}

// Reset clears all per-message state so the accumulator can be reused.
func (a *streamAccumulator) Reset() {
	a.id = ""
	a.model = ""
	a.created = 0
	a.usage = nil
	a.textIndex = -1
	a.textBuffer = nil
	a.thinkingIndex = -1
	a.thinkingBuffer = nil
	a.toolStates = nil
	a.toolOrder = nil
	a.toolCalls = nil
}

// NewStreamAccumulator creates a fresh stream accumulator.
func NewStreamAccumulator() *streamAccumulator {
	return &streamAccumulator{textIndex: -1, thinkingIndex: -1}
}

// GetText returns the accumulated text content.
func (a *streamAccumulator) GetText() string {
	if a.textBuffer == nil {
		return ""
	}
	return a.textBuffer.String()
}

// GetThinking returns the accumulated thinking content.
func (a *streamAccumulator) GetThinking() string {
	if a.thinkingBuffer == nil {
		return ""
	}
	return a.thinkingBuffer.String()
}

// Streaming event types.

type anthropicStreamEvent struct {
	Type    string                 `json:"type"`
	Message *messageStartPayload   `json:"message,omitempty"`
	Index   int                    `json:"index,omitempty"`
	Start   *anthropicContentBlock `json:"start,omitempty"`
	Delta   *streamDelta           `json:"delta,omitempty"`
	Usage   usage                  `json:"usage,omitempty"`
}

type messageStartPayload struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Role       string `json:"role"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Usage      usage  `json:"usage"`
	Created    int64  `json:"created"`
}

type streamDelta struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	PartialJSON  string `json:"partial_json,omitempty"`
	Thinking     string `json:"thinking,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
}
