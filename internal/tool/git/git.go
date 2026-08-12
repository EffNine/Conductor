package git

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/tool"
)

// readOps holds the set of git subcommands this tool permits.
var readOps = map[string]bool{
	"status": true,
	"diff":   true,
	"log":    true,
}

// Tool runs read-only git operations within a repository root.
type Tool struct {
	repoRoot string
	maxBytes int
}

// Config holds parameters for the git tool.
type Config struct {
	RepoRoot string
	MaxOutput int
}

// New creates a git tool bounded to repoRoot.
func New(cfg Config) *Tool {
	t := &Tool{
		maxBytes: cfg.MaxOutput,
	}
	if cfg.RepoRoot != "" {
		t.repoRoot = filepath.Clean(cfg.RepoRoot)
	} else {
		var err error
		t.repoRoot, err = exec.LookPath("git")
		if err != nil || t.repoRoot == "" {
			t.repoRoot = "."
		}
	}
	if t.maxBytes <= 0 {
		t.maxBytes = 65536
	}
	return t
}

func (t *Tool) nameFor(op string) string {
	return "git_" + op
}

func (t *Tool) DescriptionFor(op string) string {
	descriptions := map[string]string{
		"status": "Show the working tree status.",
		"diff":   "Show changes between commits, commit and working tree, etc.",
		"log":    "Show commit logs.",
	}
	if d, ok := descriptions[op]; ok {
		return d
	}
	return "Run git " + op
}

func (t *Tool) ParamsFor(op string) map[string]any {
	props := map[string]any{
		"args": map[string]any{
			"type":        "string",
			"description": "Additional arguments to pass to git " + op + ".",
		},
	}
	switch op {
	case "status":
		props["short"] = map[string]any{
			"type":        "boolean",
			"description": "Use short format.",
		}
	case "diff":
		props["cached"] = map[string]any{
			"type":        "boolean",
			"description": "Show cached changes.",
		}
	case "log":
		props["oneline"] = map[string]any{
			"type":        "boolean",
			"description": "Use oneline format.",
		}
		props["max_count"] = map[string]any{
			"type":        "integer",
			"description": "Limit number of commits.",
		}
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
	}
}

// RegisterTools registers all git subtools under the given registry with
// names like git_status, git_diff, git_log.
func RegisterTools(reg *tool.Registry, cfg Config) error {
	gitTool := New(cfg)
	for op := range readOps {
		name := gitTool.nameFor(op)
		// Create a per-operation wrapper.
		wrapper := &opWrapper{
			parent: gitTool,
			op:     op,
		}
		if err := reg.Register(wrapper); err != nil {
			return fmt.Errorf("register %s: %w", name, err)
		}
	}
	return nil
}

type opWrapper struct {
	parent *Tool
	op     string
}

func (w *opWrapper) Name() string { return w.parent.nameFor(w.op) }

func (w *opWrapper) Description() string { return w.parent.DescriptionFor(w.op) }

func (w *opWrapper) Params() map[string]any { return w.parent.ParamsFor(w.op) }

func (w *opWrapper) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var input struct {
		Args     string `json:"args"`
		Short    bool   `json:"short"`
		Cached   bool   `json:"cached"`
		Oneline  bool   `json:"oneline"`
		MaxCount int    `json:"max_count"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return tool.Failure("parse args: " + err.Error()), nil
	}

	// Build git command args.
	gitArgs := []string{w.op}
	switch w.op {
	case "status":
		if input.Short {
			gitArgs = append(gitArgs, "-s")
		}
	case "diff":
		if input.Cached {
			gitArgs = append(gitArgs, "--cached")
		}
		if input.Args != "" {
			gitArgs = append(gitArgs, strings.Fields(input.Args)...)
		}
	case "log":
		if input.Oneline {
			gitArgs = append(gitArgs, "--oneline")
		}
		if input.MaxCount > 0 {
			gitArgs = append(gitArgs, fmt.Sprintf("-n%d", input.MaxCount))
		}
		if input.Args != "" {
			gitArgs = append(gitArgs, strings.Fields(input.Args)...)
		}
	}

	if input.Args != "" && w.op != "diff" && w.op != "log" {
		gitArgs = append(gitArgs, strings.Fields(input.Args)...)
	}

	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Dir = w.parent.repoRoot
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	latency := time.Since(start)

	out := stdout.String()
	if len(out) > w.parent.maxBytes {
		out = out[:w.parent.maxBytes] + "...[truncated]"
	}
	errOut := stderr.String()
	if len(errOut) > w.parent.maxBytes {
		errOut = errOut[:w.parent.maxBytes] + "...[truncated]"
	}

	if err != nil {
		result := out
		if errOut != "" {
			if result != "" {
				result += "\n"
			}
			result += "[stderr] " + errOut
		}
		result += fmt.Sprintf("\n[exit code %d, latency %dms]", getExitCode(err), latency.Milliseconds())
		return tool.Failure(result), nil
	}
	return tool.Success(out), nil
}

func getExitCode(err error) int {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}
