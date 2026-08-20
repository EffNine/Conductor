package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// ---- P3.16 integration: real decision pipeline → SQLite persistence → API ----
//
// The full production shape: DecisionPipeline publishes DecisionFinished,
// TracePersistence saves to SQLiteTraceStore, and the trace query API serves
// the persisted result.

// pipelineStubProvider is a minimal provider whose runtime state makes it a
// viable routing candidate (health set explicitly via the runtime store).
type pipelineStubProvider struct {
	name       string
	latencyMs  int64
	health     runtime.ProviderState
	maxContext int
	costPer    float64
}

func (s *pipelineStubProvider) Name() string { return s.name }
func (s *pipelineStubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *pipelineStubProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (s *pipelineStubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *pipelineStubProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (s *pipelineStubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	if s.costPer <= 0 {
		return nil, nil
	}
	return map[string]provider.PricingInfo{"m": {UnitSize: 1, InputPrice: s.costPer}}, nil
}
func (s *pipelineStubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *pipelineStubProvider) SupportsModel(string) bool { return true }
func (s *pipelineStubProvider) GetMetadata() provider.Metadata {
	return provider.NewMetadata(s.name, provider.Capabilities{
		Streaming:   true,
		Vision:      true,
		Reasoning:   true,
		ToolCalling: true,
		Structured:  true,
	})
}

// setProviderHealth drives provider runtime state so scoring has real inputs.
func setProviderHealth(t *testing.T, rtStore *runtime.RuntimeStore, name string, state runtime.ProviderState, latencyMs int64) {
	t.Helper()
	if err := rtStore.Update(name, func(r runtime.ProviderRuntime) error {
		r.UpdateState(state, "", nil)
		if latencyMs > 0 {
			r.RecordLatency(latencyMs)
		}
		return nil
	}); err != nil {
		t.Fatalf("update %s: %v", name, err)
	}
}

// waitForPersistedTrace polls the store until the trace for a decision ID
// appears (persistence is async).
func waitForPersistedTrace(t *testing.T, store *database.SQLiteTraceStore, id router.DecisionID) *router.DecisionTrace {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tr, err := store.Get(context.Background(), id)
		if err == nil {
			return tr
		}
		if !errors.Is(err, router.ErrTraceNotFound) {
			t.Fatalf("Get: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("trace %s not persisted within 5s", id)
	return nil
}

func TestRoutingDecisionThenQueryTrace(t *testing.T) {
	reg := provider.NewRegistry()
	provA := &pipelineStubProvider{name: "alpha", latencyMs: 100, health: runtime.StateHealthy, maxContext: 128000, costPer: 0.0002}
	provB := &pipelineStubProvider{name: "beta", latencyMs: 300, health: runtime.StateHealthy, maxContext: 128000, costPer: 0.0009}

	// Rebuild the stack with access to the runtime store for health updates.
	rtStore := runtime.NewRuntimeStore(nil)
	reg.Register(provA)
	reg.Register(provB)
	_ = rtStore.Register(runtime.NewProviderRuntime(provA.Name(), provA))
	_ = rtStore.Register(runtime.NewProviderRuntime(provB.Name(), provB))
	manager := runtime.NewManager(rtStore)
	bus := eventbus.NewEventBus()
	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	for _, p := range []*pipelineStubProvider{provA, provB} {
		eng.SetModelCapabilities(p.Name(), "m", router.Capabilities{
			Streaming: true, Vision: true, Reasoning: true, ToolCalling: true, Structured: true, MaxContext: 128000,
		})
	}
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RoutingEngine:  eng,
		RuntimeManager: manager,
		EventBus:       bus,
		Weights:        config.DefaultRoutingWeights(),
	})
	setProviderHealth(t, rtStore, "alpha", runtime.StateHealthy, 100)
	setProviderHealth(t, rtStore, "beta", runtime.StateHealthy, 300)

	store := newTraceStore(t)
	persist := database.NewTracePersistence(bus, store, nil)
	persist.Start()
	t.Cleanup(persist.Stop)
	app, _ := setupTraceQueryApp(t, store)

	// Capture the decision ID from the DecisionFinished payload (the trace).
	var decisionID router.DecisionID
	sub := bus.Subscribe(eventbus.DecisionFinished, func(evt eventbus.Event) {
		if tr, ok := evt.Payload.(*router.DecisionTrace); ok && tr != nil {
			decisionID = tr.DecisionID
		}
	})
	defer bus.Unsubscribe(eventbus.DecisionFinished, sub)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "coding",
		Messages: []apitypes.Message{{Role: "user", Content: "implement feature"}},
	}
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil || res.Candidate == nil {
		t.Fatal("expected a selected candidate")
	}
	if decisionID == "" {
		t.Fatal("no DecisionFinished trace captured")
	}

	// Persistence is async: wait for the trace to land in SQLite.
	persisted := waitForPersistedTrace(t, store, decisionID)
	if persisted.Winner == nil || persisted.Winner.ProviderName != res.Candidate.ProviderName {
		t.Fatalf("persisted winner %+v != selection %s", persisted.Winner, res.Candidate.ProviderName)
	}

	// Query the API: list must contain the decision as a compact summary.
	_, payload := getTraceList(t, app, "")
	found := false
	for _, d := range payload.Data {
		if d["decision_id"] == string(decisionID) {
			found = true
			if d["outcome"] != "selected" {
				t.Fatalf("summary outcome = %v, want selected", d["outcome"])
			}
			if d["resolved_mode"] != "coding" {
				t.Fatalf("summary resolved_mode = %v, want coding", d["resolved_mode"])
			}
			if d["selected_provider"] != res.Candidate.ProviderName {
				t.Fatalf("summary provider = %v, want %s", d["selected_provider"], res.Candidate.ProviderName)
			}
			if _, ok := d["candidate_scores"]; ok {
				t.Fatalf("list item must be compact, no candidate_scores: %v", d)
			}
		}
	}
	if !found {
		t.Fatalf("decision %s not in list: %+v", decisionID, payload.Data)
	}

	// Query the API: single trace must return the canonical full payload.
	singleReq := httptest.NewRequest("GET", "/api/routing/traces/"+string(decisionID), nil)
	singleReq.Header.Set("Authorization", "Bearer test-key")
	singleResp, err := app.Test(singleReq)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer singleResp.Body.Close()
	if singleResp.StatusCode != 200 {
		t.Fatalf("single status = %d", singleResp.StatusCode)
	}
	var full router.DecisionTrace
	body, _ := io.ReadAll(singleResp.Body)
	if err := json.Unmarshal(body, &full); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if full.DecisionID != decisionID {
		t.Fatalf("full DecisionID = %q", full.DecisionID)
	}
	if full.Winner == nil || full.Winner.ProviderName != res.Candidate.ProviderName {
		t.Fatalf("full winner %+v != %s", full.Winner, res.Candidate.ProviderName)
	}
	if len(full.CandidateScores) != 2 {
		t.Fatalf("full candidate scores = %d, want 2", len(full.CandidateScores))
	}
	if len(full.StageResults) != 4 {
		t.Fatalf("full stage results = %d, want 4", len(full.StageResults))
	}
}

func TestFailedDecisionThenQueryTrace(t *testing.T) {
	prov := &pipelineStubProvider{name: "solo", latencyMs: 50, health: runtime.StateHealthy, maxContext: 128000}
	reg := provider.NewRegistry()
	reg.Register(prov)
	rtStore := runtime.NewRuntimeStore(nil)
	_ = rtStore.Register(runtime.NewProviderRuntime(prov.Name(), prov))
	manager := runtime.NewManager(rtStore)
	bus := eventbus.NewEventBus()
	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RoutingEngine:  eng,
		RuntimeManager: manager,
		EventBus:       bus,
		Weights:        config.DefaultRoutingWeights(),
	})
	setProviderHealth(t, rtStore, "solo", runtime.StateHealthy, 50)

	store := newTraceStore(t)
	persist := database.NewTracePersistence(bus, store, nil)
	persist.Start()
	t.Cleanup(persist.Stop)
	app, _ := setupTraceQueryApp(t, store)

	var decisionID router.DecisionID
	sub := bus.Subscribe(eventbus.DecisionFinished, func(evt eventbus.Event) {
		if tr, ok := evt.Payload.(*router.DecisionTrace); ok && tr != nil {
			decisionID = tr.DecisionID
		}
	})
	defer bus.Unsubscribe(eventbus.DecisionFinished, sub)

	// Invalid mode: the intent stage hard-rejects and publishes a failure trace.
	badReq := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "banana",
		Messages: []apitypes.Message{{Role: "user", Content: "do it"}},
	}
	if _, err := pipeline.Execute(context.Background(), badReq, router.Environment{}, router.ConfigSnapshot{}, nil); err == nil {
		t.Fatal("expected invalid-mode failure")
	}
	if decisionID == "" {
		t.Fatal("no failure trace captured")
	}

	persisted := waitForPersistedTrace(t, store, decisionID)
	if persisted.Winner != nil {
		t.Fatal("failure trace must not carry a winner")
	}

	// The API must expose the failure: outcome=failed filter finds it.
	_, payload := getTraceList(t, app, "outcome=failed")
	found := false
	for _, d := range payload.Data {
		if d["decision_id"] == string(decisionID) {
			found = true
			if d["outcome"] != "failed" {
				t.Fatalf("outcome = %v, want failed", d["outcome"])
			}
		}
	}
	if !found {
		t.Fatalf("failed decision %s not in outcome=failed list: %+v", decisionID, payload.Data)
	}

	// And the full trace explains the failure.
	singleReq := httptest.NewRequest("GET", "/api/routing/traces/"+string(decisionID), nil)
	singleReq.Header.Set("Authorization", "Bearer test-key")
	singleResp, err := app.Test(singleReq)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer singleResp.Body.Close()
	var full router.DecisionTrace
	body, _ := io.ReadAll(singleResp.Body)
	if err := json.Unmarshal(body, &full); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if full.RequestedMode != "banana" {
		t.Fatalf("RequestedMode = %q, want banana", full.RequestedMode)
	}
	failed := false
	for _, sr := range full.StageResults {
		if sr.Name == "intent" && sr.Status == router.StageStatusFailed {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("failure trace must mark intent stage failed: %+v", full.StageResults)
	}
}
