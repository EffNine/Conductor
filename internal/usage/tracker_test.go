package usage_test

import (
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/usage"
	"go.uber.org/zap"
)

func TestTrackerPersistsExtraCounters(t *testing.T) {
	db := openTestDB(t)
	reg := provider.NewRegistry()
	est := usage.NewEstimator(reg, nil)
	tracker := usage.NewTracker(db, est, zap.NewNop())

	tracker.Record(&usage.Record{
		RequestID:        "req-1",
		ModelID:          "whisper-1",
		ProviderModelID:  "whisper-1",
		Provider:         "openai",
		PromptTokens:     0,
		CompletionTokens: 0,
		TotalTokens:      0,
		Requests:         1,
		DurationMs:       1500,
		InputChars:       42,
		OutputChars:      0,
		LatencyMs:        1500,
		StatusCode:       200,
		CreatedAt:        time.Now().UTC(),
	})

	var saved database.UsageRecord
	if err := db.DB.First(&saved, "request_id = ?", "req-1").Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if saved.Requests != 1 {
		t.Fatalf("Requests = %d, want 1", saved.Requests)
	}
	if saved.DurationMs != 1500 {
		t.Fatalf("DurationMs = %d, want 1500", saved.DurationMs)
	}
	if saved.InputChars != 42 {
		t.Fatalf("InputChars = %d, want 42", saved.InputChars)
	}
	if saved.OutputChars != 0 {
		t.Fatalf("OutputChars = %d, want 0", saved.OutputChars)
	}
	if saved.PromptTokens != 0 || saved.CompletionTokens != 0 {
		t.Fatalf("token fields should be zero for non-token usage")
	}
}

func TestTrackerAggregatesByProviderAndModel(t *testing.T) {
	db := openTestDB(t)
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "openai",
		pricing: map[string]provider.PricingInfo{
			"gpt-4o": {
				UnitType:    provider.UnitToken,
				UnitSize:    1000,
				InputPrice:  1.0,
				OutputPrice: 2.0,
				Currency:    "USD",
			},
		},
	})
	est := usage.NewEstimator(reg, nil)
	tracker := usage.NewTracker(db, est, zap.NewNop())

	now := time.Now().UTC()
	tracker.Record(&usage.Record{
		RequestID: "a", ModelID: "gpt-4o", ProviderModelID: "gpt-4o", Provider: "openai",
		PromptTokens: 1000, CompletionTokens: 0, TotalTokens: 1000, Requests: 1,
		StatusCode: 200, CreatedAt: now,
	})
	tracker.Record(&usage.Record{
		RequestID: "b", ModelID: "gpt-4o", ProviderModelID: "gpt-4o", Provider: "openai",
		PromptTokens: 1000, CompletionTokens: 0, TotalTokens: 1000, Requests: 1,
		StatusCode: 200, CreatedAt: now,
	})
	tracker.Record(&usage.Record{
		RequestID: "c", ModelID: "llama3", ProviderModelID: "llama3", Provider: "ollama",
		PromptTokens: 500, CompletionTokens: 0, TotalTokens: 500, Requests: 1,
		StatusCode: 200, CreatedAt: now,
	})

	summary, err := tracker.Aggregate(usage.AggregateQuery{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if summary.Total.PromptTokens != 2500 {
		t.Fatalf("Total.PromptTokens = %d, want 2500", summary.Total.PromptTokens)
	}
	if summary.Total.Requests != 3 {
		t.Fatalf("Total.Requests = %d, want 3", summary.Total.Requests)
	}
	if summary.ByProvider["openai"].PromptTokens != 2000 {
		t.Fatalf("openai PromptTokens = %d, want 2000", summary.ByProvider["openai"].PromptTokens)
	}
	if summary.ByModel["llama3"].PromptTokens != 500 {
		t.Fatalf("llama3 PromptTokens = %d, want 500", summary.ByModel["llama3"].PromptTokens)
	}
	if summary.ByProvider["openai"].CostUSD == nil || *summary.ByProvider["openai"].CostUSD != 2.0 {
		t.Fatalf("openai CostUSD = %v, want 2.0", summary.ByProvider["openai"].CostUSD)
	}
	if summary.ByProvider["ollama"].CostUSD != nil {
		t.Fatalf("ollama CostUSD should be unknown/nil, got %v", *summary.ByProvider["ollama"].CostUSD)
	}
}

func openTestDB(t *testing.T) *database.Database {
	t.Helper()
	db, err := database.Connect(&config.DatabaseConfig{
		Driver:       "sqlite",
		DSN:          "file:" + t.Name() + "?mode=memory&cache=shared",
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
