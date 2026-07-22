package agnesai

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
)

// Provider implements the provider.Provider interface for Agnes AI.
// Agnes AI exposes an OpenAI-compatible API (https://apihub.agnes-ai.com/v1).
type Provider struct {
	*openaibase.Base
}

// NewProvider creates a new Agnes AI provider.
func NewProvider(apiKey, baseURL string, timeout time.Duration) *Provider {
	return &Provider{
		Base: openaibase.New("agnesai", apiKey, baseURL, timeout, openaibase.WithPricing(agnesAIPricing)),
	}
}

// agnesAIPricing returns an empty map because Agnes AI does not publish
// per-token pricing yet. Operators can configure manual cost.rates in config.yaml.
func agnesAIPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{}, nil
}
