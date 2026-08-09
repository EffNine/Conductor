package anthropic

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/EffNine/conductor/internal/provider"
)

// newRequest creates an HTTP request with Anthropic-specific auth headers.
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

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.auth.APIKey)
	httpReq.Header.Set("anthropic-version", p.auth.APIVersion)

	for _, header := range p.auth.BetaHeaders {
		if header != "" {
			httpReq.Header.Add("anthropic-beta", header)
		}
	}

	return httpReq, nil
}

// handleErrorResponse reads the body and returns a normalized ProviderError.
func (p *Provider) handleErrorResponse(resp *http.Response) *provider.ProviderError {
	return MapError(p.name, resp)
}
