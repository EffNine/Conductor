// Package policy defines interfaces for intent resolution and capability matching.
//
// These interfaces provide the foundation for future policy engine, adaptive
// routing, and intelligent request handling. No business logic is implemented here.
package policy

import (
	"context"

	"github.com/EffNine/conductor/internal/apitypes"
)

// Intent represents the inferred purpose of a request.
type Intent struct {
	TaskType    TaskType
	Confidence  float64
	Description string
	Metadata    map[string]any
}

// TaskType represents the classified task type.
type TaskType string

const (
	TaskTypeChat        TaskType = "chat"
	TaskTypeCompletion  TaskType = "completion"
	TaskTypeEmbedding   TaskType = "embedding"
	TaskTypeVision      TaskType = "vision"
	TaskTypeReasoning   TaskType = "reasoning"
	TaskTypeToolCalling TaskType = "tool_calling"
	TaskTypeCode        TaskType = "code"
	TaskTypeCreative    TaskType = "creative"
)

// IntentResolver determines the intent of a request.
type IntentResolver interface {
	// Resolve analyzes the request and returns the inferred intent.
	Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*Intent, error)

	// ResolveBatch analyzes multiple requests and returns their intents.
	ResolveBatch(ctx context.Context, reqs []*apitypes.ChatCompletionRequest) ([]*Intent, error)
}

// CapabilityRequirement describes what a request needs from a provider.
type CapabilityRequirement struct {
	NeedsStreaming   bool
	NeedsVision      bool
	NeedsReasoning   bool
	NeedsToolCalling bool
	NeedsStructured  bool
	MaxTokens        int
	MinLatencyMs     int64
	CostCeiling      float64
}

// CapabilityResolver determines what capabilities a request requires.
type CapabilityResolver interface {
	// Resolve analyzes the request and returns the capability requirements.
	Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*CapabilityRequirement, error)

	// CheckProviderCapabilities verifies if a provider can satisfy the requirements.
	CheckProviderCapabilities(ctx context.Context, req *CapabilityRequirement, providerName string) bool
}

// Policy defines the contract for request policies.
type Policy interface {
	// Name returns the policy name.
	Name() string

	// Execute runs the policy against a request.
	Execute(ctx context.Context, req *apitypes.ChatCompletionRequest) (*PolicyResult, error)
}

// PolicyResult represents the outcome of a policy execution.
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
	Tools       []apitypes.Tool
}
