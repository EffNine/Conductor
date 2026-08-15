package zai

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for Z.AI (01.AI).
// Z.AI exposes an OpenAI-compatible API at https://api.z.ai/api/paas/v4.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new Z.AI provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	if baseURL == "" {
		baseURL = "https://api.z.ai/api/paas/v4"
	}
	return &Provider{
		Base: openaibase.New("zai", apiKey, baseURL, timeout, openaibase.WithPricing(zaiPricing)),
	}
}

func zaiPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"glm-5.2": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0014,
			OutputPrice: 0.0044,
			Currency:    "USD",
		},
		"glm-4.5": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0014,
			OutputPrice: 0.0044,
			Currency:    "USD",
		},
	}, nil
}
