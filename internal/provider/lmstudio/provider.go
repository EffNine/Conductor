package lmstudio

import (
	"time"

	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for LM Studio.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new LM Studio provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("lmstudio", apiKey, baseURL, timeout),
	}
}
