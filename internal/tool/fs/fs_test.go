package fs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EffNine/conductor/internal/tool"
	"github.com/EffNine/conductor/internal/tool/fs"
)

// ── ReadFile tests ───────────────────────────────────────────────────────────

func TestReadFile_Allowed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(target, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool_ := fs.New(dir, 65536)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"path":"hello.txt"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Content != "hello world" {
		t.Errorf("content = %q, want %q", res.Content, "hello world")
	}
	if res.IsError {
		t.Error("IsError = true, want false")
	}
}

func TestReadFile_PathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	tool_ := fs.New(dir, 65536)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"path":"../etc/passwd"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for path traversal")
	}
}

func TestReadFile_OutsideRootRejected(t *testing.T) {
	dir := t.TempDir()
	tool_ := fs.New(dir, 65536)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"path":"/etc/passwd"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for absolute path")
	}
}

func TestReadFile_SizeLimit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "big.txt")
	data := make([]byte, 1024)
	for i := range data {
		data[i] = 'x'
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool_ := fs.New(dir, 512)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"path":"big.txt"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for oversized file")
	}
}

// ── WriteFile tests ──────────────────────────────────────────────────────────

func TestWriteFile_Allowed(t *testing.T) {
	dir := t.TempDir()
	tool_ := fs.NewWrite(dir, 1048576)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"path":"out.txt","content":"hi"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %s", res.Content)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(data) != "hi" {
		t.Errorf("content = %q, want %q", string(data), "hi")
	}
}

func TestWriteFile_PathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	tool_ := fs.NewWrite(dir, 1048576)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"path":"../evil.txt","content":"x"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for path traversal")
	}
}

func TestWriteFile_SizeLimit(t *testing.T) {
	dir := t.TempDir()
	tool_ := fs.NewWrite(dir, 128)
	big := make([]byte, 256)
	for i := range big {
		big[i] = 'x'
	}
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"path":"big.txt","content":"`+string(big)+`"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for oversized content")
	}
}

// ── Tool metadata tests ──────────────────────────────────────────────────────

func TestReadFile_Metadata(t *testing.T) {
	tool_ := fs.New("/tmp", 1024)
	if tool_.Name() != "read_file" {
		t.Errorf("Name = %q, want read_file", tool_.Name())
	}
	if tool_.Description() == "" {
		t.Error("Description is empty")
	}
	params := tool_.Params()
	if params == nil {
		t.Fatal("Params is nil")
	}
}

func TestWriteFile_Metadata(t *testing.T) {
	tool_ := fs.NewWrite("/tmp", 1024)
	if tool_.Name() != "write_file" {
		t.Errorf("Name = %q, want write_file", tool_.Name())
	}
	if tool_.Description() == "" {
		t.Error("Description is empty")
	}
	params := tool_.Params()
	if params == nil {
		t.Fatal("Params is nil")
	}
}

// compile-time check
var _ tool.Tool = (*fs.Tool)(nil)
var _ tool.Tool = (*fs.WriteTool)(nil)
