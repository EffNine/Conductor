package openai

import (
	"github.com/EffNine/conductor/internal/provider"
)

// isRetryableError determines if an error is retryable.
func isRetryableError(err *provider.ProviderError) bool {
	if err == nil {
		return false
	}
	switch err.Type {
	case provider.ErrorTypeRateLimit,
		provider.ErrorTypeProviderUnavailable,
		provider.ErrorTypeServerError:
		return true
	default:
		return false
	}
}
