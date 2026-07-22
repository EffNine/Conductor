package provider

import (
	"context"
	"errors"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
)

// ErrNotImplemented is returned by provider methods that are not yet implemented.
var ErrNotImplemented = errors.New("not implemented")

// UnitType represents the billing unit for a pricing entry.
type UnitType string

const (
	UnitToken     UnitType = "token"
	UnitRequest   UnitType = "request"
	UnitMinute    UnitType = "minute"
	UnitCharacter UnitType = "character"
)

// ModelInfo represents a model available from a provider.
type ModelInfo struct {
	ProviderModelID string // The exact model ID used in provider API calls
	ModelID         string // Optional user-facing model ID (may differ from ProviderModelID)
	OwnedBy         string // Organization or entity that owns the model
}

// PricingInfo represents pricing for a model.
type PricingInfo struct {
	UnitType    UnitType // The billing unit (token, request, minute, character)
	UnitSize    int64    // Units covered by InputPrice/OutputPrice (e.g. 1000 for per-1K-token rates; 0 or 1 = per unit)
	InputPrice  float64  // Price for UnitSize input units (USD)
	OutputPrice float64  // Price for UnitSize output units (USD)
	Currency    string   // Currency code (default: USD)
}

// Provider defines the interface that all AI providers must implement
type Provider interface {
	// Name returns the provider identifier (e.g., "openai", "anthropic")
	Name() string

	// ChatCompletion sends a non-streaming chat completion request
	ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error)

	// ChatCompletionStream sends a streaming chat completion request
	// Returns a channel that emits OpenAI-compatible stream chunks
	ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error)

	// Embeddings sends an embeddings request
	Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error)

	// ListModels returns models available from this provider
	ListModels(ctx context.Context) ([]ModelInfo, error)

	// GetPricing returns pricing information for models available from this provider.
	// The map key is the ProviderModelID.
	GetPricing(ctx context.Context) (map[string]PricingInfo, error)

	// HealthCheck pings the provider and returns health status
	HealthCheck(ctx context.Context) (*HealthStatus, error)

	// SupportsModel returns true if this provider can handle the given model ID
	SupportsModel(modelID string) bool
}

// HealthStatus represents the health status of a provider
type HealthStatus struct {
	Provider  string    `json:"provider"`
	IsHealthy bool      `json:"is_healthy"`
	LatencyMs int64     `json:"latency_ms"`
	LastError string    `json:"last_error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// ProviderError represents a normalized provider error
type ProviderError struct {
	Provider   string `json:"provider"`
	StatusCode int    `json:"status_code"`
	Type       string `json:"type"`
	Message    string `json:"message"`
	Err        error  `json:"-"`
}

func (e *ProviderError) Error() string {
	return e.Message
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// Error types
const (
	ErrorTypeInvalidRequest      = "invalid_request_error"
	ErrorTypeAuthentication      = "authentication_error"
	ErrorTypeRateLimit           = "rate_limit_error"
	ErrorTypeServerError         = "server_error"
	ErrorTypeProviderUnavailable = "provider_unavailable"
	ErrorTypeContextLength       = "context_length_exceeded"
)

// NewProviderError creates a new ProviderError
func NewProviderError(provider string, statusCode int, errType string, message string, err error) *ProviderError {
	return &ProviderError{
		Provider:   provider,
		StatusCode: statusCode,
		Type:       errType,
		Message:    message,
		Err:        err,
	}
}
