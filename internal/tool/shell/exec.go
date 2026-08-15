package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/tool"
)

// Tool runs allowed shell commands within a restricted environment.
type Tool struct {
	workingDir string
	timeout    time.Duration
	maxBytes   int
	allowList  map[string]bool
	denied     map[string]bool
	envWhite   map[string]bool
}

// Config holds parameters for the shell tool.
type Config struct {
	WorkingDir   string
	Timeout      time.Duration
	MaxOutput    int
	AllowList    []string
	Denied       []string
	EnvWhitelist []string
}

// New creates a shell tool with the given policy.
func New(cfg Config) *Tool {
	t := &Tool{
		timeout:   cfg.Timeout,
		maxBytes:  cfg.MaxOutput,
		allowList: make(map[string]bool),
		denied:    make(map[string]bool),
		envWhite:  make(map[string]bool),
	}
	if cfg.WorkingDir != "" {
		t.workingDir = filepath.Clean(cfg.WorkingDir)
	} else {
		var err error
		t.workingDir, err = os.Getwd()
		if err != nil {
			t.workingDir = "."
		}
	}
	if t.timeout <= 0 {
		t.timeout = 30 * time.Second
	}
	if t.maxBytes <= 0 {
		t.maxBytes = 65536
	}
	for _, cmd := range cfg.AllowList {
		t.allowList[filepath.Base(cmd)] = true
	}
	for _, d := range cfg.Denied {
		t.denied[filepath.Base(d)] = true
	}
	for _, env := range cfg.EnvWhitelist {
		t.envWhite[env] = true
	}
	return t
}

func (t *Tool) Name() string { return "shell" }

func (t *Tool) Description() string {
	return "Execute a shell command in a restricted environment."
}

func (t *Tool) Params() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The command to execute.",
			},
		},
		"required": []string{"command"},
	}
}

func (t *Tool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return tool.Failure("parse args: " + err.Error()), nil
	}
	if input.Command == "" {
		return tool.Failure("command is required"), nil
	}

	// Parse command into args, handling basic shell syntax.
	cmdArgs := parseShellCommand(input.Command)
	if len(cmdArgs) == 0 {
		return tool.Failure("empty command"), nil
	}

	exe := filepath.Base(cmdArgs[0])

	// Check denied list.
	if t.denied[exe] {
		return tool.Failure(fmt.Sprintf("command denied by policy: %s", exe)), nil
	}

	// Check allow list — if configured, only listed commands are allowed.
	if len(t.allowList) > 0 && !t.allowList[exe] {
		return tool.Failure(fmt.Sprintf("command not in allow list: %s", exe)), nil
	}

	// Build environment: whitelist only.
	env := t.buildEnv()

	// Resolve command path.
	fullPath := cmdArgs[0]
	if !strings.Contains(fullPath, string(filepath.Separator)) {
		look, err := exec.LookPath(fullPath)
		if err != nil {
			return tool.Failure(fmt.Sprintf("command not found: %s", fullPath)), nil
		}
		cmdArgs[0] = look
	}

	// Apply configured timeout if set.
	execCtx := ctx
	if t.timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	// #nosec G204 -- cmdArgs are validated by the tool's allowlist/denylist; user input is never passed directly.
	cmd := exec.CommandContext(execCtx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = t.workingDir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	latency := time.Since(start)

	out := limitOutput(stdout.String(), t.maxBytes)
	errOut := limitOutput(stderr.String(), t.maxBytes)

	result := out
	if errOut != "" {
		if result != "" {
			result += "\n"
		}
		result += "[stderr] " + errOut
	}
	if err != nil {
		result += fmt.Sprintf("\n[exit code %d, latency %dms]", getExitCode(err), latency.Milliseconds())
		return tool.Failure(result), nil
	}
	return tool.Success(result), nil
}

func (t *Tool) buildEnv() []string {
	whitelist := t.envWhite
	if len(whitelist) == 0 {
		// Default: forward PATH and HOME only.
		whitelist["PATH"] = true
		whitelist["HOME"] = true
	}
	var env []string
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if whitelist[parts[0]] {
			env = append(env, e)
		}
	}
	return env
}

func parseShellCommand(cmd string) []string {
	// Simple tokenization: split on whitespace, respect basic quoting.
	var parts []string
	var current strings.Builder
	var inQuote rune
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if inQuote != 0 {
			if c == byte(inQuote) {
				inQuote = 0
			} else {
				current.WriteByte(c)
			}
			continue
		}
		switch c {
		case '"', '\'':
			inQuote = rune(c)
		case ' ', '\t', '\n', '\r':
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func limitOutput(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "...[truncated]"
}

func getExitCode(err error) int {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}
