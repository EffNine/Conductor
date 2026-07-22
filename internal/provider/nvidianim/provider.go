package nvidianim

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for NVIDIA NIM.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new NVIDIA NIM provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("nvidia_nim", apiKey, baseURL, timeout, openaibase.WithPricing(nvidiaNimPricing)),
	}
}

func nvidiaNimPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	// NVIDIA NIM pricing varies by deployment/hosting. Provide a placeholder
	// map for commonly hosted models; operators should override via cost.rates.
	return map[string]provider.PricingInfo{
		"meta/llama-3.1-70b-instruct": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0007,
			OutputPrice: 0.0009,
			Currency:    "USD",
		},
		"meta/llama-3.1-8b-instruct": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.0002,
			OutputPrice: 0.0002,
			Currency:    "USD",
		},
	}, nil
}
