package requesty

import (
	"time"

	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for Requesty.
// Requesty exposes an OpenAI-compatible API at https://router.requesty.ai/v1.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new Requesty provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	if baseURL == "" {
		baseURL = "https://router.requesty.ai/v1"
	}
	return &Provider{
		Base: openaibase.New("requesty", apiKey, baseURL, timeout),
	}
}
