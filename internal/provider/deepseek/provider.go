package deepseek

import (
	"context"
	"time"

	"github.com/novexa/gateway/internal/provider"
	"github.com/novexa/gateway/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for DeepSeek.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new DeepSeek provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("deepseek", apiKey, baseURL, timeout, openaibase.WithPricing(deepseekPricing)),
	}
}

func deepseekPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"deepseek-chat": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00007,
			OutputPrice: 0.0011,
			Currency:    "USD",
		},
		"deepseek-reasoner": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00014,
			OutputPrice: 0.00219,
			Currency:    "USD",
		},
	}, nil
}
