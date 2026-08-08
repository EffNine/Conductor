package openai

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/EffNine/conductor/internal/provider"
)

// newRequest creates an HTTP request with proper auth and content-type headers.
func (p *Provider) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, bodyReader)
	if err != nil {
		return nil, provider.NewProviderError(p.name, 500,
			provider.ErrorTypeServerError, "failed to create request", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	return httpReq, nil
}

// mapError converts an HTTP error response to a normalized ProviderError.
func (p *Provider) mapError(resp *http.Response) *provider.ProviderError {
	return MapError(p.name, resp)
}

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

// openaiAuthError maps OpenAI auth error codes to canonical error types.
func openaiAuthError(code string) string {
	switch code {
	case "invalid_api_key", "invalid_user":
		return provider.ErrorTypeAuthentication
	case "authentication_required":
		return provider.ErrorTypeAuthentication
	default:
		return provider.ErrorTypeAuthentication
	}
}

// openaiRateLimitError maps OpenAI rate limit error codes.
func openaiRateLimitError(code string) bool {
	switch code {
	case "rate_limit_exceeded", "insufficient_quota":
		return true
	default:
		return false
	}
}
