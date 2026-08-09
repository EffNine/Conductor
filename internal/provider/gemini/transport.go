package gemini

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/EffNine/conductor/internal/provider"
)

// newRequest builds an HTTP request with Gemini auth headers applied.
func (p *Provider) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, bodyReader)
	if err != nil {
		return nil, provider.NewProviderError(p.name, http.StatusInternalServerError,
			provider.ErrorTypeServerError, "failed to create request", err)
	}

	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	p.auth.ApplyAuthHeaders(func(k, v string) {
		httpReq.Header.Set(k, v)
	})

	return httpReq, nil
}

// generateContentPath builds the non-streaming endpoint path.
func (p *Provider) generateContentPath(modelID string) string {
	return "/models/" + modelIDPath(modelID) + ":generateContent"
}

// streamGenerateContentPath builds the streaming SSE endpoint path.
func (p *Provider) streamGenerateContentPath(modelID string) string {
	return "/models/" + modelIDPath(modelID) + ":streamGenerateContent?alt=sse"
}

// modelIDPath normalizes a model id for use in a URL path segment. Google
// accepts both bare ids and "models/"-prefixed resource names.
func modelIDPath(modelID string) string {
	return strings.TrimPrefix(modelID, "models/")
}

// mapError converts an HTTP error response to a normalized ProviderError.
func (p *Provider) mapError(resp *http.Response) *provider.ProviderError {
	return MapError(p.name, resp)
}

// handleErrorResponse reads the body and returns a normalized ProviderError.
func (p *Provider) handleErrorResponse(resp *http.Response) *provider.ProviderError {
	return MapError(p.name, resp)
}