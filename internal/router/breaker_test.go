package router_test

import (
	"testing"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
)

func TestEngineBreakerPool(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{name: "openai"})
	reg.Register(&stubProvider{name: "groq"})

	engine := router.NewEngine(&config.Config{
		Circuit: config.CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 3,
			RecoveryTimeout:  10,
			SuccessThreshold: 2,
		},
		Routes: map[string]config.RouteConfig{
			"gpt-4o": {Provider: "openai"},
		},
	}, reg)

	pool := engine.BreakerPool()
	if pool == nil {
		t.Fatal("expected breaker pool")
	}

	resolved, err := engine.Resolve("gpt-4o")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Breaker == nil {
		t.Fatal("expected breaker on resolved route")
	}

	stats := pool.Stats()
	if len(stats) != 1 {
		t.Fatalf("expected 1 breaker, got %d", len(stats))
	}
	if _, ok := stats["openai"]; !ok {
		t.Fatalf("expected openai breaker")
	}
}

func TestEngineNoBreakerWhenDisabled(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{name: "openai"})

	engine := router.NewEngine(&config.Config{
		Circuit: config.CircuitBreakerConfig{
			Enabled: false,
		},
		Routes: map[string]config.RouteConfig{
			"gpt-4o": {Provider: "openai"},
		},
	}, reg)

	if engine.BreakerPool() != nil {
		t.Fatal("expected nil breaker pool when disabled")
	}

	resolved, err := engine.Resolve("gpt-4o")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Breaker != nil {
		t.Fatal("expected nil breaker when disabled")
	}
}
