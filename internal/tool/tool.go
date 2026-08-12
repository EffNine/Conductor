package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// Tool represents a callable unit of work that an agent can invoke.
type Tool interface {
	// Name returns the tool identifier used in LLM function-calling.
	Name() string

	// Description returns a human-readable description of what the tool does.
	Description() string

	// Params returns the JSON Schema for the tool's input parameters.
	// May be nil or empty for tools with no arguments.
	Params() map[string]any

	// Execute runs the tool with the provided arguments.
	// args is a raw JSON object (e.g. {"path": "/etc/hosts"}).
	Execute(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

// ToolResult is the outcome of a tool execution.
type ToolResult struct {
	// Content is the primary output. Non-empty on success.
	Content string
	// IsError indicates the tool failed.
	IsError bool
}

// Success creates a successful ToolResult.
func Success(content string) ToolResult {
	return ToolResult{Content: content}
}

// Failure creates a failed ToolResult.
func Failure(content string) ToolResult {
	return ToolResult{Content: content, IsError: true}
}

// String converts a ToolResult to a human-readable string.
func (r ToolResult) String() string {
	if r.IsError {
		return fmt.Errorf("tool error: %s", r.Content).Error()
	}
	return r.Content
}

// MarshalJSON serializes ToolResult to JSON for LLM consumption.
func (r ToolResult) MarshalJSON() ([]byte, error) {
	if r.IsError {
		return json.Marshal(map[string]any{
			"content": r.Content,
			"error":   true,
		})
	}
	return json.Marshal(map[string]any{
		"content": r.Content,
		"error":   false,
	})
}
