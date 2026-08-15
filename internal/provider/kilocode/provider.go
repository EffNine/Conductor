package kilocode

import (
	"time"

	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for KiloCode.
// KiloCode exposes an OpenAI-compatible API at https://api.kilo.ai/api/gateway.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new KiloCode provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	if baseURL == "" {
		baseURL = "https://api.kilo.ai/api/gateway"
	}
	return &Provider{
		Base: openaibase.New("kilocode", apiKey, baseURL, timeout),
	}
}
