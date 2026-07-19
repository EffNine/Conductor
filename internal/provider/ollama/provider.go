package ollama

import (
	"time"

	"github.com/novexa/gateway/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for Ollama.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new Ollama provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("ollama", apiKey, baseURL, timeout),
	}
}
