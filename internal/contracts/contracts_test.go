package contracts_test

import (
	"testing"

	"github.com/EffNine/conductor/internal/contracts"
)

func TestSchemaVersion(t *testing.T) {
	if contracts.CurrentSchemaVersion != "v2.2-c" {
		t.Errorf("CurrentSchemaVersion = %q, want v2.2-c", contracts.CurrentSchemaVersion)
	}
}

func TestSchemaMetadataValidate(t *testing.T) {
	m := contracts.NewSchemaMetadata("test_contract")
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.SchemaVersion != contracts.CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", m.SchemaVersion, contracts.CurrentSchemaVersion)
	}
	if m.ContractID != "test_contract" {
		t.Errorf("ContractID = %q, want test_contract", m.ContractID)
	}
}

func TestSchemaMetadataEmptyContractID(t *testing.T) {
	m := contracts.SchemaMetadata{SchemaVersion: contracts.CurrentSchemaVersion}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for empty contract ID")
	}
}

func TestSchemaMetadataWrongVersion(t *testing.T) {
	m := contracts.SchemaMetadata{
		SchemaVersion: "v1.0.0",
		ContractID:    "test",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for old schema version")
	}
}

func TestCanonicalIDs(t *testing.T) {
	decID := contracts.NewDecisionID()
	if decID == "" {
		t.Fatal("expected non-empty DecisionID")
	}

	traceID := contracts.NewTraceID()
	if traceID == "" {
		t.Fatal("expected non-empty TraceID")
	}

	snapID := contracts.NewSnapshotID()
	if snapID == "" {
		t.Fatal("expected non-empty SnapshotID")
	}

	execID := contracts.NewExecutionID()
	if execID == "" {
		t.Fatal("expected non-empty ExecutionID")
	}

	candID := contracts.NewCandidateID("openai", "gpt-4o")
	if candID != "openai/gpt-4o" {
		t.Errorf("CandidateID = %q, want openai/gpt-4o", candID)
	}

	provID := contracts.NewProviderID("groq")
	if provID != "groq" {
		t.Errorf("ProviderID = %q, want groq", provID)
	}

	policyID := contracts.NewPolicyID("default")
	if policyID != "default" {
		t.Errorf("PolicyID = %q, want default", policyID)
	}
}

func TestRuntimeSnapshotBuilder(t *testing.T) {
	builder := contracts.NewRuntimeSnapshotBuilder()
	builder.SetProvider("openai", contracts.ProviderInfo{
		State:     "healthy",
		LatencyMs: 100,
		IsHealthy: true,
	})
	builder.SetProvider("groq", contracts.ProviderInfo{
		State:     "degraded",
		LatencyMs: 500,
		IsHealthy: false,
	})
	builder.SetGlobal(contracts.GlobalRuntimeState{
		TotalProviders:   2,
		HealthyProviders: 1,
		AvgLatencyMs:     300,
	})

	snap, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := snap.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(snap.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(snap.Providers))
	}
	if snap.Providers["openai"].LatencyMs != 100 {
		t.Errorf("openai LatencyMs = %d, want 100", snap.Providers["openai"].LatencyMs)
	}
}

func TestRuntimeSnapshotClone(t *testing.T) {
	builder := contracts.NewRuntimeSnapshotBuilder()
	builder.SetProvider("openai", contracts.ProviderInfo{State: "healthy", LatencyMs: 50})
	snap, _ := builder.Build()

	cloned := snap.Clone()
	if cloned.Providers["openai"].LatencyMs != 50 {
		t.Errorf("cloned provider latency = %d, want 50", cloned.Providers["openai"].LatencyMs)
	}
	// Mutate original and verify clone is independent.
	orig := snap.Providers["openai"]
	orig.LatencyMs = 999
	snap.Providers["openai"] = orig
	if cloned.Providers["openai"].LatencyMs != 50 {
		t.Error("clone was mutated by original change")
	}
}

func TestDecisionContextBuilder(t *testing.T) {
	builder := contracts.NewDecisionContextBuilder()
	builder.SetRequest(contracts.RequestSpec{
		Model:        "gpt-4o",
		Stream:       true,
		MessageCount: 2,
		Messages: []contracts.MessageSpec{
			{Role: "user", Content: "hello"},
		},
	})
	builder.SetTaskMeta(&contracts.TaskMetadata{
		ModelID:      "gpt-4o",
		IsStream:     true,
		MessageCount: 2,
	})
	builder.SetEnvironment(&contracts.EnvironmentSpec{
		CircuitBreakerEnabled: true,
	})

	dc, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := dc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if dc.DecisionID() == "" {
		t.Fatal("expected non-empty DecisionID")
	}
	if dc.Request().Model != "gpt-4o" {
		t.Errorf("Request.Model = %q, want gpt-4o", dc.Request().Model)
	}
}

func TestDecisionContextBuilderMissingModel(t *testing.T) {
	builder := contracts.NewDecisionContextBuilder()
	_, err := builder.Build()
	if err == nil {
		t.Fatal("expected error for missing model")
	}
}

func TestDecisionContextClone(t *testing.T) {
	builder := contracts.NewDecisionContextBuilder()
	builder.SetRequest(contracts.RequestSpec{Model: "gpt-4o", Messages: []contracts.MessageSpec{{Role: "user", Content: "hi"}}})
	dc, _ := builder.Build()

	cloned := dc.Clone()
	if cloned.DecisionID() != dc.DecisionID() {
		t.Error("cloned DecisionID differs")
	}
	if cloned.Request().Messages[0].Content != "hi" {
		t.Error("cloned message content differs")
	}
	// Mutate original and verify clone is independent.
	cloned.Request().Messages[0].Content = "mutated"
	if dc.Request().Messages[0].Content != "hi" {
		t.Error("original was mutated by clone change")
	}
}

func TestCandidateBuilder(t *testing.T) {
	builder := contracts.NewCandidateBuilder("openai", "gpt-4o")
	cost := 0.00001
	builder.SetHealthScore(0.95)
	builder.SetLatency(120)
	builder.SetCostPerToken(&cost)
	builder.SetCapabilities(contracts.Capabilities{Vision: true, Streaming: true})
	builder.SetAvailable(true)
	builder.SetTotalScore(0.88)

	cand, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if cand.ProviderID != "openai" {
		t.Errorf("ProviderID = %q, want openai", cand.ProviderID)
	}
	if cand.ProviderModelID != "gpt-4o" {
		t.Errorf("ProviderModelID = %q, want gpt-4o", cand.ProviderModelID)
	}
	if cand.TotalScore != 0.88 {
		t.Errorf("TotalScore = %f, want 0.88", cand.TotalScore)
	}
	if *cand.CostPerToken != 0.00001 {
		t.Errorf("CostPerToken = %f, want 0.00001", *cand.CostPerToken)
	}
}

func TestCandidateBuilderMissingProvider(t *testing.T) {
	builder := contracts.NewCandidateBuilder("", "gpt-4o")
	_, err := builder.Build()
	if err == nil {
		t.Fatal("expected error for empty provider ID")
	}
}

func TestCandidateClone(t *testing.T) {
	builder := contracts.NewCandidateBuilder("groq", "llama-3.1-8b")
	builder.SetTotalScore(0.75)
	cand, _ := builder.Build()

	cloned := cand.Clone()
	cloned.TotalScore = 0.99
	if cand.TotalScore != 0.75 {
		t.Error("original was mutated by clone change")
	}
}

func TestDecisionResultBuilder(t *testing.T) {
	builder := contracts.NewDecisionResultBuilder("dec-123", "snap-456")
	builder.SetWinner(&contracts.ResolvedRoute{
		ProviderName:    "openai",
		ProviderModelID: "gpt-4o",
		ModelID:         "gpt-4o",
	})
	builder.AddCandidate(&contracts.CandidateRecord{
		CandidateID:     "openai/gpt-4o",
		ProviderName:    "openai",
		ProviderModelID: "gpt-4o",
		TotalScore:      0.9,
		Selected:        true,
	})
	builder.SetDecision(contracts.RoutingDecision{
		SelectedProvider:  "openai",
		SelectedModelID:   "gpt-4o",
		RoutingDurationMs: 15,
	})
	builder.SetConfidence(0.95)

	result, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if result.DecisionID != "dec-123" {
		t.Errorf("DecisionID = %q, want dec-123", result.DecisionID)
	}
	if result.Winner == nil || result.Winner.ProviderName != "openai" {
		t.Fatal("expected winner")
	}
	if result.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", result.Confidence)
	}
}

func TestExecutionResultBuilder(t *testing.T) {
	builder := contracts.NewExecutionResultBuilder("dec-123", "trace-456", "snap-789")
	builder.SetProvider("openai", "gpt-4o", "gpt-4o-2024-08-06")
	builder.SetSuccess(true)
	builder.SetLatency(234)
	builder.SetStatusCode(200)
	builder.SetUsage(&contracts.UsageRecord{
		PromptTokens:     10,
		CompletionTokens: 50,
		TotalTokens:      60,
	})

	exec, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if exec.ExecutionID == "" {
		t.Fatal("expected non-empty ExecutionID")
	}
	if exec.ProviderName != "openai" {
		t.Errorf("ProviderName = %q, want openai", exec.ProviderName)
	}
	if !exec.Success {
		t.Error("expected success")
	}
	if exec.LatencyMs != 234 {
		t.Errorf("LatencyMs = %d, want 234", exec.LatencyMs)
	}
}

func TestExecutionResultMissingProvider(t *testing.T) {
	builder := contracts.NewExecutionResultBuilder("dec-123", "trace-456", "snap-789")
	_, err := builder.Build()
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestProviderSnapshotBuilder(t *testing.T) {
	builder := contracts.NewProviderSnapshotBuilder("groq")
	builder.SetState("healthy")
	builder.SetLatency(80)
	builder.SetErrorRate(0.01)
	builder.SetCapacity(0.9)
	builder.SetHealthy(true)

	snap, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if snap.ProviderID != "groq" {
		t.Errorf("ProviderID = %q, want groq", snap.ProviderID)
	}
	if snap.LatencyMs != 80 {
		t.Errorf("LatencyMs = %d, want 80", snap.LatencyMs)
	}
}

func TestProviderSnapshotMissingID(t *testing.T) {
	builder := contracts.NewProviderSnapshotBuilder("")
	_, err := builder.Build()
	if err == nil {
		t.Fatal("expected error for empty provider ID")
	}
}

func TestTraceBuilder(t *testing.T) {
	builder := contracts.NewDecisionTraceBuilder("dec-123", 1, "snap-456", "abc123")
	builder.AddStageResult(&contracts.StageResult{
		Name:       "intent",
		DurationMs: 2,
		Status:     contracts.StageStatusCompleted,
	})
	builder.AddStageResult(&contracts.StageResult{
		Name:       "capability",
		DurationMs: 1,
		Status:     contracts.StageStatusCompleted,
	})
	builder.AddEvent(contracts.EventRecord{
		Type: "decision.started",
	})
	builder.SetWinner(&contracts.WinnerRecord{
		ProviderName:    "openai",
		ProviderModelID: "gpt-4o",
		ModelID:         "gpt-4o",
	})
	builder.SetRejectionReasons([]contracts.RejectionReason{
		{Provider: "groq", Reason: "circuit breaker open"},
	})

	trace, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if trace.DecisionID != "dec-123" {
		t.Errorf("DecisionID = %q, want dec-123", trace.DecisionID)
	}
	if len(trace.StageResults) != 2 {
		t.Fatalf("expected 2 stage results, got %d", len(trace.StageResults))
	}
	if trace.Winner == nil || trace.Winner.ProviderName != "openai" {
		t.Fatal("expected winner")
	}
}

func TestTraceValidation(t *testing.T) {
	builder := contracts.NewDecisionTraceBuilder("dec-123", 1, "snap-456", "hash123")
	builder.AddStageResult(&contracts.StageResult{Name: "intent", Status: contracts.StageStatusCompleted})

	trace, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := trace.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestTraceClone(t *testing.T) {
	builder := contracts.NewDecisionTraceBuilder("dec-123", 1, "snap-456", "hash123")
	builder.AddStageResult(&contracts.StageResult{Name: "test", Status: contracts.StageStatusCompleted})
	trace, _ := builder.Build()

	cloned := trace.Clone()
	cloned.StageResults[0].DurationMs = 999
	if trace.StageResults[0].DurationMs != 0 {
		t.Error("original was mutated by clone change")
	}
}

func TestMarshalUnmarshalDecisionTrace(t *testing.T) {
	builder := contracts.NewDecisionTraceBuilder("dec-123", 1, "snap-456", "hash123")
	builder.AddStageResult(&contracts.StageResult{Name: "intent", DurationMs: 5, Status: contracts.StageStatusCompleted})
	trace, _ := builder.Build()

	data, err := trace.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	unmarshaled, err := contracts.UnmarshalTrace(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if unmarshaled.DecisionID != trace.DecisionID {
		t.Errorf("round-trip DecisionID = %q, want %q", unmarshaled.DecisionID, trace.DecisionID)
	}
}

func TestMarshalUnmarshalExecutionResult(t *testing.T) {
	builder := contracts.NewExecutionResultBuilder("dec-1", "trace-1", "snap-1")
	builder.SetProvider("openai", "gpt-4o", "gpt-4o")
	builder.SetSuccess(true)
	builder.SetLatency(100)
	exec, _ := builder.Build()

	data, err := exec.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	unmarshaled, err := contracts.UnmarshalExecutionResult(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if unmarshaled.ProviderName != "openai" {
		t.Errorf("round-trip ProviderName = %q, want openai", unmarshaled.ProviderName)
	}
}
