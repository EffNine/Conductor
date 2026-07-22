package openrouter

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for OpenRouter.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new OpenRouter provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("openrouter", apiKey, baseURL, timeout, openaibase.WithPricing(openrouterPricing)),
	}
}

func openrouterPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"openai/gpt-4o": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.005,
			OutputPrice: 0.015,
			Currency:    "USD",
		},
		"anthropic/claude-3.5-sonnet": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.003,
			OutputPrice: 0.015,
			Currency:    "USD",
		},
		"deepseek/deepseek-chat": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0007,
			OutputPrice: 0.0011,
			Currency:    "USD",
		},
	}, nil
}
