package generic

import (
	"time"

	"github.com/novexa/gateway/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for generic OpenAI-compatible endpoints.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new generic provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("generic", apiKey, baseURL, timeout),
	}
}
