package xai

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for xAI.
// xAI exposes an OpenAI-compatible API (https://api.x.ai/v1).
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new xAI provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("xai", apiKey, baseURL, timeout, openaibase.WithPricing(xaiPricing)),
	}
}

// xaiPricing returns known public xAI token rates in USD per 1000 tokens.
// Rates are sourced from https://docs.x.ai/docs/models and may change; operators
// can override or extend via manual cost.rates in config.yaml.
func xaiPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"grok-3": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.003,
			OutputPrice: 0.015,
			Currency:    "USD",
		},
		"grok-3-fast": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.005,
			OutputPrice: 0.025,
			Currency:    "USD",
		},
		"grok-3-mini": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0003,
			OutputPrice: 0.0005,
			Currency:    "USD",
		},
		"grok-3-mini-fast": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0006,
			OutputPrice: 0.001,
			Currency:    "USD",
		},
		"grok-2": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.002,
			OutputPrice: 0.010,
			Currency:    "USD",
		},
		"grok-2-vision": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.002,
			OutputPrice: 0.010,
			Currency:    "USD",
		},
	}, nil
}
