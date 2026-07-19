package groq

import (
	"context"
	"time"

	"github.com/novexa/gateway/internal/provider"
	"github.com/novexa/gateway/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for Groq.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new Groq provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("groq", apiKey, baseURL, timeout, openaibase.WithPricing(groqPricing)),
	}
}

func groqPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{
		"llama-3.1-70b-versatile": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00059,
			OutputPrice: 0.00079,
			Currency:    "USD",
		},
		"llama-3.1-8b-instant": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00005,
			OutputPrice: 0.00008,
			Currency:    "USD",
		},
		"mixtral-8x7b-32768": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00024,
			OutputPrice: 0.00024,
			Currency:    "USD",
		},
		"gemma-2-9b-it": {
			UnitType:    provider.UnitToken,
			UnitSize:    1000,
			InputPrice:  0.00000,
			OutputPrice: 0.00000,
			Currency:    "USD",
		},
	}, nil
}
