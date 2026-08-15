package mistral

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for Mistral AI.
// Mistral exposes an OpenAI-compatible API at https://api.mistral.ai/v1.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new Mistral provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	if baseURL == "" {
		baseURL = "https://api.mistral.ai/v1"
	}
	return &Provider{
		Base: openaibase.New("mistral", apiKey, baseURL, timeout, openaibase.WithPricing(mistralPricing)),
	}
}

func mistralPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"mistral-large-latest": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0005,
			OutputPrice: 0.0015,
			Currency:    "USD",
		},
		"mistral-small-latest": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0001,
			OutputPrice: 0.0003,
			Currency:    "USD",
		},
		"ministral-3b-latest": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00004,
			OutputPrice: 0.00004,
			Currency:    "USD",
		},
		"ministral-8b-latest": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0001,
			OutputPrice: 0.0001,
			Currency:    "USD",
		},
		"codestral-latest": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0003,
			OutputPrice: 0.0009,
			Currency:    "USD",
		},
		"codestral-2508": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0003,
			OutputPrice: 0.0009,
			Currency:    "USD",
		},
		"mistral-embed": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0001,
			OutputPrice: 0.0,
			Currency:    "USD",
		},
	}, nil
}
