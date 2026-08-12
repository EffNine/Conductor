package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/EffNine/conductor/internal/tool"
)

// Tool reads a file from the filesystem within the configured root.
type Tool struct {
	root     string
	maxBytes int
}

// New creates a read_file tool bounded to root.
func New(root string, maxBytes int) *Tool {
	if maxBytes <= 0 {
		maxBytes = 65536
	}
	return &Tool{
		root:     filepath.Clean(root),
		maxBytes: maxBytes,
	}
}

func (t *Tool) Name() string { return "read_file" }

func (t *Tool) Description() string {
	return "Read the contents of a file at the given path. Path must be relative to the workspace root."
}

func (t *Tool) Params() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative path to the file to read.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *Tool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return tool.Failure("parse args: " + err.Error()), nil
	}
	if input.Path == "" {
		return tool.Failure("path is required"), nil
	}

	target, err := t.resolve(input.Path)
	if err != nil {
		return tool.Failure(err.Error()), nil
	}

	f, err := os.Open(target)
	if err != nil {
		return tool.Failure("open " + input.Path + ": " + err.Error()), nil
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return tool.Failure("stat " + input.Path + ": " + err.Error()), nil
	}
	if info.IsDir() {
		return tool.Failure("path is a directory: " + input.Path), nil
	}
	if info.Size() > int64(t.maxBytes) {
		return tool.Failure(fmt.Sprintf("file exceeds max size (%d bytes): %s", t.maxBytes, input.Path)), nil
	}

	// Read with context cancellation via a goroutine.
	data := make([]byte, info.Size())
	done := make(chan struct{})
	var readErr error
	go func() {
		defer close(done)
		_, readErr = io.ReadFull(f, data)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return tool.Failure(ctx.Err().Error()), nil
	}
	if readErr != nil {
		return tool.Failure("read " + input.Path + ": " + readErr.Error()), nil
	}
	return tool.Success(string(data)), nil
}

func (t *Tool) resolve(rel string) (string, error) {
	// Reject absolute paths.
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", rel)
	}
	// Clean and prevent traversal outside root.
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
