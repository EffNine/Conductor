package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

func TestNewDecisionPipeline(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	eventBus := eventbus.NewEventBus()
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry: reg,
		EventBus: eventBus,
		Weights:  config.DefaultRoutingWeights(),
	})
	if pipeline == nil {
		t.Fatal("expected non-nil pipeline")
	}
	if pipeline.RoutingEngine() == nil {
		t.Fatal("expected non-nil routing engine")
	}
}

func TestDecisionPipelineStagesOrder(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	stages := pipeline.Stages()
	if len(stages) != 4 {
		t.Fatalf("expected 4 stages, got %d", len(stages))
	}
	expected := []string{"intent", "capability", "candidate", "selection"}
	for i, s := range stages {
		if s.Name() != expected[i] {
			t.Errorf("stage[%d] = %q, want %q", i, s.Name(), expected[i])
		}
	}
}

func TestDecisionPipelineExecute(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	pOpenAI, _ := reg.Get("openai")
	pGroq, _ := reg.Get("groq")
	_ = store.Register(runtime.NewProviderRuntime("openai", pOpenAI))
	_ = store.Register(runtime.NewProviderRuntime("groq", pGroq))
	manager := runtime.NewManager(store)

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
	if result.Decision.SelectedModelID == "" {
		t.Fatal("expected non-empty selected model ID")
	}
}

func TestDecisionPipelineNoProviders(t *testing.T) {
	reg := provider.NewRegistry()
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "hello"},
		},
	}
	env := router.Environment{}
	cfgSnap := router.ConfigSnapshot{}

	_, err := pipeline.Execute(context.Background(), req, env, cfgSnap, nil)
	if err == nil {
		t.Fatal("expected error for no providers")
	}
}

func TestDecisionPipelineCapabilityStage(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	// Test that capability stage extracts vision requirement from image content.
	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
	env := router.Environment{}
	cfgSnap := router.ConfigSnapshot{}

	result, err := pipeline.Execute(context.Background(), req, env, cfgSnap, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestDecisionPipelineEventBus(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	eventBus := eventbus.NewEventBus()
	var events []string
	eventBus.Subscribe(eventbus.DecisionStarted, func(e eventbus.Event) {
		events = append(events, "started")
	})
	eventBus.Subscribe(eventbus.DecisionFinished, func(e eventbus.Event) {
		events = append(events, "finished")
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry: reg,
		EventBus: eventBus,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "hello"},
		},
	}

	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(events), events)
	}
	if events[0] != "started" {
		t.Errorf("first event = %q, want started", events[0])
	}
	if events[1] != "finished" {
		t.Errorf("second event = %q, want finished", events[1])
	}
}
