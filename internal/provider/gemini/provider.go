package gemini

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for Gemini.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new Gemini provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("gemini", apiKey, baseURL, timeout, openaibase.WithPricing(geminiPricing)),
	}
}

func geminiPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"gemini-1.5-pro": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0035,
			OutputPrice: 0.0105,
			Currency:    "USD",
		},
		"gemini-1.5-flash": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00035,
			OutputPrice: 0.00105,
			Currency:    "USD",
		},
		"gemini-1.5-flash-8b": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.000075,
			OutputPrice: 0.0003,
			Currency:    "USD",
		},
	}, nil
}
