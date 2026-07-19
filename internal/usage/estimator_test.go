package usage_test

import (
	"context"
	"testing"

	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/provider"
	"github.com/novexa/gateway/internal/usage"
)

func TestEstimatorUsesProviderPricing(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "openai",
		pricing: map[string]provider.PricingInfo{
			"gpt-4o": {
				UnitType:    provider.UnitToken,
				UnitSize:    1000,
				InputPrice:  0.0025,
				OutputPrice: 0.010,
				Currency:    "USD",
			},
		},
	})

	est := usage.NewEstimator(reg, nil)
	result, err := est.Estimate(context.Background(), usage.CostInput{
		Provider:         "openai",
		ProviderModelID:  "gpt-4o",
		PromptTokens:     1000,
		CompletionTokens: 1000,
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if result.CostUSD == nil {
		t.Fatal("expected CostUSD, got nil")
	}
	want := 0.0125
	if *result.CostUSD != want {
		t.Fatalf("CostUSD = %v, want %v", *result.CostUSD, want)
	}
	if result.Source != usage.CostSourcePricing {
		t.Fatalf("Source = %q, want %q", result.Source, usage.CostSourcePricing)
	}
}

func TestEstimatorPrefersActualCostOverPricing(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "openai",
		pricing: map[string]provider.PricingInfo{
			"gpt-4o": {
				UnitType:    provider.UnitToken,
				UnitSize:    1000,
				InputPrice:  0.0025,
				OutputPrice: 0.010,
				Currency:    "USD",
			},
		},
	})

	actual := 0.42
	est := usage.NewEstimator(reg, nil)
	result, err := est.Estimate(context.Background(), usage.CostInput{
		Provider:         "openai",
		ProviderModelID:  "gpt-4o",
		PromptTokens:     1000,
		CompletionTokens: 1000,
		ActualCostUSD:    &actual,
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if result.CostUSD == nil || *result.CostUSD != actual {
		t.Fatalf("CostUSD = %v, want %v", result.CostUSD, actual)
	}
	if result.Source != usage.CostSourceActual {
		t.Fatalf("Source = %q, want %q", result.Source, usage.CostSourceActual)
	}
}

func TestEstimatorFallsBackToManualRate(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "ollama",
		err:  provider.ErrNotImplemented,
	})

	est := usage.NewEstimator(reg, []usage.ManualRate{{
		Provider:        "ollama",
		ProviderModelID: "llama3",
		UnitType:        provider.UnitToken,
		UnitSize:        1000,
		InputPrice:      0.001,
		OutputPrice:     0.002,
	}})

	result, err := est.Estimate(context.Background(), usage.CostInput{
		Provider:         "ollama",
		ProviderModelID:  "llama3",
		PromptTokens:     1000,
		CompletionTokens: 500,
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if result.CostUSD == nil {
		t.Fatal("expected CostUSD, got nil")
	}
	want := 0.002 // 1000/1000*0.001 + 500/1000*0.002
	if *result.CostUSD != want {
		t.Fatalf("CostUSD = %v, want %v", *result.CostUSD, want)
	}
	if result.Source != usage.CostSourceManual {
		t.Fatalf("Source = %q, want %q", result.Source, usage.CostSourceManual)
	}
}

func TestEstimatorReturnsUnknownWhenNoRate(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "generic",
		err:  provider.ErrNotImplemented,
	})

	est := usage.NewEstimator(reg, nil)
	result, err := est.Estimate(context.Background(), usage.CostInput{
		Provider:         "generic",
		ProviderModelID:  "custom-model",
		PromptTokens:     100,
		CompletionTokens: 50,
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if result.CostUSD != nil {
		t.Fatalf("CostUSD = %v, want nil", *result.CostUSD)
	}
	if result.Source != usage.CostSourceUnknown {
		t.Fatalf("Source = %q, want %q", result.Source, usage.CostSourceUnknown)
	}
}

type stubProvider struct {
	name    string
	pricing map[string]provider.PricingInfo
	err     error
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.pricing, nil
}

func (s *stubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}

func (s *stubProvider) SupportsModel(string) bool { return false }
