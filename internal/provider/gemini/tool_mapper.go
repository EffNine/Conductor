package gemini

import (
	"encoding/json"
	"strings"

	"github.com/EffNine/conductor/internal/apitypes"
)

// toolState tracks a streaming function call while its arguments are
// accumulated for a candidate. Gemini streams tool-call arguments in one of
// two shapes:
//
//   - Legacy/receiving-end: functionCall.args arrives as a complete JSON value
//     in a single chunk.
//   - Fine-grained: functionCall.partialArgs carries one or more jsonPath
//     fragments (e.g. "$.location") with a value, plus willContinue. The call
//     is considered finished when a functionCall without a name and with no
//     willContinue closes it (a bare functionCall{} part or a finishReason).
//
// The raw-string fragments are appended per jsonPath and only assembled into a
// real JSON object when the call closes, so no partial JSON is emitted.
type toolState struct {
	name string

	buf       *strings.Builder // legacy: concatenated raw args fragments
	completed bool             // legacy args assembled OR part-closed

	args   map[string]string // jsonPath -> accumulated string value
	order  []string          // jsonPath publication order for deterministic output
	opened bool              // name confirmed for fine-grained streaming
}

// newToolState creates a fresh tool accumulator.
func newToolState(name string) *toolState {
	return &toolState{name: name, buf: &strings.Builder{}, args: map[string]string{}}
}

// appendArgs buffers a legacy raw fragment and reports whether the accumulated
// args are now a complete, valid JSON value.
func (t *toolState) appendArgs(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return t.completed
	}
	t.buf.Write(raw)
	if t.completed {
		return true
	}
	if isCompleteJSON(t.buf.String()) {
		t.completed = true
	}
	return t.completed
}

// appendPartial records one fine-grained fragment for a jsonPath. A sentinel
// empty value with no willContinue marks that path complete; the value string
// for that path stops accumulating.
func (t *toolState) appendPartial(path, value string, willContinue bool) {
	if path == "" {
		return
	}
	if _, ok := t.args[path]; !ok {
		t.order = append(t.order, path)
	}
	t.args[path] += value
	_ = willContinue
}

// hasPartial reports whether any fine-grained partial fragments were seen.
func (t *toolState) hasPartial() bool {
	return len(t.args) > 0
}

// finalize returns the accumulated arguments as a JSON string. For fine-grained
// partial args the per-path fragments are assembled into a JSON object; for
// legacy raw fragments the buffered JSON is returned (best-effort when partial).
func (t *toolState) finalize() string {
	if len(t.args) > 0 {
		return buildPartialObject(t.args, t.order)
	}
	if t.buf.Len() == 0 {
		return ""
	}
	return t.buf.String()
}

// buildPartialObject assembles fine-grained partial args into a JSON object.
// Paths use Gemini's "$.location" style and may nest ("$.a.b." or "$.items[0]").
func buildPartialObject(values map[string]string, order []string) string {
	root := map[string]interface{}{}
	for _, path := range order {
		segments := splitJSONPath(path)
		if len(segments) == 0 {
			continue
		}
		node := root
		for i, seg := range segments {
			last := i == len(segments)-1
			if last {
				node[seg] = values[path]
				break
			}
			inner, ok := node[seg].(map[string]interface{})
			if !ok {
				inner = map[string]interface{}{}
				node[seg] = inner
			}
			node = inner
		}
	}
	b, err := json.Marshal(root)
	if err != nil {
		return ""
	}
	return string(b)
}

// splitJSONPath splits a Gemini partialArgs jsonPath like "$.location" or
// "$.user.name" into its leaf segments ("location", "user.name" -> ...).
func splitJSONPath(path string) []string {
	// Supported shapes: "$.location", "$.a.b", "$[...]" indices are not
	// tracked structurally; simple dot-path is what the adapter documents.
	trimmed := strings.TrimPrefix(path, "$")
	trimmed = strings.Trim(trimmed, ".")
	var out []string
	for _, seg := range strings.Split(trimmed, ".") {
		if seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

// isCompleteJSON reports whether s parses as a complete JSON value.
func isCompleteJSON(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	var v interface{}
	return json.Unmarshal([]byte(s), &v) == nil
}

// MakeToolCallID synthesizes a stable tool-call id for the Gemini native API,
// which has no native tool-call identifiers. The id is deterministic within a
// request: call_<candidateIndex>_<callIndex>. The code Consequently also
// enables canonical ToolCallID -> function-name correlation for tool results.
func makeToolCallID(candidateIndex, callIndex int) string {
	return "call_gemini_" + itoa(candidateIndex) + "-" + itoa(callIndex)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// mapToolChoice converts the canonical tool_choice value into Gemini's
// toolConfig.functionCallingConfig. Supported modes: AUTO, ANY, NONE, plus
// allowedFunctionNames for forced single-tool selection.
func mapToolChoice(raw interface{}) *geminiFunctionCallingConfig {
	switch v := raw.(type) {
	case nil:
		return nil
	case string:
		switch v {
		case "none":
			return &geminiFunctionCallingConfig{Mode: "NONE"}
		case "required":
			return &geminiFunctionCallingConfig{Mode: "ANY"}
		default: // "auto"
			return &geminiFunctionCallingConfig{Mode: "AUTO"}
		}
	case map[string]interface{}:
		typ, _ := v["type"].(string)
		switch typ {
		case "none":
			return &geminiFunctionCallingConfig{Mode: "NONE"}
		case "auto":
			return &geminiFunctionCallingConfig{Mode: "AUTO"}
		case "tool", "function":
			cfg := &geminiFunctionCallingConfig{Mode: "ANY"}
			if name := extractFunctionName(v); name != "" {
				cfg.AllowedFunctionNames = []string{name}
			}
			return cfg
		default:
			if name := extractFunctionName(v); name != "" {
				return &geminiFunctionCallingConfig{Mode: "ANY", AllowedFunctionNames: []string{name}}
			}
			return &geminiFunctionCallingConfig{Mode: "AUTO"}
		}
	default:
		return nil
	}
}

func extractFunctionName(v map[string]interface{}) string {
	if name, ok := v["name"].(string); ok && name != "" {
		return name
	}
	if fn, ok := v["function"].(map[string]interface{}); ok {
		if name, ok := fn["name"].(string); ok && name != "" {
			return name
		}
	}
	return ""
}

// mapTool converts a canonical Tool into a Gemini function declaration.
func mapTool(t apitypes.Tool) geminiFunctionDeclaration {
	params := t.Function.Parameters
	if params == nil {
		params = map[string]interface{}{"type": "object"}
	}
	return geminiFunctionDeclaration{
		Name:        t.Function.Name,
		Description: t.Function.Description,
		Parameters:  params,
	}
}

// mapToolArgumentsToValue converts a JSON-stringified arguments payload into a
// Gemini-compatible JSON object (RawMessage).
func mapToolArgumentsToValue(arguments string) json.RawMessage {
	if arguments == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(arguments)) {
		return json.RawMessage(arguments)
	}
	return json.RawMessage(`{"_raw":"` + jsonEscape(arguments) + `"}`)
}

// jsonEscape escapes a string for embedding in a JSON string literal.
func jsonEscape(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	if len(b) < 2 {
		return ""
	}
	return string(b[1 : len(b)-1])
}

// mapToolOutputToJson marshals a Gemini function-call args object into a
// JSON-string arguments payload for the canonical FunctionCall.Arguments.
func mapToolOutputToJSON(args json.RawMessage) string {
	if len(args) == 0 || string(args) == "null" {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal(args, &v); err != nil {
		// Preserve raw args even if not a valid JSON value.
		return string(args)
	}
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
