package orchestration

import (
	"context"

	"github.com/EffNine/conductor/internal/policy"
	"github.com/EffNine/conductor/internal/router"
)

// CapabilityRequirement describes what the task execution needs.
type CapabilityRequirement struct {
	NeedsFileSystem  bool
	NeedsShell       bool
	NeedsGit         bool
	NeedsReasoning   bool
	NeedsVision      bool
	NeedsToolCalling bool
	NeedsStreaming   bool
	MaxTokens        int
}

// ResolveCapabilities determines the capability requirements for a task.
func ResolveCapabilities(ctx context.Context, input string, intent *Intent, toolNames []string) *CapabilityRequirement {
	_ = ctx
	req := &CapabilityRequirement{}

	// Detect filesystem needs from tool availability and intent.
	if intent != nil && (intent.TaskType == string(router.ModeCoding) || intent.TaskType == string(router.ModeElite)) {
		req.NeedsFileSystem = true
	}
	for _, name := range toolNames {
		switch name {
		case "read_file", "write_file", "list_files", "edit_file":
			req.NeedsFileSystem = true
		case "shell_exec", "run_command":
			req.NeedsShell = true
		case "git_add", "git_commit", "git_diff", "git_status":
			req.NeedsGit = true
		}
	}

	// Infer from intent type.
	switch intent.TaskType {
	case string(router.ModeReasoning), string(router.ModeElite):
		req.NeedsReasoning = true
	case string(router.ModeVision):
		req.NeedsVision = true
	}

	// Tool calling is needed whenever tools are available.
	req.NeedsToolCalling = len(toolNames) > 0

	return req
}

// ToPolicyCapability converts orchestration capability requirements to policy form.
func ToPolicyCapability(req *CapabilityRequirement) *policy.CapabilityRequirement {
	return &policy.CapabilityRequirement{
		NeedsStreaming:   req.NeedsStreaming,
		NeedsVision:      req.NeedsVision,
		NeedsReasoning:   req.NeedsReasoning,
		NeedsToolCalling: req.NeedsToolCalling,
		NeedsStructured:  false,
		MaxTokens:        req.MaxTokens,
	}
}
