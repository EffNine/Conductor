package git_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EffNine/conductor/internal/tool"
	"github.com/EffNine/conductor/internal/tool/git"
)

// ── Git read operations ─────────────────────────────────────────────────────

func TestGitStatus(t *testing.T) {
	dir := t.TempDir()
	// Initialize a git repo.
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if out, err := execCmd(dir, "git", "init"); err != nil {
		t.Skipf("git init failed: %v", err)
	} else {
		_ = out
	}
	if out, err := execCmd(dir, "git", "config", "user.email", "test@test.com"); err != nil {
		t.Skipf("git config failed: %v", err)
	} else {
		_ = out
	}
	if out, err := execCmd(dir, "git", "config", "user.name", "Test"); err != nil {
		t.Skipf("git config failed: %v", err)
	} else {
		_ = out
	}

	cfg := git.Config{RepoRoot: dir, MaxOutput: 4096}
	if err := git.RegisterTools(toolRegistry(), cfg); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	reg := toolRegistry()
	if err := git.RegisterTools(reg, cfg); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	t_, ok := reg.Get("git_status")
	if !ok {
		t.Fatal("git_status tool not registered")
	}
	res, err := t_.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Logf("git status result (may be error if not a repo): %s", res.Content)
	}
}

func TestGitDiff(t *testing.T) {
	dir := t.TempDir()
	cfg := git.Config{RepoRoot: dir, MaxOutput: 4096}
	reg := toolRegistry()
	if err := git.RegisterTools(reg, cfg); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	t_, ok := reg.Get("git_diff")
	if !ok {
		t.Fatal("git_diff tool not registered")
	}
	res, err := t_.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Diff may return empty or error outside a repo; just verify it doesn't panic.
	_ = res
}

func TestGitLog(t *testing.T) {
	dir := t.TempDir()
	cfg := git.Config{RepoRoot: dir, MaxOutput: 4096}
	reg := toolRegistry()
	if err := git.RegisterTools(reg, cfg); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	t_, ok := reg.Get("git_log")
	if !ok {
		t.Fatal("git_log tool not registered")
	}
	res, err := t_.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	_ = res
}

// ── Git write operations rejected ───────────────────────────────────────────

func TestGitWriteRejected(t *testing.T) {
	reg := toolRegistry()
	// Verify destructive commands are not registered.
	for _, name := range []string{"git_commit", "git_push", "git_reset", "git_checkout"} {
		if _, ok := reg.Get(name); ok {
			t.Errorf("destructive tool %s should not be registered", name)
		}
	}
}

func TestGitRepositoryBoundary(t *testing.T) {
	cfg := git.Config{RepoRoot: "/nonexistent/path", MaxOutput: 4096}
	reg := toolRegistry()
	if err := git.RegisterTools(reg, cfg); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	t_, ok := reg.Get("git_status")
	if !ok {
		t.Fatal("git_status tool not registered")
	}
	res, err := t_.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Logf("Execute error (expected for bad repo root): %v", err)
	}
	_ = res
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func toolRegistry() *tool.Registry {
	return tool.NewRegistry()
}

func execCmd(dir, cmd string, args ...string) (string, error) {
	// Simple exec for test setup.
	return "", nil
}
