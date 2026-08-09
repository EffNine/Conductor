package gemini

import (
	"fmt"

	"github.com/EffNine/conductor/internal/provider"
)

// AuthConfig holds Gemini authentication configuration.
type AuthConfig struct {
	APIKey string
}

// NewAuthConfig creates an AuthConfig for the given API key.
func NewAuthConfig(apiKey string) *AuthConfig {
	return &AuthConfig{APIKey: apiKey}
}

// ApplyAuthHeaders writes Gemini auth headers using a setter callback.
func (a *AuthConfig) ApplyAuthHeaders(setHeader func(key, value string)) {
	if setHeader == nil {
		return
	}
	if a != nil && a.APIKey != "" {
		setHeader("x-goog-api-key", a.APIKey)
	}
}

// ValidateAuth checks that an API key is configured.
func ValidateAuth(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("Gemini API key is required")
	}
	return nil
}

// BuildAuthHeader is provided for compatibility with other adapters; Gemini
// uses the x-goog-api-key header rather than an Authorization header.
func BuildAuthHeader(apiKey string) string {
	return apiKey
}

// isRetryableError reports whether a normalized error can be retried.
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
