package anthropic

import (
	"encoding/json"
	"strings"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
)

// MapRequest converts a canonical ChatCompletionRequest to an Anthropic Messages API request.
func MapRequest(req *apitypes.ChatCompletionRequest) *anthropicMessagesRequest {
	if req == nil {
		return nil
	}

	if len(req.ResponseFormat) > 0 {
		return nil
	}

	out := &anthropicMessagesRequest{
		Model:     req.Model,
		MaxTokens: 1024,
		Messages:  make([]anthropicMessage, 0, len(req.Messages)),
	}

	if req.MaxTokens != nil {
		out.MaxTokens = *req.MaxTokens
	}
	if req.Temperature != nil {
		out.Temperature = req.Temperature
	}
	if req.TopP != nil {
		out.TopP = req.TopP
	}
	if req.Stream {
		out.Stream = true
	}
	if req.User != "" {
		out.User = req.User
	}

	if req.Stop != nil {
		switch v := req.Stop.(type) {
		case string:
			if v != "" {
				out.StopSequences = []string{v}
			}
		case []string:
			out.StopSequences = v
		}
	}

	var systemParts []string
	for _, m := range req.Messages {
		if m.Role == "system" {
			if s := m.ContentString(); s != "" {
				systemParts = append(systemParts, s)
			}
			continue
		}
	}
	if len(systemParts) > 0 {
		out.System = strings.Join(systemParts, "\n")
	}

	for _, m := range req.Messages {
		if m.Role == "system" {
			continue
		}
		out.Messages = append(out.Messages, mapMessage(m))
	}

	if len(req.Tools) > 0 {
		out.Tools = make([]anthropicTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			out.Tools = append(out.Tools, mapTool(t))
		}
	}

	if req.ToolChoice != nil {
		out.ToolChoice = mapToolChoice(req.ToolChoice)
	}

	if req.Reasoning != nil {
		out.Thinking = mapThinking(req.Reasoning)
	}

	return out
}

func mapMessage(m apitypes.Message) anthropicMessage {
	msg := anthropicMessage{
		Role: mapRole(m.Role),
	}

	switch v := m.Content.(type) {
	case string:
		if v != "" {
			msg.Content = []anthropicContentBlock{{Type: "text", Text: v}}
		}
	case []apitypes.ContentPart:
		blocks := make([]anthropicContentBlock, 0, len(v))
		for _, p := range v {
			blocks = append(blocks, mapContentPart(p))
		}
		if len(blocks) > 0 {
			msg.Content = blocks
		}
	case nil:
		msg.Content = []anthropicContentBlock{}
	}

	if len(m.ToolCalls) > 0 {
		blocks := make([]anthropicContentBlock, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, anthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: mapToolInput(tc.Function.Arguments),
			})
		}
		msg.Content = blocks
	}

	if m.ToolCallID != "" {
		content := ""
		if s, ok := m.Content.(string); ok {
			content = s
		}
		msg.Content = []anthropicContentBlock{{
			Type:      "tool_result",
			ToolUseID: m.ToolCallID,
			Content:   content,
			IsError:   false,
		}}
	}

	return msg
}

func mapRole(role string) string {
	switch role {
	case "assistant":
		return "assistant"
	case "user", "tool":
		return "user"
	default:
		return "user"
	}
}

func mapContentPart(p apitypes.ContentPart) anthropicContentBlock {
	switch p.Type {
	case apitypes.ContentPartText:
		return anthropicContentBlock{Type: "text", Text: p.Text}
	case apitypes.ContentPartImageURL:
		if p.ImageURL != nil && p.ImageURL.URL != "" {
			return imageURLToAnthropicBlock(p.ImageURL.URL)
		}
		return anthropicContentBlock{Type: "text", Text: p.Text}
	default:
		return anthropicContentBlock{Type: "text", Text: p.Text}
	}
}

func mapToolInput(arguments string) map[string]any {
	if arguments == "" {
		return map[string]any{}
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return map[string]any{}
	}
	return input
}

func mapTool(t apitypes.Tool) anthropicTool {
	params := provider.StripSchemaMetaFields(t.Function.Parameters)
	if params != nil {
		params = provider.NormalizeSchema(params, provider.NormalizeForAnthropic)
	}
	return anthropicTool{
		Name:        t.Function.Name,
		Description: t.Function.Description,
		InputSchema: params,
	}
}

func mapToolChoice(tc interface{}) interface{} {
	switch v := tc.(type) {
	case string:
		if v == "none" || v == "auto" {
			return v
		}
		return map[string]interface{}{
			"type": "tool",
			"name": v,
		}
	case map[string]interface{}:
		if t, ok := v["type"]; ok {
			switch t {
			case "tool":
				return v
			case "auto", "none":
				return t
			}
		}
		if n, ok := v["name"].(string); ok {
			return map[string]interface{}{
				"type": "tool",
				"name": n,
			}
		}
		return v
	default:
		return nil
	}
}

func mapThinking(rc *apitypes.ReasoningConfig) map[string]interface{} {
	if rc == nil {
		return nil
	}
	result := map[string]interface{}{}
	if rc.MaxTokens != nil {
		result["budget_tokens"] = *rc.MaxTokens
	}
	if rc.Exclude != nil && *rc.Exclude {
		result["type"] = "disabled"
	} else if rc.Enabled != nil && !*rc.Enabled {
		result["type"] = "disabled"
	} else {
		result["type"] = "enabled"
	}
	return result
}

func imageURLToAnthropicBlock(rawURL string) anthropicContentBlock {
	if mediaType, data, ok := parseDataURL(rawURL); ok {
		return anthropicContentBlock{
			Type: "image",
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: mediaType,
				Data:      data,
			},
		}
	}
	return anthropicContentBlock{
		Type: "image",
		Source: &anthropicImageSource{
			Type: "url",
			URL:  rawURL,
		},
	}
}

func parseDataURL(raw string) (mediaType, data string, ok bool) {
	if !strings.HasPrefix(raw, "data:") {
		return "", "", false
	}
	rest := strings.TrimPrefix(raw, "data:")
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", "", false
	}
	meta := rest[:comma]
	data = rest[comma+1:]
	if data == "" || !strings.Contains(meta, "base64") {
		return "", "", false
	}
	mediaType, _, _ = strings.Cut(meta, ";")
	if mediaType == "" {
		return "", "", false
	}
	return mediaType, data, true
}

// Anthropic-specific internal types (must not escape adapter boundary).

type anthropicMessagesRequest struct {
	Model         string                 `json:"model"`
	Messages      []anthropicMessage     `json:"messages"`
	System        string                 `json:"system,omitempty"`
	MaxTokens     int                    `json:"max_tokens"`
	Temperature   *float64               `json:"temperature,omitempty"`
	TopP          *float64               `json:"top_p,omitempty"`
	StopSequences []string               `json:"stop_sequences,omitempty"`
	Stream        bool                   `json:"stream,omitempty"`
	User          string                 `json:"user,omitempty"`
	Tools         []anthropicTool        `json:"tools,omitempty"`
	ToolChoice    interface{}            `json:"tool_choice,omitempty"`
	Thinking      map[string]interface{} `json:"thinking,omitempty"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicContentBlock struct {
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     map[string]any        `json:"input,omitempty"`
	Thinking  string                `json:"thinking,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   string                `json:"content,omitempty"`
	IsError   bool                  `json:"is_error,omitempty"`
	Source    *anthropicImageSource `json:"source,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type anthropicMessageResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Model      string             `json:"model"`
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      usage              `json:"usage"`
}

type anthropicContent struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Input    map[string]any `json:"input,omitempty"`
	Thinking string         `json:"thinking,omitempty"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
