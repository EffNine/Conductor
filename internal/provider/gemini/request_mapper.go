package gemini

import (
	"encoding/json"
	"strings"

	"github.com/EffNine/conductor/internal/apitypes"
)

// Gemini-native wire types. Everything here is unexported so the wire schema
// never escapes the adapter package; the canonical contract stays untouched.

type generateContentRequest struct {
	Contents          []geminiContent         `json:"contents,omitempty"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

// geminiContent is a conversation turn. Gemini roles are "user" and "model"
// only; consecutive same-role contents are not allowed by the API.
type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiBlob             `json:"inlineData,omitempty"`
	FileData         *geminiFileData         `json:"fileData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiBlob struct {
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"` // base64
}

type geminiFileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri,omitempty"`
}

type geminiFunctionCall struct {
	Name         string             `json:"name,omitempty"`
	Args         json.RawMessage    `json:"args,omitempty"`
	PartialArgs  []geminiPartialArg `json:"partialArgs,omitempty"`
	WillContinue bool               `json:"willContinue,omitempty"`
}

// geminiPartialArg is one fragment of a fine-grained, streamed function-call
// argument. Gemini delivers args incrementally as jsonPath fragments (e.g.
// "$.location") paired with a value, marking the call unfinished via
// willContinue until the closing functionCall{} event.
type geminiPartialArg struct {
	JSONPath    string   `json:"jsonPath,omitempty"`
	StringValue string   `json:"stringValue,omitempty"`
	NumberValue *float64 `json:"numberValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig *geminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature      *float64               `json:"temperature,omitempty"`
	TopP             *float64               `json:"topP,omitempty"`
	TopK             *int                   `json:"topK,omitempty"`
	MaxOutputTokens  *int                   `json:"maxOutputTokens,omitempty"`
	CandidateCount   *int                   `json:"candidateCount,omitempty"`
	StopSequences    []string               `json:"stopSequences,omitempty"`
	ResponseMimeType string                 `json:"responseMimeType,omitempty"`
	ResponseSchema   map[string]interface{} `json:"responseSchema,omitempty"`
	Seed             *int                   `json:"seed,omitempty"`
	PresencePenalty  *float64               `json:"presencePenalty,omitempty"`
	FrequencyPenalty *float64               `json:"frequencyPenalty,omitempty"`
	ThinkingConfig   map[string]interface{} `json:"thinkingConfig,omitempty"`
}

// MapRequest converts a canonical ChatCompletionRequest into a Gemini
// generateContent request. It returns nil when the request cannot be
// represented (mirrors the Anthropic adapter's nil-on-unrepresentable rule;
// currently all canonical fields are representable).
func MapRequest(req *apitypes.ChatCompletionRequest) *generateContentRequest {
	if req == nil {
		return nil
	}

	out := &generateContentRequest{
		Contents: make([]geminiContent, 0, len(req.Messages)),
	}

	// System instructions.
	var systemParts []string
	for _, m := range req.Messages {
		if m.Role == "system" {
			if s := m.ContentString(); s != "" {
				systemParts = append(systemParts, s)
			}
		}
	}
	if len(systemParts) > 0 {
		out.SystemInstruction = &geminiContent{
			Role:  "system",
			Parts: []geminiPart{{Text: strings.Join(systemParts, "\n")}},
		}
	}

	// Contents with role merging. toolCallNames remembers the function name for
	// each canonical tool_call id so that later tool results can be correlated
	// back to the tool Gemini invoked (Gemini's history protocol has no ids).
	toolCallNames := make(map[string]string)
	for i := range req.Messages {
		m := req.Messages[i]
		if m.Role == "system" {
			continue
		}
		role := mapGeminiRole(m.Role)
		parts := mapMessageParts(m, toolCallNames)
		if len(parts) == 0 {
			continue
		}
		if n := len(out.Contents); n > 0 && out.Contents[n-1].Role == role {
			out.Contents[n-1].Parts = append(out.Contents[n-1].Parts, parts...)
		} else {
			out.Contents = append(out.Contents, geminiContent{Role: role, Parts: parts})
		}
	}

	if len(req.Tools) > 0 {
		decls := make([]geminiFunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, mapTool(t))
		}
		out.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}

	if req.ToolChoice != nil {
		cfg := mapToolChoice(req.ToolChoice)
		if cfg != nil {
			out.ToolConfig = &geminiToolConfig{FunctionCallingConfig: cfg}
		}
	}

	out.GenerationConfig = mapGenerationConfig(req)

	return out
}

func mapGeminiRole(role string) string {
	switch role {
	case "assistant":
		return "model"
	case "user", "tool":
		return "user"
	default:
		return "user"
	}
}

// mapMessageParts converts one canonical message into Gemini parts. It keeps
// the tool-call id -> function-name correlation for later tool results.
func mapMessageParts(m apitypes.Message, toolCallNames map[string]string) []geminiPart {
	if m.Role == "tool" || m.ToolCallID != "" {
		name := toolCallNames[m.ToolCallID]
		if name == "" {
			name = "tool_0"
		}
		var output json.RawMessage
		if s, ok := m.Content.(string); ok && s != "" {
			var v interface{}
			if json.Unmarshal([]byte(s), &v) == nil {
				output, _ = json.Marshal(v)
			} else {
				output, _ = json.Marshal(map[string]any{"result": s})
			}
		} else if parts, ok := m.Content.([]apitypes.ContentPart); ok {
			output, _ = json.Marshal(map[string]any{"result": partsString(parts)})
		}
		if len(output) == 0 {
			output = json.RawMessage(`{}`)
		}
		return []geminiPart{{
			FunctionResponse: &geminiFunctionResponse{Name: name, Response: output},
		}}
	}

	parts := mapContentParts(m)

	if len(m.ToolCalls) > 0 {
		for i, tc := range m.ToolCalls {
			toolCallNames[tc.ID] = tc.Function.Name
			parts = append(parts, geminiPart{
				FunctionCall: &geminiFunctionCall{
					Name: tc.Function.Name,
					Args: mapToolArgumentsToValue(tc.Function.Arguments),
				},
				// Maintains no wire-level index; order is preserved by slice order.
			})
			_ = i
		}
	}

	// Thought signature deltas (canonical reasoning) round-trip as thought text.
	if m.Reasoning != "" || m.ReasoningContent != "" {
		text := m.Reasoning
		if text == "" {
			text = m.ReasoningContent
		}
		parts = append(parts, geminiPart{Text: text})
	}

	return parts
}

func mapContentParts(m apitypes.Message) []geminiPart {
	switch v := m.Content.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []geminiPart{{Text: v}}
	case []apitypes.ContentPart:
		parts := make([]geminiPart, 0, len(v))
		for _, p := range v {
			switch p.Type {
			case apitypes.ContentPartText:
				if p.Text != "" {
					parts = append(parts, geminiPart{Text: p.Text})
				}
			case apitypes.ContentPartImageURL:
				if p.ImageURL == nil || p.ImageURL.URL == "" {
					continue
				}
				if blob, ok := imageURLToBlob(p.ImageURL.URL); ok {
					parts = append(parts, geminiPart{InlineData: blob})
				} else if fd := fileURLToFileData(p.ImageURL.URL); fd != nil {
					parts = append(parts, geminiPart{FileData: fd})
				}
				// Arbitrary https URLs are not accepted by generateContent;
				// the part is dropped (documented in gap-analysis/gemini.md).
			}
		}
		return parts
	default:
		return nil
	}
}

func partsString(parts []apitypes.ContentPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type == apitypes.ContentPartText {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// mapGenerationConfig builds generationConfig from canonical sampling,
// stopping, structured-output and reasoning fields.
func mapGenerationConfig(req *apitypes.ChatCompletionRequest) *geminiGenerationConfig {
	gc := &geminiGenerationConfig{}

	if req.Temperature != nil {
		gc.Temperature = req.Temperature
	}
	if req.TopP != nil {
		gc.TopP = req.TopP
	}
	if req.MaxTokens != nil {
		gc.MaxOutputTokens = req.MaxTokens
	}
	if req.N != nil && *req.N > 1 {
		gc.CandidateCount = req.N
	}
	if req.Seed != nil {
		gc.Seed = req.Seed
	}
	if req.PresencePenalty != nil {
		gc.PresencePenalty = req.PresencePenalty
	}
	if req.FrequencyPenalty != nil {
		gc.FrequencyPenalty = req.FrequencyPenalty
	}
	if req.Stop != nil {
		switch v := req.Stop.(type) {
		case string:
			if v != "" {
				gc.StopSequences = []string{v}
			}
		case []string:
			gc.StopSequences = v
		case []interface{}:
			for _, s := range v {
				if str, ok := s.(string); ok && str != "" {
					gc.StopSequences = append(gc.StopSequences, str)
				}
			}
		}
	}

	if len(req.ResponseFormat) > 0 {
		if mime, schema := mapResponseFormat(req.ResponseFormat); mime != "" {
			gc.ResponseMimeType = mime
			if len(schema) > 0 {
				gc.ResponseSchema = schema
			}
		}
	}

	if thinking := mapThinkingConfig(req); thinking != nil {
		gc.ThinkingConfig = thinking
	}

	// Only return when something was actually set.
	if gc.Temperature == nil && gc.TopP == nil && gc.MaxOutputTokens == nil &&
		gc.CandidateCount == nil && gc.Seed == nil && gc.PresencePenalty == nil &&
		gc.FrequencyPenalty == nil && len(gc.StopSequences) == 0 &&
		gc.ResponseMimeType == "" && len(gc.ResponseSchema) == 0 &&
		len(gc.ThinkingConfig) == 0 {
		return nil
	}
	return gc
}

// mapResponseFormat converts the canonical response_format object into
// Gemini's responseMimeType / responseSchema pair.
func mapResponseFormat(format map[string]interface{}) (mime string, schema map[string]interface{}) {
	typ, _ := format["type"].(string)
	switch typ {
	case "json_object":
		return "application/json", nil
	case "json_schema":
		if jss, ok := format["json_schema"].(map[string]interface{}); ok {
			if s, ok := jss["schema"].(map[string]interface{}); ok {
				return "application/json", s
			}
		}
		if s, ok := format["schema"].(map[string]interface{}); ok {
			return "application/json", s
		}
		return "application/json", nil
	default:
		if typ != "" {
			return "application/json", nil
		}
		return "", nil
	}
}

func mapThinkingConfig(req *apitypes.ChatCompletionRequest) map[string]interface{} {
	out := map[string]interface{}{}

	if req.ThinkingBudget != nil {
		out["thinkingBudget"] = *req.ThinkingBudget
		if req.IncludeReasoning != nil && *req.IncludeReasoning {
			out["includeThoughts"] = true
		}
	}

	if req.Reasoning != nil {
		rc := req.Reasoning
		if rc.MaxTokens != nil {
			out["thinkingBudget"] = *rc.MaxTokens
		}
		effort := rc.Effort
		if effort == "" {
			effort = req.ReasoningEffort
		}
		if _, hasBudget := out["thinkingBudget"]; !hasBudget {
			if lvl := mapThinkingLevelEffort(effort); lvl != "" {
				out["thinkingLevel"] = lvl
			} else if isReasoningDisabled(effort) {
				out["thinkingBudget"] = 0
			}
		}
		if rc.Exclude != nil {
			out["includeThoughts"] = !*rc.Exclude
		} else if rc.Enabled != nil && !*rc.Enabled {
			out["thinkingBudget"] = 0
		}
	} else if req.ReasoningEffort != "" {
		if lvl := mapThinkingLevelEffort(req.ReasoningEffort); lvl != "" {
			out["thinkingLevel"] = lvl
		} else if isReasoningDisabled(req.ReasoningEffort) {
			out["thinkingBudget"] = 0
		}
	}

	if req.IncludeReasoning != nil && *req.IncludeReasoning {
		out["includeThoughts"] = true
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// mapThinkingLevelEffort maps effort levels to Gemini thinking levels.
func mapThinkingLevelEffort(effort string) string {
	switch effort {
	case "low", "minimal":
		return "LOW"
	case "medium", "auto", "adaptive":
		return "MEDIUM"
	case "high", "max", "xhigh":
		return "HIGH"
	default:
		return ""
	}
}

// isReasoningDisabled reports whether an effort shorthand means thinking off.
func isReasoningDisabled(effort string) bool {
	return effort == "none"
}

// imageURLToBlob converts a data: URL into Gemini inlineData.
func imageURLToBlob(raw string) (*geminiBlob, bool) {
	if !strings.HasPrefix(raw, "data:") {
		return nil, false
	}
	rest := strings.TrimPrefix(raw, "data:")
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return nil, false
	}
	meta := rest[:comma]
	data := rest[comma+1:]
	if data == "" || !strings.Contains(meta, "base64") {
		return nil, false
	}
	mediaType, _, _ := strings.Cut(meta, ";")
	if mediaType == "" {
		return nil, false
	}
	return &geminiBlob{MimeType: mediaType, Data: data}, true
}

// fileURLToFileData converts a gs:// or files.goog URL into a fileData ref.
func fileURLToFileData(raw string) *geminiFileData {
	if strings.HasPrefix(raw, "gs://") {
		return &geminiFileData{FileURI: raw}
	}
	if strings.Contains(raw, "files.googl.com") || strings.Contains(raw, "generativelanguage.googleapis.com/v1beta/files/") {
		return &geminiFileData{FileURI: raw}
	}
	return nil
}
