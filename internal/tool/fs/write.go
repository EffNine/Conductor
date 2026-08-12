package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EffNine/conductor/internal/tool"
)

// WriteTool writes a file to the filesystem within the configured root.
type WriteTool struct {
	root       string
	maxBytes   int
}

// NewWrite creates a write_file tool bounded to root.
func NewWrite(root string, maxBytes int) *WriteTool {
	if maxBytes <= 0 {
		maxBytes = 1048576
	}
	return &WriteTool{
		root:     filepath.Clean(root),
		maxBytes: maxBytes,
	}
}

func (t *WriteTool) Name() string { return "write_file" }

func (t *WriteTool) Description() string {
	return "Write content to a file at the given path. Path must be relative to the workspace root."
}

func (t *WriteTool) Params() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative path to the file to write.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file.",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteTool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return tool.Failure("parse args: " + err.Error()), nil
	}
	if input.Path == "" {
		return tool.Failure("path is required"), nil
	}
	if len(input.Content) > t.maxBytes {
		return tool.Failure(fmt.Sprintf("content exceeds max size (%d bytes)", t.maxBytes)), nil
	}

	target, err := t.resolve(input.Path)
	if err != nil {
		return tool.Failure(err.Error()), nil
	}

	// Ensure parent directory exists.
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return tool.Failure("create directory " + dir + ": " + err.Error()), nil
	}

	if err := os.WriteFile(target, []byte(input.Content), 0o644); err != nil {
		return tool.Failure("write " + input.Path + ": " + err.Error()), nil
	}
	return tool.Success(fmt.Sprintf("wrote %d bytes to %s", len(input.Content), input.Path)), nil
}

func (t *WriteTool) resolve(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", rel)
	}
	clean := filepath.Clean(rel)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("path traversal not allowed: %s", rel)
	}
	resolved := filepath.Join(t.root, clean)
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if !strings.HasPrefix(absResolved, t.root+string(filepath.Separator)) && absResolved != t.root {
		return "", fmt.Errorf("path traverses outside workspace root: %s", rel)
	}
	return absResolved, nil
}
