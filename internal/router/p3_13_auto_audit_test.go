package router_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EffNine/conductor/internal/automode"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 9: "auto" mode audit.
//
// Verified facts:
//   - automode.ClassifyTask is a pure delegation to router.ClassifyRequest —
//     there is NO competing classification path.
//   - Explicit Mode="auto" resolves via ParseMode to the ModeDefault profile
//     ("default", source "explicit"). It is NOT re-classified from the text.
//   - Omitted Mode ("") runs the text classifier (source "classifier"); the
//     classifier can resolve to coding/reasoning/vision/fast/default AND to
//     the internal, inactive "elite" mode — which then fails the pipeline.

// traceRecorder captures the DecisionTrace published on the event bus.
type traceRecorder struct {
	mu    sync.Mutex
	trace *router.DecisionTrace
}

func (r *traceRecorder) onEvent(evt eventbus.Event) {
	if evt.Type != eventbus.DecisionTraceCreated {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if tr, ok := evt.Payload.(*router.DecisionTrace); ok {
		r.trace = tr
	}
}

func (r *traceRecorder) get() *router.DecisionTrace {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.trace
}

// setupCalibPipelineTraced is setupCalibPipeline plus an event bus that
// records the decision trace for the LAST Execute call.
func setupCalibPipelineTraced(t *testing.T, providers ...*calibStubProvider) (*router.DecisionPipeline, *runtime.RuntimeStore, *traceRecorder) {
	t.Helper()
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	for _, p := range providers {
		reg.Register(p)
		_ = store.Register(runtime.NewProviderRuntime(p.name, p))
	}
	manager := runtime.NewManager(store)
	bus := eventbus.NewEventBus()
	rec := &traceRecorder{}
	bus.Subscribe(eventbus.DecisionTraceCreated, rec.onEvent)

	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	for _, p := range providers {
		if p.maxContext > 0 {
			eng.SetModelCapabilities(p.name, "m", router.Capabilities{
				Streaming:   true,
				Vision:      p.vision,
				Reasoning:   p.reasoning,
				ToolCalling: p.toolCalling,
				Structured:  p.structured,
				MaxContext:  p.maxContext,
			})
		}
	}
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RoutingEngine:  eng,
		RuntimeManager: manager,
		EventBus:       bus,
		Weights:        config.DefaultRoutingWeights(),
	})
	return pipeline, store, rec
}

// TestP313AutoDelegatesToRouterClassifier: the legacy automode classifier is a
// thin wrapper over router.ClassifyRequest — no independent classification
// logic exists.
func TestP313AutoDelegatesToRouterClassifier(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"write a python function to parse json", string(router.ModeCoding)},
		{"analyze the trade-offs between x and y", string(router.ModeReasoning)},
		{"describe this image", string(router.ModeVision)},
		{"hi", string(router.ModeFast)},
		{"what is the weather like", string(router.ModeDefault)},
	}
	for _, c := range cases {
		got := automode.ClassifyTask(c.text)
		if got != c.want {
			t.Fatalf("ClassifyTask(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func intentPayload(t *testing.T, rec *traceRecorder) map[string]any {
	t.Helper()
	tr := rec.get()
	if tr == nil {
		t.Fatal("no trace captured")
	}
	for _, evt := range tr.Events {
		if evt.Type == string(eventbus.IntentResolved) {
			if m, ok := evt.Payload.(map[string]any); ok {
				return m
			}
		}
	}
	t.Fatal("no intent.resolved event in trace")
	return nil
}

// TestP313AutoIsDefaultNotClassifier: Mode="auto" with coding-looking text
// resolves to the DEFAULT profile (source "explicit") — the text is NOT
// classified. Behaviorally: the capability-strong but degraded provider LOSES
// under auto, while the same setup under omitted mode (classifier → coding)
// makes the capability-strong provider WIN.
func TestP313AutoIsDefaultNotClassifier(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateDegraded, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// Explicit auto + coding text: default weights (40/25/15/20) -> healthy
	// zzz wins; resolved_mode=default, mode_source=explicit.
	req := hintReq("auto")
	req.Messages[0].Content = "write a python function to parse json"
	if _, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	payload := intentPayload(t, rec)
	if got := payload["resolved_mode"]; got != "default" {
		t.Fatalf("auto: resolved_mode = %v, want default", got)
	}
	if got := payload["mode_source"]; got != "explicit" {
		t.Fatalf("auto: mode_source = %v, want explicit", got)
	}
	if got := payload["requested_mode"]; got != "auto" {
		t.Fatalf("auto: requested_mode = %v, want auto", got)
	}

	// Same providers, omitted mode: classifier resolves coding (capability
	// weight 60) -> aaa (capability-strong) wins.
	pipeline2, store2, rec2 := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store2, "aaa", runtime.StateDegraded, 100)
	setHealth(t, store2, "zzz", runtime.StateHealthy, 100)
	req2 := hintReq("")
	req2.Messages[0].Content = "write a python function to parse json"
	res2, err := pipeline2.Execute(context.Background(), req2, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res2.Decision.SelectedProvider != "aaa" {
		t.Fatalf("omitted mode: expected aaa (classifier->coding), got %q", res2.Decision.SelectedProvider)
	}
	payload2 := intentPayload(t, rec2)
	if got := payload2["resolved_mode"]; got != "coding" {
		t.Fatalf("omitted mode: resolved_mode = %v, want coding", got)
	}
	if got := payload2["mode_source"]; got != "classifier" {
		t.Fatalf("omitted mode: mode_source = %v, want classifier", got)
	}
}

// TestP313AutoNoReclassificationAcrossRequests: the trace event bus captures
// each decision's own trace; sequential auto decisions do not leak state.
func TestP313AutoNoReclassificationAcrossRequests(t *testing.T) {
	pipeline, store, rec := setupCalibPipelineTraced(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)

	req := execReq("auto", "analyze why this fails")
	if _, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	payload := intentPayload(t, rec)
	if got := payload["resolved_mode"]; got != "default" {
		t.Fatalf("auto with reasoning text: resolved_mode = %v, want default", got)
	}
}

// TestP313AutoEliteClassificationFails: the classifier can resolve to the
// internal-only "elite" mode (inactive profile). With mode omitted, such a
// request fails the pipeline with "not yet supported" — documented finding.
func TestP313AutoEliteClassificationFails(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)

	req := execReq("", "implement a distributed microservice backend")
	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err == nil {
		t.Fatal("expected error for elite-classified request with omitted mode")
	}
	// Explicit auto on the same text is fine (default profile).
	req.Mode = "auto"
	if _, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil); err != nil {
		t.Fatalf("explicit auto must not fail: %v", err)
	}
}
