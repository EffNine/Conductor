package nousportal

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for Nous Portal.
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new Nous Portal provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("nous_portal", apiKey, baseURL, timeout, openaibase.WithPricing(nousPortalPricing)),
	}
}

func nousPortalPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	// Nous Portal is a subscription service; per-token rates are not published.
	// Operators can configure manual cost.rates or rely on per-request actual cost.
	return map[string]provider.PricingInfo{}, nil
}
