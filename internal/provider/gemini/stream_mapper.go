package gemini

import (
	"strings"

	"github.com/EffNine/conductor/internal/apitypes"
)

// candidateStreamState tracks a single streaming candidate across SSE events.
// Gemini streams fine-grained function calls over multiple events with
// partialArgs jsonPath fragments, so tool fragments are accumulated per
// candidate until the closing functionCall{} event before a tool_calls delta
// is emitted.
type candidateStreamState struct {
	index int

	textBuffer *strings.Builder
	thoughtBuf *strings.Builder

	tools     map[string]*toolState
	toolOrder []string
	callCount int

	current *toolState // open fine-grained call awaiting its sealing event
}

// ensureTool returns the tool state for a tool name, creating it on first
// sight (the order of creation drives the emitted tool_call ids). Continuation
// deltas omit the name, so an empty name resolves to the currently open tool.
func (cs *candidateStreamState) ensureTool(name string) *toolState {
	if name == "" {
		return cs.current
	}
	if cs.tools == nil {
		cs.tools = make(map[string]*toolState)
	}
	ts, ok := cs.tools[name]
	if !ok {
		ts = newToolState(name)
		cs.tools[name] = ts
		cs.toolOrder = append(cs.toolOrder, name)
	}
	cs.current = ts
	return ts
}

// closeOpenTool seals whatever fine-grained call is open (bare functionCall{}
// event). A sealed call that accumulated partial arguments emits its tool_call
// delta; an empty confirmation emits nothing.
func (cs *candidateStreamState) closeOpenTool(index int) *apitypes.StreamChunk {
	if cs.current == nil {
		return nil
	}
	ts := cs.current
	cs.current = nil
	if !ts.opened && !ts.hasPartial() {
		return nil
	}
	return cs.emitTool(index, ts)
}

// emitTool assembles the canonical chunk carrying one completed tool call.
func (cs *candidateStreamState) emitTool(index int, ts *toolState) *apitypes.StreamChunk {
	ts.completed = true
	cs.callCount++
	return &apitypes.StreamChunk{
		Object: "chat.completion.chunk",
		Choices: []apitypes.Choice{{
			Index: index,
			Delta: &apitypes.Message{
				Role: "assistant",
				ToolCalls: []apitypes.ToolCall{{
					ID:   makeToolCallID(index, cs.callCount),
					Type: "function",
					Function: apitypes.FunctionCall{
						Name:      ts.name,
						Arguments: ts.finalize(),
					},
				}},
			},
		}},
	}
}

// streamAccumulator tracks state across SSE events for one request.
type streamAccumulator struct {
	id         string
	model      string
	created    int64
	usage      *apitypes.Usage
	candidates map[int]*candidateStreamState
}

// NewStreamAccumulator creates a fresh accumulator for a request.
func NewStreamAccumulator(model, id string) *streamAccumulator {
	return &streamAccumulator{
		model:      model,
		id:         id,
		candidates: make(map[int]*candidateStreamState),
	}
}

func (a *streamAccumulator) candidate(index int) *candidateStreamState {
	cs, ok := a.candidates[index]
	if !ok {
		cs = &candidateStreamState{index: index}
		a.candidates[index] = cs
	}
	return cs
}

// Reset clears per-request state so the accumulator could be reused.
func (a *streamAccumulator) Reset() {
	a.id = ""
	a.model = ""
	a.created = 0
	a.usage = nil
	a.candidates = make(map[int]*candidateStreamState)
}

func (a *streamAccumulator) makeChunk(index int, delta *apitypes.Message) *apitypes.StreamChunk {
	return &apitypes.StreamChunk{
		ID:      a.id,
		Object:  "chat.completion.chunk",
		Created: a.created,
		Model:   a.model,
		Choices: []apitypes.Choice{{Index: index, Delta: delta}},
	}
}

// MapStreamChunk converts one Gemini SSE GenerateContentResponse into zero or
// more canonical stream chunks. A single event can carry text deltas, thought
// deltas, completed function calls and a finish reason, so the return is a
// slice (ordered).
func MapStreamChunk(event *generateContentResponse, accum *streamAccumulator) []*apitypes.StreamChunk {
	if event == nil || accum == nil {
		return nil
	}

	if accum.model == "" {
		accum.model = event.ModelVersion
	}
	eventUsage := mapUsage(event.UsageMetadata)
	if eventUsage != nil {
		accum.usage = eventUsage
	}

	var chunks []*apitypes.StreamChunk
	for ci := range event.Candidates {
		cand := event.Candidates[ci]
		index := cand.Index
		if ci == 0 && index == 0 {
			index = 0
		}
		cs := accum.candidate(index)

		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				switch {
				case part.FunctionCall != nil:
					if tc := accum.handleToolCall(index, part.FunctionCall); tc != nil {
						if eventUsage != nil {
							tc.Usage = eventUsage
						}
						chunks = append(chunks, tc)
					}
				case part.Thought && part.Text != "":
					if cs.thoughtBuf == nil {
						cs.thoughtBuf = &strings.Builder{}
					}
					cs.thoughtBuf.WriteString(part.Text)
					chunk := accum.makeChunk(index, &apitypes.Message{Role: "assistant", Reasoning: part.Text})
					if eventUsage != nil {
						chunk.Usage = eventUsage
					}
					chunks = append(chunks, chunk)
				case part.Text != "":
					if cs.textBuffer == nil {
						cs.textBuffer = &strings.Builder{}
					}
					cs.textBuffer.WriteString(part.Text)
					chunk := accum.makeChunk(index, &apitypes.Message{Role: "assistant", Content: part.Text})
					if eventUsage != nil {
						chunk.Usage = eventUsage
					}
					chunks = append(chunks, chunk)
				case part.FunctionResponse != nil:
					// Function responses belong to the request history, not to
					// a streamed assistant candidate; ignore.
				}
			}
		}

		if cand.FinishReason != "" {
			finish := mapFinishReason(cand.FinishReason, cs.callCount > 0 || len(cs.toolOrder) > 0)
			delta := &apitypes.Message{Role: "assistant"}

			// Flush any tool calls that never became complete JSON, including
			// fine-grained partials whose sealing functionCall{} never arrived.
			var flush []apitypes.ToolCall
			for _, name := range cs.toolOrder {
				ts := cs.tools[name]
				if ts == nil || ts.completed {
					continue
				}
				if !ts.opened && !ts.hasPartial() && len(ts.buf.String()) == 0 {
					continue
				}
				cs.callCount++
				flush = append(flush, apitypes.ToolCall{
					ID:   makeToolCallID(index, cs.callCount),
					Type: "function",
					Function: apitypes.FunctionCall{
						Name:      ts.name,
						Arguments: ts.finalize(),
					},
				})
				ts.completed = true
			}
			if len(flush) > 0 {
				delta.ToolCalls = flush
			}

			chunk := accum.makeChunk(index, delta)
			chunk.Choices[0].FinishReason = &finish
			if accum.usage != nil {
				chunk.Usage = accum.usage
			}
			chunks = append(chunks, chunk)
		}
	}

	// A trailing usage-only event (no candidates) still surfaces the final
	// token counts on an otherwise-empty chunk.
	if len(chunks) == 0 && eventUsage != nil && len(event.Candidates) == 0 {
		chunk := accum.makeChunk(0, &apitypes.Message{})
		chunk.Usage = eventUsage
		chunks = append(chunks, chunk)
	}

	return chunks
}

// handleToolCall accumulates a streaming function-call fragment on the
// candidate's tool state and, when the call closes, emits the final tool-call
// delta. Gemini drives fine-grained streaming with an initial functionCall
// part carrying the name and willContinue, then partialArgs jsonPath
// fragments, and finally a bare functionCall{} (no name, no willContinue) that
// seals the call. Miscarrier fragments and legacy complete args are handled too.
func (a *streamAccumulator) handleToolCall(index int, fc *geminiFunctionCall) *apitypes.StreamChunk {
	cs := a.candidate(index)

	// A bare functionCall{} without a name seals whichever fine-grained call is
	// currently open on this candidate.
	if fc.Name == "" && len(fc.Args) == 0 && len(fc.PartialArgs) == 0 {
		return cs.closeOpenTool(index)
	}

	ts := cs.ensureTool(fc.Name)
	if len(fc.PartialArgs) > 0 {
		ts.opened = true
		for _, pa := range fc.PartialArgs {
			ts.appendPartial(pa.JSONPath, pa.StringValue, fc.WillContinue)
		}
		// A partialArgs event that no longer carries a value seals the open
		// call's payload; a following functionCall{} part emits it.
		return nil
	}

	if len(fc.Args) > 0 {
		if ts.appendArgs(fc.Args) {
			cs.callCount++
			return a.makeChunk(index, &apitypes.Message{
				Role: "assistant",
				ToolCalls: []apitypes.ToolCall{{
					ID:   makeToolCallID(index, cs.callCount),
					Type: "function",
					Function: apitypes.FunctionCall{
						Name:      ts.name,
						Arguments: ts.finalize(),
					},
				}},
			})
		}
		return nil
	}

	// Openers that only confirm the name (willContinue, no args/partials yet)
	// stay silent until partialArgs or the sealing event arrive.
	if fc.WillContinue && fc.Name != "" {
		ts.opened = true
		return nil
	}

	// A name with no args and no continuation is complete (rare).
	if ts.opened {
		return cs.emitTool(index, ts)
	}
	return nil
}
