package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

func TestTelemetryVisibleToNextRoutingDecision(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	pOpenAI, _ := reg.Get("openai")
	pGroq, _ := reg.Get("groq")
	_ = store.Register(runtime.NewProviderRuntime("openai", pOpenAI))
	_ = store.Register(runtime.NewProviderRuntime("groq", pGroq))
	manager := runtime.NewManager(store)

	// Record telemetry: openai has successful executions, groq has failures.
	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.RecordExecutionOutcome(true, 0)
		r.RecordToolCallOutcome(true)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.RecordExecutionOutcome(false, 1)
		r.RecordToolCallOutcome(false)
		return nil
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "hello"},
		},
	}
	env := router.Environment{
		CircuitBreakerEnabled: true,
	}
	cfgSnap := router.ConfigSnapshot{
		Weights: config.DefaultRoutingWeights(),
	}

	result, err := pipeline.Execute(context.Background(), req, env, cfgSnap, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	snap := manager.Snapshot(context.Background())
	if snap.Providers["openai"].ExecutionSuccessCount != 1 {
		t.Errorf("openai expected 1 execution success, got %d", snap.Providers["openai"].ExecutionSuccessCount)
	}
	if snap.Providers["groq"].ExecutionFailureCount != 1 {
		t.Errorf("groq expected 1 execution failure, got %d", snap.Providers["groq"].ExecutionFailureCount)
	}
}

func TestPipelineStillUsesSingleRuntimeSnapshot(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	pOpenAI, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", pOpenAI))
	manager := runtime.NewManager(store)

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "test", nil)
		return nil
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "hello"},
		},
	}
	cfgSnap := router.ConfigSnapshot{Weights: config.DefaultRoutingWeights()}

	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, cfgSnap, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	snap := manager.Snapshot(context.Background())
	if snap.GlobalState.TotalProviders != 1 {
		t.Errorf("expected 1 provider, got %d", snap.GlobalState.TotalProviders)
	}
	if snap.Providers["openai"].State != runtime.StateHealthy {
		t.Errorf("expected openai healthy, got %s", snap.Providers["openai"].State)
	}
}

func TestExistingModesRoutingUnchanged(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	pOpenAI, _ := reg.Get("openai")
	pGroq, _ := reg.Get("groq")
	_ = store.Register(runtime.NewProviderRuntime("openai", pOpenAI))
	_ = store.Register(runtime.NewProviderRuntime("groq", pGroq))
	manager := runtime.NewManager(store)

	// Set openai healthy, groq degraded
	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateDegraded, "", nil)
		return nil
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	cfgSnap := router.ConfigSnapshot{Weights: config.DefaultRoutingWeights()}

	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, cfgSnap, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// OpenAI should be selected due to healthy state and lower latency
	if result.Candidate == nil {
		t.Fatal("expected non-nil candidate")
	}
	if result.Candidate.ProviderName != "openai" {
		t.Errorf("expected openai, got %s", result.Candidate.ProviderName)
	}
}
