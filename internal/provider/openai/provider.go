package openai

import (
	"context"
	"time"

	"github.com/novexa/gateway/internal/provider"
	"github.com/novexa/gateway/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for OpenAI.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new OpenAI provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("openai", apiKey, baseURL, timeout, openaibase.WithPricing(openaiPricing)),
	}
}

func openaiPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"gpt-4o": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0025,
			OutputPrice: 0.010,
			Currency:    "USD",
		},
		"gpt-4o-2024-08-06": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0025,
			OutputPrice: 0.010,
			Currency:    "USD",
		},
		"gpt-4o-mini": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00015,
			OutputPrice: 0.0006,
			Currency:    "USD",
		},
		"gpt-4-turbo": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.010,
			OutputPrice: 0.030,
			Currency:    "USD",
		},
		"gpt-3.5-turbo": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0005,
			OutputPrice: 0.0015,
			Currency:    "USD",
		},
		"text-embedding-3-small": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00002,
			OutputPrice: 0.00002,
			Currency:    "USD",
		},
		"text-embedding-3-large": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00013,
			OutputPrice: 0.00013,
			Currency:    "USD",
		},
	}, nil
}
