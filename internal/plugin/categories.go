package plugin

import (
	"context"
)

// ProviderPlugin extends the base Plugin interface with provider-specific capabilities.
// Implementations add new upstream AI providers to the gateway.
type ProviderPlugin interface {
	Plugin

	// ProviderName returns the provider identifier (e.g., "openai", "anthropic").
	ProviderName() string

	// ChatCompletion sends a non-streaming chat completion request.
	// The request and response types are application-specific; implementations
	// should assert to the concrete API types in the plugin package.
	ChatCompletion(ctx context.Context, req any) (any, error)

	// ChatCompletionStream sends a streaming chat completion request.
	ChatCompletionStream(ctx context.Context, req any) (<-chan any, error)

	// ListModels returns models available from this provider.
	ListModels(ctx context.Context) ([]any, error)
}

// PolicyPlugin extends the base Plugin interface with policy-specific capabilities.
// Implementations provide intent resolution, capability matching, and request policies.
type PolicyPlugin interface {
	Plugin

	// ResolveIntent analyzes a request and returns the inferred intent.
	ResolveIntent(ctx context.Context, req any) (*IntentResult, error)

	// ResolveCapabilities analyzes a request and returns the capability requirements.
	ResolveCapabilities(ctx context.Context, req any) (*CapabilityResult, error)

	// ExecutePolicy runs a policy against a request and returns the result.
	ExecutePolicy(ctx context.Context, req any) (*PolicyResult, error)
}

// IntentResult represents the outcome of intent resolution.
type IntentResult struct {
	TaskType    string
	Confidence  float64
	Description string
	Metadata    map[string]any
}

// CapabilityResult represents the outcome of capability resolution.
type CapabilityResult struct {
	NeedsStreaming   bool
	NeedsVision      bool
	NeedsReasoning   bool
	NeedsToolCalling bool
	NeedsStructured  bool
	MaxTokens        int
	MinLatencyMs     int64
	CostCeiling      float64
}

// PolicyResult represents the outcome of policy execution.
type PolicyResult struct {
	Allowed       bool
	Modifications *RequestModifications
	Reason        string
}

// RequestModifications holds changes a policy wants to make to a request.
type RequestModifications struct {
	ModelID     string
	Temperature *float32
	MaxTokens   *int
	Stop        []string
	Tools       []any
}

// LearningPlugin extends the base Plugin interface with learning capabilities.
// Implementations provide adaptive routing, performance modeling, and self-optimization.
type LearningPlugin interface {
	Plugin

	// Record observes a routing decision and its outcome for learning.
	Record(ctx context.Context, decision DecisionRecord) error

	// ScoreCandidate evaluates a candidate provider for a given request context.
	ScoreCandidate(ctx context.Context, candidate CandidateScore) (float64, error)

	// GetModel returns the current learning model state.
	GetModel(ctx context.Context) (map[string]any, error)
}

// DecisionRecord captures a single routing decision for learning.
type DecisionRecord struct {
	RequestHash      string
	ModelID          string
	SelectedProvider string
	CandidateScores  map[string]float64
	Success          bool
	LatencyMs        int64
	Cost             float64
	Timestamp        int64
}

// CandidateScore holds scoring data for a single candidate.
type CandidateScore struct {
	ProviderName string
	ModelID      string
	HealthScore  float64
	LatencyMs    int64
	CostPerToken *float64
	Capabilities map[string]bool
	TotalScore   float64
}

// SchedulerPlugin extends the base Plugin interface with scheduling capabilities.
// Implementations add background jobs to the gateway's job registry.
type SchedulerPlugin interface {
	Plugin

	// RegisterJobs registers background jobs with the scheduler.
	RegisterJobs(ctx context.Context, reg JobRegistry) error
}

// JobRegistry is the interface for registering background jobs.
type JobRegistry interface {
	// Register adds a periodic job.
	Register(name string, interval uint64, fn func(context.Context) error) error

	// RegisterCron adds a cron-scheduled job.
	RegisterCron(name string, spec string, fn func(context.Context) error) error

	// Deregister removes a job by name.
	Deregister(name string) error
}

// DashboardPlugin extends the base Plugin interface with dashboard capabilities.
// Implementations add API endpoints and dashboard features.
type DashboardPlugin interface {
	Plugin

	// RegisterRoutes registers HTTP routes with the dashboard router.
	RegisterRoutes(ctx context.Context, router RouteRegistry) error
}

// RouteRegistry is the interface for registering HTTP routes.
type RouteRegistry interface {
	// Get registers a GET route.
	Get(path string, handler func(context.Context) (any, error)) error

	// Post registers a POST route.
	Post(path string, handler func(context.Context) (any, error)) error

	// Group creates a route group with a common prefix.
	Group(prefix string) SubRouteRegistry
}

// SubRouteRegistry is a route registry within a group.
type SubRouteRegistry interface {
	Get(path string, handler func(context.Context) (any, error)) error
	Post(path string, handler func(context.Context) (any, error)) error
}

// ToolPlugin extends the base Plugin interface with tool capabilities.
// Implementations provide request/response transformation tools.
type ToolPlugin interface {
	Plugin

	// TransformRequest modifies a chat completion request before execution.
	TransformRequest(ctx context.Context, req any) (any, error)

	// TransformResponse modifies a chat completion response after execution.
	TransformResponse(ctx context.Context, resp any) (any, error)

	// TransformStream modifies stream chunks as they pass through.
	TransformStream(ctx context.Context, chunk any) (any, error)
}
