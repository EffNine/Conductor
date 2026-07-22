package opencode

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for OpenCode Zen.
// Chat completions work for Zen models served via /v1/chat/completions
// (Grok, DeepSeek, MiniMax, GLM, Kimi, etc.). GPT models use /responses and
// Claude models use /messages — those need dedicated adapters later.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new OpenCode provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("opencode", apiKey, baseURL, timeout, openaibase.WithPricing(opencodePricing)),
	}
}

// Prices from https://opencode.ai/docs/zen/ (USD per 1M tokens), converted to
// per-1000-token UnitSize used by the cost estimator.
func opencodePricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"gpt-5.6-sol": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.005,
			OutputPrice: 0.030,
			Currency:    "USD",
		},
		"gpt-5.6-terra": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0025,
			OutputPrice: 0.015,
			Currency:    "USD",
		},
		"gpt-5.6-luna": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.001,
			OutputPrice: 0.006,
			Currency:    "USD",
		},
		"grok-4.5": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.002,
			OutputPrice: 0.006,
			Currency:    "USD",
		},
		"grok-build-0.1": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.001,
			OutputPrice: 0.002,
			Currency:    "USD",
		},
		"deepseek-v4-pro": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00174,
			OutputPrice: 0.00348,
			Currency:    "USD",
		},
		"deepseek-v4-flash": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00014,
			OutputPrice: 0.00028,
			Currency:    "USD",
		},
		"minimax-m2.7": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0003,
			OutputPrice: 0.0012,
			Currency:    "USD",
		},
		"glm-5.2": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0014,
			OutputPrice: 0.0044,
			Currency:    "USD",
		},
		"kimi-k2.6": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00095,
			OutputPrice: 0.004,
			Currency:    "USD",
		},
	}, nil
}
