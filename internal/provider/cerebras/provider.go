package cerebras

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for Cerebras.
// Cerebras exposes an OpenAI-compatible API at https://api.cerebras.ai/v1.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new Cerebras provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	if baseURL == "" {
		baseURL = "https://api.cerebras.ai/v1"
	}
	return &Provider{
		Base: openaibase.New("cerebras", apiKey, baseURL, timeout, openaibase.WithPricing(cerebrasPricing)),
	}
}

func cerebrasPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"gpt-oss-120b": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0006,
			OutputPrice: 0.0006,
			Currency:    "USD",
		},
		"gemma-4-31b": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0004,
			OutputPrice: 0.0004,
			Currency:    "USD",
		},
		"zai-glm-4.7": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00005,
			OutputPrice: 0.00005,
			Currency:    "USD",
		},
	}, nil
}
