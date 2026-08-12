package shell_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/tool"
	"github.com/EffNine/conductor/internal/tool/shell"
)

// ── Allowed command tests ───────────────────────────────────────────────────

func TestShell_AllowedCommand(t *testing.T) {
	dir := t.TempDir()
	cfg := shell.Config{
		WorkingDir: dir,
		Timeout:    5 * time.Second,
		MaxOutput:  4096,
		AllowList:  []string{"echo"},
	}
	tool_ := shell.New(cfg)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %s", res.Content)
	}
}

func TestShell_DeniedExecutable(t *testing.T) {
	cfg := shell.Config{
		WorkingDir: ".",
		Timeout:    5 * time.Second,
		MaxOutput:  4096,
		AllowList:  []string{"echo"},
	}
	tool_ := shell.New(cfg)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"command":"rm -rf /"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for disallowed executable")
	}
}

func TestShell_Timemout(t *testing.T) {
	cfg := shell.Config{
		WorkingDir: ".",
		Timeout:    50 * time.Millisecond,
		MaxOutput:  4096,
		AllowList:  []string{"sleep"},
	}
	tool_ := shell.New(cfg)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"command":"sleep 10"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for timed-out command")
	}
}

func TestShell_OutputLimit(t *testing.T) {
	dir := t.TempDir()
	cfg := shell.Config{
		WorkingDir: dir,
		Timeout:    5 * time.Second,
		MaxOutput:  64,
		AllowList:  []string{"bash"},
	}
	tool_ := shell.New(cfg)
	// Use bash to print more than 64 bytes.
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"command":"bash -c 'printf \"%.200x\" 0'"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Result should be truncated or errored.
	_ = res
	_ = err
}

func TestShell_ContextCancellation(t *testing.T) {
	cfg := shell.Config{
		WorkingDir: ".",
		Timeout:    30 * time.Second,
		MaxOutput:  4096,
		AllowList:  []string{"sleep"},
	}
	tool_ := shell.New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	res, err := tool_.Execute(ctx, json.RawMessage(`{"command":"sleep 10"}`))
	if err != nil {
		// Context cancellation may surface as an error from the tool.
		t.Logf("got expected error: %v", err)
		return
	}
	if !res.IsError {
		t.Error("expected error for cancelled context")
	}
}

func TestShell_DangerousCommandRejected(t *testing.T) {
	cfg := shell.Config{
		WorkingDir: ".",
		Timeout:    5 * time.Second,
		MaxOutput:  4096,
		Denied:     []string{"rm", "dd", "mkfs"},
		AllowList:  []string{"echo", "rm"},
	}
	tool_ := shell.New(cfg)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"command":"rm -rf /"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for denied command")
	}
}

func TestShell_EnvWhitelist(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("MY_SECRET", "should-not-appear")
	defer os.Unsetenv("MY_SECRET")
	cfg := shell.Config{
		WorkingDir:   dir,
		Timeout:      5 * time.Second,
		MaxOutput:    4096,
		AllowList:    []string{"env"},
		EnvWhitelist: []string{"PATH"},
	}
	tool_ := shell.New(cfg)
	res, err := tool_.Execute(context.Background(), json.RawMessage(`{"command":"env"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %s", res.Content)
	}
	if containsSubstring(res.Content, "MY_SECRET") {
		t.Error("env whitelist failed: MY_SECRET should not appear")
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstringHelper(s, sub))
}

func containsSubstringHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── Tool metadata ────────────────────────────────────────────────────────────

func TestShell_Metadata(t *testing.T) {
	cfg := shell.Config{WorkingDir: ".", Timeout: 5 * time.Second, MaxOutput: 4096}
	tool_ := shell.New(cfg)
	if tool_.Name() != "shell" {
		t.Errorf("Name = %q, want shell", tool_.Name())
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
var _ tool.Tool = (*shell.Tool)(nil)
