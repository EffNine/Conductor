package usage

import (
	"context"

	"github.com/novexa/gateway/internal/config"
	"github.com/novexa/gateway/internal/provider"
)

// CostSource identifies how a cost was determined.
type CostSource string

const (
	CostSourceActual  CostSource = "actual"
	CostSourcePricing CostSource = "pricing"
	CostSourceManual  CostSource = "manual"
	CostSourceUnknown CostSource = "unknown"
)

// CostInput is the usage data needed to estimate cost.
type CostInput struct {
	Provider         string
	ProviderModelID  string
	PromptTokens     int
	CompletionTokens int
	Requests         int
	DurationMs       int64
	InputChars       int
	OutputChars      int
	ActualCostUSD    *float64 // Provider-reported per-request cost, if any
}

// CostResult is the outcome of cost resolution.
type CostResult struct {
	CostUSD *float64
	Source  CostSource
}

// ManualRate is a configured fallback Cost Rate.
type ManualRate struct {
	Provider        string
	ProviderModelID string
	UnitType        provider.UnitType
	UnitSize        int64
	InputPrice      float64
	OutputPrice     float64
}

// Estimator resolves cost using provider pricing, then manual rates.
type Estimator struct {
	registry *provider.Registry
	manual   []ManualRate
}

// NewEstimator creates a Cost Estimator. manual may be nil.
func NewEstimator(registry *provider.Registry, manual []ManualRate) *Estimator {
	return &Estimator{registry: registry, manual: manual}
}

// Estimate resolves cost for the given usage.
// Precedence: actual → provider GetPricing → manual rates → unknown.
func (e *Estimator) Estimate(ctx context.Context, in CostInput) (CostResult, error) {
	if in.ActualCostUSD != nil {
		v := *in.ActualCostUSD
		return CostResult{CostUSD: &v, Source: CostSourceActual}, nil
	}

	if e.registry != nil {
		if p, ok := e.registry.Get(in.Provider); ok {
			pricing, err := p.GetPricing(ctx)
			if err == nil {
				if info, ok := pricing[in.ProviderModelID]; ok {
					cost := applyPricing(info, in)
					return CostResult{CostUSD: &cost, Source: CostSourcePricing}, nil
				}
			}
		}
	}

	for _, rate := range e.manual {
		if rate.Provider == in.Provider && rate.ProviderModelID == in.ProviderModelID {
			info := provider.PricingInfo{
				UnitType:    rate.UnitType,
				UnitSize:    rate.UnitSize,
				InputPrice:  rate.InputPrice,
				OutputPrice: rate.OutputPrice,
			}
			cost := applyPricing(info, in)
			return CostResult{CostUSD: &cost, Source: CostSourceManual}, nil
		}
	}

	return CostResult{Source: CostSourceUnknown}, nil
}

// ManualRatesFromConfig converts config cost rates into estimator manual rates.
func ManualRatesFromConfig(cfg *config.Config) []ManualRate {
	if cfg == nil || len(cfg.Cost.Rates) == 0 {
		return nil
	}
	out := make([]ManualRate, 0, len(cfg.Cost.Rates))
	for _, r := range cfg.Cost.Rates {
		unitType := provider.UnitType(r.UnitType)
		if unitType == "" {
			unitType = provider.UnitToken
		}
		unitSize := r.UnitSize
		if unitSize <= 0 {
			unitSize = 1000
		}
		out = append(out, ManualRate{
			Provider:        r.Provider,
			ProviderModelID: r.ProviderModelID,
			UnitType:        unitType,
			UnitSize:        unitSize,
			InputPrice:      r.InputPrice,
			OutputPrice:     r.OutputPrice,
		})
	}
	return out
}

func applyPricing(info provider.PricingInfo, in CostInput) float64 {
	unitSize := info.UnitSize
	if unitSize <= 0 {
		unitSize = 1
	}

	switch info.UnitType {
	case provider.UnitRequest:
		return float64(in.Requests) * info.InputPrice / float64(unitSize)
	case provider.UnitMinute:
		minutes := float64(in.DurationMs) / 60000.0
		return minutes * info.InputPrice / float64(unitSize)
	case provider.UnitCharacter:
		input := float64(in.InputChars) / float64(unitSize) * info.InputPrice
		output := float64(in.OutputChars) / float64(unitSize) * info.OutputPrice
		return input + output
	default: // UnitToken
		input := float64(in.PromptTokens) / float64(unitSize) * info.InputPrice
		output := float64(in.CompletionTokens) / float64(unitSize) * info.OutputPrice
		return input + output
	}
}
