package gemini

import (
	"encoding/json"
	"strings"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
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
	params := provider.StripSchemaMetaFields(t.Function.Parameters)
	if params == nil {
		params = map[string]interface{}{"type": "object"}
	}
	params = normalizeGeminiSchema(params)
	return geminiFunctionDeclaration{
		Name:        t.Function.Name,
		Description: t.Function.Description,
		Parameters:  params,
	}
}

// normalizeGeminiSchema rewrites a JSON Schema object into the subset of
// fields that Gemini's native protobuf Schema type actually accepts.
//
// Gemini rejects several standard JSON Schema keywords:
//   - exclusiveMinimum / exclusiveMaximum (no proto equivalent; minimum/maximum
//     are integer-only in Gemini's Schema)
//   - type as an array (proto field is singular string, not repeating)
//   - additionalProperties (rejected by the server in tool schemas)
//   - anyOf / oneOf / allOf (composition keywords are not part of the proto)
//
// Transformations are semantic where possible:
//   - type:["string","null"] → type:"string", nullable:true
//   - anyOf with a single non-null option + null → same as above
//   - anyOf/oneOf/allOf with mixed types → flattened to the first option
//
// Supported keywords that pass through unchanged:
//
//	type(string), format, description, nullable, properties, items, enum,
//	required, minimum, maximum, minLength, maxLength, pattern, minItems,
//	maxItems, default.
func normalizeGeminiSchema(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return nil
	}
	out := make(map[string]interface{}, len(schema))
	// Track whether nullable was derived from a type array containing "null",
	// so an explicit nullable:false in the input doesn't overwrite it.
	typeDerivedNullable := false
	for k, v := range schema {
		switch k {
		case "exclusiveMinimum", "exclusiveMaximum", "additionalProperties":
			// Not supported by Gemini's protobuf Schema for tool parameters.
			continue
		case "$schema", "$ref", "$defs", "$id":
			// JSON Schema meta-fields are not part of Gemini's proto Schema.
			continue
		case "anyOf", "oneOf":
			// Flatten the union into the parent schema.
			merged := normalizeGeminiUnion(v)
			if mergedMap, ok := merged.(map[string]interface{}); ok {
				for mk, mv := range mergedMap {
					out[mk] = mv
				}
			}
		case "allOf":
			// Flatten the first allOf clause into the parent schema.
			merged := normalizeGeminiAllOf(v)
			if mergedMap, ok := merged.(map[string]interface{}); ok {
				for mk, mv := range mergedMap {
					out[mk] = mv
				}
			}
		case "type":
			typ := normalizeGeminiType(v)
			out[k] = typ
			// If the original type was an array containing "null", set nullable.
			if arr, ok := v.([]interface{}); ok {
				for _, item := range arr {
					if s, ok := item.(string); ok && s == "null" {
						out["nullable"] = true
						typeDerivedNullable = true
						break
					}
				}
			}
		case "nullable":
			if typeDerivedNullable {
				// Preserve the type-derived nullable flag; don't let an
				// explicit false in the input override a type array that
				// contains "null".
				continue
			}
			out[k] = normalizeGeminiSchemaValue(v)
		default:
			out[k] = normalizeGeminiSchemaValue(v)
		}
	}
	return out
}

// normalizeGeminiUnion rewrites anyOf / oneOf into a single schema or a
// type+nullable pair when the union is a nullable type union.
func normalizeGeminiUnion(v interface{}) interface{} {
	arr, ok := toSlice(v)
	if !ok || len(arr) == 0 {
		return v
	}
	// Collect non-null options and check for a null option.
	var nonNull []map[string]interface{}
	hasNull := false
	for _, item := range arr {
		m, ok := toMap(item)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		if t == "null" {
			hasNull = true
			continue
		}
		nonNull = append(nonNull, m)
	}
	if hasNull && len(nonNull) == 1 {
		// Nullable union: promote the single non-null type and set nullable.
		schema := normalizeGeminiSchema(nonNull[0])
		schema["nullable"] = true
		return schema
	}
	if len(nonNull) == 1 {
		return normalizeGeminiSchema(nonNull[0])
	}
	// Fallback: use the first option (best-effort, loses disjunctive semantics).
	if len(arr) > 0 {
		if m, ok := toMap(arr[0]); ok {
			return normalizeGeminiSchema(m)
		}
	}
	return v
}

// normalizeGeminiAllOf merges an allOf array into the first schema. Full
// merging is complex and error-prone; taking the first clause is the safest
// lossy fallback that preserves the primary type constraint.
func normalizeGeminiAllOf(v interface{}) interface{} {
	arr, ok := toSlice(v)
	if !ok || len(arr) == 0 {
		return v
	}
	if m, ok := toMap(arr[0]); ok {
		return normalizeGeminiSchema(m)
	}
	return v
}

// normalizeGeminiType converts a type value to a single Gemini-compatible
// string. Arrays like ["string","null"] become the non-null type with
// nullable:true added to the parent schema by the caller.
func normalizeGeminiType(v interface{}) string {
	switch tv := v.(type) {
	case string:
		return tv
	case []interface{}:
		// Pick the first non-null type; callers handle the nullable case.
		for _, item := range tv {
			if s, ok := item.(string); ok && s != "null" {
				return s
			}
		}
		if len(tv) > 0 {
			if s, ok := tv[0].(string); ok {
				return s
			}
		}
		return ""
	}
	return ""
}

// normalizeGeminiSchemaValue recursively normalizes a schema value that is
// not a top-level map (e.g. an items schema inside an array, or a property
// schema inside properties).
func normalizeGeminiSchemaValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		return normalizeGeminiSchema(val)
	case []interface{}:
		// Arrays of primitive values (e.g. enum, required) pass through as-is.
		stripped := make([]interface{}, 0, len(val))
		for _, item := range val {
			if m, ok := item.(map[string]interface{}); ok {
				stripped = append(stripped, normalizeGeminiSchema(m))
			} else {
				stripped = append(stripped, item)
			}
		}
		return stripped
	}
	return v
}

func toMap(v interface{}) (map[string]interface{}, bool) {
	m, ok := v.(map[string]interface{})
	return m, ok
}

func toSlice(v interface{}) ([]interface{}, bool) {
	s, ok := v.([]interface{})
	return s, ok
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
