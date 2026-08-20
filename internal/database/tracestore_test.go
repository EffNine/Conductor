package database_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/policy"
	"github.com/EffNine/conductor/internal/router"
)

// newTestTraceStore creates a fresh SQLite database in a temp dir, migrates
// it, and returns the store plus the raw database handle for payload checks.
func newTestTraceStore(t *testing.T) (*database.SQLiteTraceStore, *database.Database) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, err := database.Connect(&config.DatabaseConfig{
		Driver:       "sqlite",
		DSN:          dsn,
		MaxOpenConns: 4,
		MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return database.NewSQLiteTraceStore(db), db
}

// sampleTrace builds a fully-populated canonical DecisionTrace for a decision
// with the given ID and timestamp. All contract dimensions (mode resolution,
// intent, capability requirements, weights, bonuses, candidate scores,
// winner, rejections, runtime hash, stages, events) are set so round-trip and
// payload tests exercise the complete trace.
func sampleTrace(id string, ts time.Time) *router.DecisionTrace {
	return &router.DecisionTrace{
		DecisionID:      router.DecisionID(id),
		TraceSchemaVer:  router.TraceSchemaVersion(),
		Timestamp:       ts.UTC(),
		RequestedMode:   "auto",
		ResolvedMode:    router.ModeDefault,
		ModeSource:      "explicit",
		ModeDescription: "no strong signal",
		ModeTraits:      []string{"balanced", "neutral"},
		Intent: &policy.Intent{
			TaskType:    policy.TaskTypeChat,
			Confidence:  0.8,
			Description: "chat",
		},
		CapabilityRequirements: &policy.CapabilityRequirement{
			NeedsStreaming: true,
		},
		ContextRequirement: 0,
		EffectiveWeights:   router.Weights{Health: 0.25, Latency: 0.25, Cost: 0.25, Capability: 0.25},
		ModeBonuses:        router.CapabilityBonuses{ToolCalling: 0.05, Reasoning: 0.10},
		RuntimeHash:        strings.Repeat("ab", 32),
		CandidateScores: []router.CandidateScore{
			{Provider: "provider_a", ProviderID: "model-a", TotalScore: 0.9, HealthScore: 1.0, LatencyScore: 0.8, CostScore: 0.7, CapScore: 0.95, ModeBonus: 0.05, Selected: true},
			{Provider: "provider_b", ProviderID: "model-b", TotalScore: 0.6, HealthScore: 0.5, LatencyScore: 0.9, CostScore: 0.8, CapScore: 0.6, Rejected: true, RejectionReason: "lower score"},
		},
		Winner: &router.ResolvedRoute{
			ProviderName:    "provider_a",
			ProviderModelID: "model-a",
			ModelID:         "gpt-4o",
		},
		RejectionReasons: []router.RejectionReason{
			{Provider: "provider_b", Reason: "lower score"},
		},
		StageResults: []*router.StageResult{
			{Name: "intent", DurationMs: 1, Status: router.StageStatusCompleted},
			{Name: "capability", DurationMs: 1, Status: router.StageStatusCompleted},
			{Name: "candidate", DurationMs: 2, Status: router.StageStatusCompleted, Metadata: map[string]any{"count": 2}},
			{Name: "selection", DurationMs: 1, Status: router.StageStatusCompleted},
		},
		Events: []router.EventRecord{
			{Type: "decision.started", Timestamp: ts.UTC()},
			{Type: "decision.finished", Timestamp: ts.UTC()},
		},
	}
}

// failedTrace is a decision that failed at the intent stage (hard rejection).
func failedTrace(id string, ts time.Time) *router.DecisionTrace {
	tr := sampleTrace(id, ts)
	tr.Winner = nil
	tr.CandidateScores = nil
	tr.StageResults = []*router.StageResult{
		{Name: "intent", DurationMs: 5, Status: router.StageStatusFailed, Metadata: map[string]any{"error": "inactive mode"}},
	}
	return tr
}

// rejectedTrace is a decision that completed but selected no candidate.
func rejectedTrace(id string, ts time.Time) *router.DecisionTrace {
	tr := sampleTrace(id, ts)
	tr.Winner = nil
	tr.CandidateScores = []router.CandidateScore{
		{Provider: "provider_b", ProviderID: "model-b", TotalScore: 0.6, HealthScore: 0.5, LatencyScore: 0.9, CostScore: 0.8, CapScore: 0.6, Rejected: true, RejectionReason: "vision required"},
	}
	tr.RejectionReasons = []router.RejectionReason{
		{Provider: "provider_b", Reason: "vision required"},
	}
	return tr
}

func saveTrace(t *testing.T, store *database.SQLiteTraceStore, trace *router.DecisionTrace) {
	t.Helper()
	if err := store.Save(context.Background(), trace); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// TestTraceStoreSave persists a complete trace and makes it retrievable.
func TestTraceStoreSave(t *testing.T) {
	store, _ := newTestTraceStore(t)
	tr := sampleTrace("dec-1", time.Now().UTC())
	saveTrace(t, store, tr)

	got, err := store.Get(context.Background(), "dec-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DecisionID != "dec-1" {
		t.Fatalf("DecisionID = %q, want dec-1", got.DecisionID)
	}
}

// TestTraceStoreGet retrieves a persisted trace by decision ID.
func TestTraceStoreGet(t *testing.T) {
	store, _ := newTestTraceStore(t)
	ts := time.Now().UTC().Add(-2 * time.Hour)
	tr := sampleTrace("dec-get", ts)
	saveTrace(t, store, tr)

	got, err := store.Get(context.Background(), "dec-get")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Timestamp.UTC() != ts {
		t.Fatalf("Timestamp = %v, want %v", got.Timestamp, ts)
	}
	if got.ResolvedMode != router.ModeDefault {
		t.Fatalf("ResolvedMode = %q, want default", got.ResolvedMode)
	}
	if got.Winner == nil || got.Winner.ProviderName != "provider_a" {
		t.Fatalf("Winner = %+v, want provider_a", got.Winner)
	}
}

// TestTraceStoreNotFound returns a clear not-found error for unknown IDs.
func TestTraceStoreNotFound(t *testing.T) {
	store, _ := newTestTraceStore(t)
	_, err := store.Get(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown decision ID")
	}
	if err != router.ErrTraceNotFound {
		t.Fatalf("err = %v, want router.ErrTraceNotFound", err)
	}
}

// TestTraceStoreList returns summaries newest-first for all traces.
func TestTraceStoreList(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC().Add(-24 * time.Hour)
	for i := 0; i < 5; i++ {
		saveTrace(t, store, sampleTrace("dec-list-"+string(rune('a'+i)), base.Add(time.Duration(i)*time.Minute)))
	}

	summaries, err := store.List(context.Background(), router.TraceFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 5 {
		t.Fatalf("len = %d, want 5", len(summaries))
	}
	// Newest first.
	if summaries[0].DecisionID != "dec-list-e" || summaries[4].DecisionID != "dec-list-a" {
		t.Fatalf("order = %v, want newest-first", summaries[0].DecisionID)
	}
	if summaries[0].SelectedProvider != "provider_a" || summaries[0].SelectedModel != "model-a" {
		t.Fatalf("summary dimensions wrong: %+v", summaries[0])
	}
	if summaries[0].CandidateCount != 2 || summaries[0].SelectedScore != 0.9 {
		t.Fatalf("summary scores wrong: %+v", summaries[0])
	}
	if summaries[0].Outcome != database.TraceOutcomeSelected {
		t.Fatalf("outcome = %q, want selected", summaries[0].Outcome)
	}
}

// TestTraceStoreModeFilter filters by resolved mode.
func TestTraceStoreModeFilter(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC()
	saveTrace(t, store, sampleTrace("dec-mode-default", base))
	coding := sampleTrace("dec-mode-coding", base.Add(time.Minute))
	coding.ResolvedMode = router.ModeCoding
	coding.RequestedMode = "coding"
	saveTrace(t, store, coding)

	summaries, err := store.List(context.Background(), router.TraceFilter{Mode: "coding"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1 || summaries[0].DecisionID != "dec-mode-coding" {
		t.Fatalf("got %+v, want only dec-mode-coding", summaries)
	}
}

// TestTraceStoreProviderFilter filters by selected provider.
func TestTraceStoreProviderFilter(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC()
	saveTrace(t, store, sampleTrace("dec-prov-a", base))
	tr := sampleTrace("dec-prov-b", base.Add(time.Minute))
	tr.Winner.ProviderName = "provider_b"
	tr.Winner.ProviderModelID = "model-b"
	tr.CandidateScores[0].Provider = "provider_b"
	saveTrace(t, store, tr)

	summaries, err := store.List(context.Background(), router.TraceFilter{Provider: "provider_b"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1 || summaries[0].DecisionID != "dec-prov-b" {
		t.Fatalf("got %+v, want only dec-prov-b", summaries)
	}
}

// TestTraceStoreModelFilter filters by selected model.
func TestTraceStoreModelFilter(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC()
	saveTrace(t, store, sampleTrace("dec-model-a", base))
	tr := sampleTrace("dec-model-b", base.Add(time.Minute))
	tr.Winner.ProviderModelID = "gpt-5"
	saveTrace(t, store, tr)

	summaries, err := store.List(context.Background(), router.TraceFilter{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1 || summaries[0].DecisionID != "dec-model-b" {
		t.Fatalf("got %+v, want only dec-model-b", summaries)
	}
}

// TestTraceStoreRuntimeHashFilter filters by runtime hash.
func TestTraceStoreRuntimeHashFilter(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC()
	saveTrace(t, store, sampleTrace("dec-hash-a", base))
	tr := sampleTrace("dec-hash-b", base.Add(time.Minute))
	tr.RuntimeHash = strings.Repeat("cd", 32)
	saveTrace(t, store, tr)

	want := strings.Repeat("cd", 32)
	summaries, err := store.List(context.Background(), router.TraceFilter{RuntimeHash: want})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1 || summaries[0].DecisionID != "dec-hash-b" {
		t.Fatalf("got %+v, want only dec-hash-b", summaries)
	}
}

// TestTraceStoreTimeRange filters by decision timestamp.
func TestTraceStoreTimeRange(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 10; i++ {
		saveTrace(t, store, sampleTrace("dec-range-"+string(rune('a'+i)), base.Add(time.Duration(i)*time.Minute)))
	}

	from := base.Add(2 * time.Minute)
	to := base.Add(6 * time.Minute)
	summaries, err := store.List(context.Background(), router.TraceFilter{From: from, To: to})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 5 {
		t.Fatalf("len = %d, want 5 (traces at +2..+6 min)", len(summaries))
	}
	// Newest first within the window: g, f, e, d, c (+2..+6 min inclusive).
	if summaries[0].DecisionID != "dec-range-g" || summaries[4].DecisionID != "dec-range-c" {
		t.Fatalf("window order = %v", summaries)
	}
}

// TestTraceStorePagination applies limit/offset deterministically.
func TestTraceStorePagination(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 25; i++ {
		saveTrace(t, store, sampleTrace("dec-page-"+string(rune('a'+i)), base.Add(time.Duration(i)*time.Minute)))
	}

	page1, err := store.List(context.Background(), router.TraceFilter{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1) != 10 || page1[0].DecisionID != "dec-page-y" || page1[9].DecisionID != "dec-page-p" {
		t.Fatalf("page1 = %d rows, first %s last %s", len(page1), page1[0].DecisionID, page1[9].DecisionID)
	}

	page3, err := store.List(context.Background(), router.TraceFilter{Limit: 10, Offset: 20})
	if err != nil {
		t.Fatalf("List page3: %v", err)
	}
	if len(page3) != 5 || page3[0].DecisionID != "dec-page-e" {
		t.Fatalf("page3 = %d rows, first %s", len(page3), page3[0].DecisionID)
	}

	// Default limit is 100.
	if got, err := store.List(context.Background(), router.TraceFilter{}); err != nil {
		t.Fatalf("List default: %v", err)
	} else if len(got) != 25 {
		t.Fatalf("default list len = %d, want 25", len(got))
	}
}

// TestTraceOutcomeDerivation persists selected/rejected/failed outcomes.
func TestTraceOutcomeDerivation(t *testing.T) {
	store, db := newTestTraceStore(t)
	base := time.Now().UTC()
	saveTrace(t, store, sampleTrace("dec-out-selected", base))
	saveTrace(t, store, rejectedTrace("dec-out-rejected", base.Add(time.Minute)))
	saveTrace(t, store, failedTrace("dec-out-failed", base.Add(2*time.Minute)))

	for _, tc := range []struct {
		id      string
		outcome string
	}{
		{"dec-out-selected", database.TraceOutcomeSelected},
		{"dec-out-rejected", database.TraceOutcomeRejected},
		{"dec-out-failed", database.TraceOutcomeFailed},
	} {
		var outcome string
		if err := db.DB.Raw("SELECT outcome FROM routing_traces WHERE decision_id = ?", tc.id).Scan(&outcome).Error; err != nil {
			t.Fatalf("query %s: %v", tc.id, err)
		}
		if outcome != tc.outcome {
			t.Fatalf("%s outcome = %q, want %q", tc.id, outcome, tc.outcome)
		}
	}

	// Outcome filter.
	failed, err := store.List(context.Background(), router.TraceFilter{Outcome: database.TraceOutcomeFailed})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(failed) != 1 || failed[0].DecisionID != "dec-out-failed" {
		t.Fatalf("failed list = %+v", failed)
	}
}

// TestTraceRoundTrip verifies the canonical payload survives save+get losslessly.
func TestTraceRoundTrip(t *testing.T) {
	store, _ := newTestTraceStore(t)
	ts := time.Now().UTC().Add(-3 * time.Hour)
	tr := sampleTrace("dec-roundtrip", ts)
	saveTrace(t, store, tr)

	got, err := store.Get(context.Background(), "dec-roundtrip")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Marshal both sides and compare JSON: any difference is a lossy round trip.
	orig, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal orig: %v", err)
	}
	round, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal round: %v", err)
	}
	if string(orig) != string(round) {
		t.Fatalf("round trip not lossless:\norig:  %s\nround: %s", orig, round)
	}
	if got.RuntimeHash != tr.RuntimeHash || got.EffectiveWeights != tr.EffectiveWeights {
		t.Fatal("trace identity fields changed")
	}
	if len(got.StageResults) != 4 || got.StageResults[2].Metadata["count"] != float64(2) {
		t.Fatalf("stage metadata lost: %+v", got.StageResults)
	}
}

// payloadOf returns the raw canonical payload JSON of a persisted trace.
func payloadOf(t *testing.T, db *database.Database, id string) string {
	t.Helper()
	var payload string
	if err := db.DB.Raw("SELECT payload_json FROM routing_traces WHERE decision_id = ?", id).Scan(&payload).Error; err != nil {
		t.Fatalf("payload query: %v", err)
	}
	return payload
}

// TestTracePayloadContainsCandidateScores: candidate scores with per-component
// contributions and the selected flag survive into the persisted payload.
func TestTracePayloadContainsCandidateScores(t *testing.T) {
	store, db := newTestTraceStore(t)
	saveTrace(t, store, sampleTrace("dec-payload-scores", time.Now().UTC()))

	payload := payloadOf(t, db, "dec-payload-scores")
	for _, want := range []string{
		`"candidate_scores"`,
		`"provider":"provider_a"`,
		`"total_score":0.9`,
		`"health_score":1`,
		`"mode_bonus":0.05`,
		`"selected":true`,
		`"rejected":true`,
		`"rejection_reason":"lower score"`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %s:\n%s", want, payload)
		}
	}
}

// TestTracePayloadContainsRejectionReasons: hard rejection reasons persist.
func TestTracePayloadContainsRejectionReasons(t *testing.T) {
	store, db := newTestTraceStore(t)
	saveTrace(t, store, rejectedTrace("dec-payload-rej", time.Now().UTC()))

	payload := payloadOf(t, db, "dec-payload-rej")
	if !strings.Contains(payload, `"rejection_reasons"`) || !strings.Contains(payload, `"vision required"`) {
		t.Fatalf("rejection reasons missing from payload:\n%s", payload)
	}
}

// TestTracePayloadContainsRuntimeHash: the runtime hash survives intact.
func TestTracePayloadContainsRuntimeHash(t *testing.T) {
	store, db := newTestTraceStore(t)
	saveTrace(t, store, sampleTrace("dec-payload-hash", time.Now().UTC()))

	payload := payloadOf(t, db, "dec-payload-hash")
	if !strings.Contains(payload, `"runtime_hash":"`+strings.Repeat("ab", 32)+`"`) {
		t.Fatalf("runtime hash missing from payload:\n%s", payload)
	}
}

// TestTracePayloadContainsEffectiveWeights: effective weights persist.
func TestTracePayloadContainsEffectiveWeights(t *testing.T) {
	store, db := newTestTraceStore(t)
	saveTrace(t, store, sampleTrace("dec-payload-weights", time.Now().UTC()))

	payload := payloadOf(t, db, "dec-payload-weights")
	for _, want := range []string{`"effective_weights"`, `"health":0.25`, `"latency":0.25`, `"cost":0.25`, `"capability":0.25`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %s:\n%s", want, payload)
		}
	}
}

// TestTracePayloadContainsModeMetadata: mode resolution fields persist.
func TestTracePayloadContainsModeMetadata(t *testing.T) {
	store, db := newTestTraceStore(t)
	saveTrace(t, store, sampleTrace("dec-payload-mode", time.Now().UTC()))

	payload := payloadOf(t, db, "dec-payload-mode")
	for _, want := range []string{
		`"requested_mode":"auto"`,
		`"resolved_mode":"default"`,
		`"mode_source":"explicit"`,
		`"mode_description":"no strong signal"`,
		`"mode_bonuses"`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %s:\n%s", want, payload)
		}
	}
}

// TestTracePayloadDoesNotContainSecrets: no API keys, authorization headers,
// or credential-shaped strings may ever reach the persisted payload.
func TestTracePayloadDoesNotContainSecrets(t *testing.T) {
	store, db := newTestTraceStore(t)
	saveTrace(t, store, sampleTrace("dec-safety-1", time.Now().UTC()))

	payload := payloadOf(t, db, "dec-safety-1")
	lower := strings.ToLower(payload)
	for _, forbidden := range []string{
		"sk-", "api_key", "apikey", "authorization", "bearer ", "x-api-key",
		"secret", "password",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("payload contains forbidden secret string %q:\n%s", forbidden, payload)
		}
	}
}

// TestTracePayloadDoesNotContainPrompt: request content never persists.
func TestTracePayloadDoesNotContainPrompt(t *testing.T) {
	store, db := newTestTraceStore(t)
	tr := sampleTrace("dec-safety-2", time.Now().UTC())
	saveTrace(t, store, tr)

	payload := payloadOf(t, db, "dec-safety-2")
	// The trace contract has no request body; verify no message-like content
	// and no role/content JSON keys appear.
	for _, forbidden := range []string{`"messages"`, `"role":"user"`, `"content"`, "hello", "prompt"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("payload contains forbidden request content %q:\n%s", forbidden, payload)
		}
	}
}

// TestConcurrentTraceWrites: concurrent saves are race-safe and all land.
func TestConcurrentTraceWrites(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC()

	const workers = 20
	const perWorker = 5
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := "dec-conc-write-" + string(rune('a'+w)) + string(rune('0'+i))
				if err := store.Save(context.Background(), sampleTrace(id, base)); err != nil {
					t.Errorf("Save %s: %v", id, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	summaries, err := store.List(context.Background(), router.TraceFilter{Limit: 1000})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != workers*perWorker {
		t.Fatalf("persisted = %d, want %d", len(summaries), workers*perWorker)
	}
}

// TestConcurrentTraceReadsAndWrites: mixed readers/writers under race detector.
func TestConcurrentTraceReadsAndWrites(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC()
	for i := 0; i < 10; i++ {
		saveTrace(t, store, sampleTrace("dec-conc-mix-"+string(rune('a'+i)), base))
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := "dec-conc-mix-w" + string(rune('0'+w))
			for i := 0; i < 20; i++ {
				if err := store.Save(context.Background(), sampleTrace(id+"-"+string(rune('0'+i)), base)); err != nil {
					t.Errorf("Save: %v", err)
					return
				}
				if _, err := store.Get(context.Background(), "dec-conc-mix-a"); err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if _, err := store.List(context.Background(), router.TraceFilter{Limit: 10}); err != nil {
					t.Errorf("List: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

// TestRoutingTraceMigration: routing_traces is created on fresh databases and
// existing schemas remain healthy on re-migration.
func TestRoutingTraceMigration(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, err := database.Connect(&config.DatabaseConfig{Driver: "sqlite", DSN: dsn, MaxOpenConns: 1, MaxIdleConns: 1})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Fresh database: table exists.
	var count int64
	if err := db.DB.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='routing_traces'").Scan(&count).Error; err != nil {
		t.Fatalf("table check: %v", err)
	}
	if count != 1 {
		t.Fatalf("routing_traces table count = %d, want 1", count)
	}

	// Existing tables remain writable after the new migration.
	rec := database.UsageRecord{ID: "u-1", RequestID: "r-1", ModelID: "m", Provider: "p", CreatedAt: time.Now()}
	if err := db.DB.Create(&rec).Error; err != nil {
		t.Fatalf("existing table write after migration: %v", err)
	}

	// Re-migration (startup on an existing database) is idempotent and healthy.
	if err := db.Migrate(); err != nil {
		t.Fatalf("re-Migrate: %v", err)
	}
	var again int64
	if err := db.DB.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='routing_traces'").Scan(&again).Error; err != nil {
		t.Fatalf("re-check: %v", err)
	}
	if again != 1 {
		t.Fatalf("routing_traces after re-migrate = %d, want 1", again)
	}
}

// TestRoutingTraceIndexes: the documented query indexes exist.
func TestRoutingTraceIndexes(t *testing.T) {
	_, db := newTestTraceStore(t)

	var names []string
	if err := db.DB.Raw("SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='routing_traces' ORDER BY name").Scan(&names).Error; err != nil {
		t.Fatalf("index query: %v", err)
	}
	want := []string{
		"idx_routing_traces_runtime_hash",
		"idx_routing_traces_selected_model",
		"idx_routing_traces_timestamp",
		"idx_routing_traces_timestamp_mode",
		"idx_routing_traces_timestamp_provider",
	}
	for _, w := range want {
		found := false
		for _, n := range names {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("index %s missing; have %v", w, names)
		}
	}
}

// TestTraceStoreListDeterministicOrdering: rows with IDENTICAL timestamps are
// ordered by decision_id DESC, so pagination boundaries never shift when
// timestamps collide.
func TestTraceStoreListDeterministicOrdering(t *testing.T) {
	store, _ := newTestTraceStore(t)
	ts := time.Now().UTC().Truncate(time.Second)
	// All three rows share the same timestamp; order must fall to decision_id.
	saveTrace(t, store, sampleTrace("dec-ord-b", ts))
	saveTrace(t, store, sampleTrace("dec-ord-a", ts))
	saveTrace(t, store, sampleTrace("dec-ord-c", ts))

	summaries, err := store.List(context.Background(), router.TraceFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("len = %d, want 3", len(summaries))
	}
	got := []router.DecisionID{summaries[0].DecisionID, summaries[1].DecisionID, summaries[2].DecisionID}
	want := []router.DecisionID{"dec-ord-c", "dec-ord-b", "dec-ord-a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (decision_id DESC tiebreak)", got, want)
		}
	}

	// Stable across repeated queries (no drift between pages).
	for i := 0; i < 5; i++ {
		again, err := store.List(context.Background(), router.TraceFilter{Limit: 10})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for j := range again {
			if again[j].DecisionID != summaries[j].DecisionID {
				t.Fatalf("order unstable on repeat %d: %v vs %v", i, again, summaries)
			}
		}
	}
}

// TestTraceStoreFilterCombination: multiple filter dimensions combine with
// AND semantics in one query.
func TestTraceStoreFilterCombination(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC().Add(-time.Hour)

	tr := sampleTrace("dec-combo-target", base.Add(5*time.Minute))
	tr.ResolvedMode = router.ModeCoding
	tr.Winner.ProviderName = "provider_a"
	tr.Winner.ProviderModelID = "model-a"
	tr.RuntimeHash = strings.Repeat("ef", 32)
	saveTrace(t, store, tr)

	// Same mode, different provider.
	other := sampleTrace("dec-combo-other-prov", base.Add(6*time.Minute))
	other.ResolvedMode = router.ModeCoding
	other.Winner.ProviderName = "provider_b"
	other.RuntimeHash = strings.Repeat("ef", 32)
	saveTrace(t, store, other)

	// Same mode+provider, different hash.
	otherHash := sampleTrace("dec-combo-other-hash", base.Add(7*time.Minute))
	otherHash.ResolvedMode = router.ModeCoding
	otherHash.Winner.ProviderName = "provider_a"
	saveTrace(t, store, otherHash)

	// Same everything, outside the time window.
	outside := sampleTrace("dec-combo-outside", base.Add(2*time.Hour))
	outside.ResolvedMode = router.ModeCoding
	outside.Winner.ProviderName = "provider_a"
	outside.RuntimeHash = strings.Repeat("ef", 32)
	saveTrace(t, store, outside)

	summaries, err := store.List(context.Background(), router.TraceFilter{
		Mode:        "coding",
		Provider:    "provider_a",
		RuntimeHash: strings.Repeat("ef", 32),
		From:        base,
		To:          base.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1 || summaries[0].DecisionID != "dec-combo-target" {
		t.Fatalf("combined filter = %+v, want only dec-combo-target", summaries)
	}
}

// TestTraceStoreLimitCap: the store-level cap (1000) still applies when a
// caller requests more than the cap allows.
func TestTraceStoreLimitCap(t *testing.T) {
	store, _ := newTestTraceStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 1200; i++ {
		tr := sampleTrace("dec-cap-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Minute))
		saveTrace(t, store, tr)
	}

	summaries, err := store.List(context.Background(), router.TraceFilter{Limit: 5000})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1000 {
		t.Fatalf("len = %d, want 1000 (store cap)", len(summaries))
	}
	if summaries[0].DecisionID != "dec-cap-1199" {
		t.Fatalf("first = %s, want newest dec-cap-1199", summaries[0].DecisionID)
	}
}

// BenchmarkTraceStoreSave measures insert latency for a complete trace.
// Each iteration uses a unique decision ID so the insert is real (the
// OnConflict no-op path is only exercised on duplicates).
func BenchmarkTraceStoreSave(b *testing.B) {
	store, _ := newTestBenchStore(b)
	base := time.Now().UTC()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr := sampleTrace("bench-save-"+strconv.Itoa(i), base)
		if err := store.Save(context.Background(), tr); err != nil {
			b.Fatalf("Save: %v", err)
		}
	}
}

// BenchmarkTraceStoreList measures filtered list latency on a populated store.
func BenchmarkTraceStoreList(b *testing.B) {
	store, _ := newTestBenchStore(b)
	base := time.Now().UTC()
	for i := 0; i < 1000; i++ {
		tr := sampleTrace("bench-list", base.Add(time.Duration(i)*time.Minute))
		if i%2 == 0 {
			tr.ResolvedMode = router.ModeCoding
		}
		if err := store.Save(context.Background(), tr); err != nil {
			b.Fatalf("seed Save: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.List(context.Background(), router.TraceFilter{Mode: "coding", Limit: 50}); err != nil {
			b.Fatalf("List: %v", err)
		}
	}
}

// BenchmarkTracePayloadSize measures the canonical serialized payload size.
func BenchmarkTracePayloadSize(b *testing.B) {
	tr := sampleTrace("bench-payload", time.Now().UTC())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		payload, err := json.Marshal(tr)
		if err != nil {
			b.Fatalf("marshal: %v", err)
		}
		b.ReportMetric(float64(len(payload)), "payload_bytes")
	}
}

func newTestBenchStore(b *testing.B) (*database.SQLiteTraceStore, *database.Database) {
	b.Helper()
	dsn := filepath.Join(b.TempDir(), "test.db")
	db, err := database.Connect(&config.DatabaseConfig{Driver: "sqlite", DSN: dsn, MaxOpenConns: 2, MaxIdleConns: 2})
	if err != nil {
		b.Fatalf("Connect: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		b.Fatalf("Migrate: %v", err)
	}
	return database.NewSQLiteTraceStore(db), db
}
