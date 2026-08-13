package router

import (
	"context"
	"strings"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/policy"
)

// PipelineStage is the interface that every decision pipeline stage must implement.
// Each stage receives a mutable DecisionContext and produces typed outputs that
// are stored back into the context by the pipeline orchestrator.
type PipelineStage interface {
	// Name returns the human-readable stage name.
	Name() string

	// Execute runs the stage logic. Errors abort the pipeline.
	Execute(ctx context.Context, dc *DecisionContext) error
}

// IntentStage resolves the request intent (task type, confidence, description).
type IntentStage struct{}

// NewIntentStage creates a new IntentStage.
func NewIntentStage() *IntentStage { return &IntentStage{} }

func (s *IntentStage) Name() string { return "intent" }

func (s *IntentStage) Execute(_ context.Context, dc *DecisionContext) error {
	taskText := ""
	if req := dc.Request(); req != nil && len(req.Messages) > 0 {
		taskText = req.Messages[0].ContentString()
	}
	intent := classifyIntent(taskText)
	policyIntent := &policy.Intent{
		TaskType:    taskTypeFromProfile(intent.Profile),
		Confidence:  intent.Confidence,
		Description: intent.Description,
		Metadata:    intent.Metadata,
	}
	dc.SetIntent(policyIntent)
	return nil
}

type intentResult struct {
	Profile     string
	Confidence  float64
	Description string
	Metadata    map[string]any
}

func classifyIntent(text string) *intentResult {
	lower := strings.ToLower(text)

	// Vision is the most specific signal.
	if matchesAny(lower, []string{
		"image", "picture", "screenshot", "vision", "look at", "describe this",
		"what is in", "what's in", "diagram", "chart", "photo",
	}) {
		return &intentResult{Profile: "vision", Confidence: 0.8, Description: "image or visual content understanding"}
	}

	// Elite / complex agentic coding.
	if matchesAny(lower, []string{"implement", "refactor", "architect", "design a system", "build a", "create a full", "end-to-end", "multi-step", "complex"}) &&
		matchesAny(lower, []string{"code", "function", "api", "service", "module", "app", "application", "system", "distributed", "microservice", "backend", "infrastructure"}) {
		return &intentResult{Profile: "elite", Confidence: 0.75, Description: "complex agentic coding task requiring multi-step execution"}
	}

	// Coding tasks.
	if matchesAny(lower, []string{
		"code", "coding", "program", "function", "debug", "fix", "refactor",
		"implementation", "script", "algorithm", "test case", "unit test",
		"pull request", "commit", "git", "repo", "repository", "syntax",
		"compile", "build error", "runtime error", "stack trace", "exception",
		"write a", "create a", "build a",
	}) {
		return &intentResult{Profile: "coding", Confidence: 0.7, Description: "code generation, debugging, or refactoring"}
	}

	// Reasoning / analysis.
	if matchesAny(lower, []string{
		"analyze", "compare", "evaluate", "explain", "reason", "why", "how does",
		"trade-off", "tradeoff", "pros and cons", "advantages", "disadvantages",
		"summarize and", "step by step", "prove", "derive", "solve",
	}) {
		return &intentResult{Profile: "reasoning", Confidence: 0.65, Description: "analysis, comparison, or multi-step logical reasoning"}
	}

	// Fast / trivial tasks.
	if matchesAny(lower, []string{
		"hi", "hello", "hey", "quick", "short", "brief", "one sentence",
		"one word", "simple", "just", "only", "greeting", "thank", "thanks",
	}) {
		return &intentResult{Profile: "fast", Confidence: 0.6, Description: "short, simple request requiring minimal processing"}
	}

	return &intentResult{Profile: "default", Confidence: 0.3, Description: "general task with no strong signal"}
}

func matchesAny(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func taskTypeFromProfile(profile string) policy.TaskType {
	switch profile {
	case "elite", "coding":
		return policy.TaskTypeCode
	case "reasoning":
		return policy.TaskTypeReasoning
	case "vision":
		return policy.TaskTypeVision
	case "fast":
		return policy.TaskTypeChat
	default:
		return policy.TaskTypeChat
	}
}

// CapabilityStage resolves the capability requirements of a request.
type CapabilityStage struct{}

func NewCapabilityStage() *CapabilityStage { return &CapabilityStage{} }

func (s *CapabilityStage) Name() string { return "capability" }

func (s *CapabilityStage) Execute(_ context.Context, dc *DecisionContext) error {
	hint := ExtractCapabilityHint(dc.Request())
	cr := &policy.CapabilityRequirement{
		NeedsStreaming:   hint.Streaming,
		NeedsVision:      hint.Vision,
		NeedsReasoning:   hint.Reasoning,
		NeedsToolCalling: hint.ToolCalling,
		NeedsStructured:  hint.Structured,
	}
	dc.SetCapability(cr)
	return nil
}

// CandidateStage generates provider candidates for the request.
type CandidateStage struct {
	engine *RouterEngine
}

func NewCandidateStage(engine *RouterEngine) *CandidateStage {
	return &CandidateStage{engine: engine}
}

func (s *CandidateStage) Name() string { return "candidate" }

func (s *CandidateStage) Execute(ctx context.Context, dc *DecisionContext) error {
	if s.engine == nil || dc.Request() == nil {
		return nil
	}
	hint := ExtractCapabilityHint(dc.Request())
	scores := s.engine.GetProviderScores(hint)
	if len(scores) == 0 {
		return nil
	}
	dc.SetCandidateScores(scores)
	return nil
}

// SelectionStage performs the final provider selection.
type SelectionStage struct {
	engine *RouterEngine
}

func NewSelectionStage(engine *RouterEngine) *SelectionStage {
	return &SelectionStage{engine: engine}
}

func (s *SelectionStage) Name() string { return "selection" }

func (s *SelectionStage) Execute(_ context.Context, dc *DecisionContext) error {
	if s.engine == nil {
		return nil
	}
	req := dc.Request()
	if req == nil {
		req = &apitypes.ChatCompletionRequest{}
	}
	result, err := s.engine.SelectBestProvider(dc.Context(), dc.TaskMetadata().ModelID, req)
	if err != nil {
		return dc.Err("selection failed", err)
	}
	if result == nil {
		return nil
	}
	dc.SetSelection(result)
	return nil
}
