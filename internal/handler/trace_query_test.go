package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/auth"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ---- P3.16: routing trace query API ----
//
// Read-only observability over persisted routing traces. These tests drive
// the real HTTP endpoints against the real SQLiteTraceStore.

// setupTraceQueryApp builds a Fiber app with the trace query endpoints wired
// to the given store (nil store exercises the persistence-unavailable path).
func setupTraceQueryApp(t *testing.T, store router.TraceStore) (*fiber.App, *database.Database) {
	t.Helper()
	cfg := &config.Config{APIKey: "test-key"}
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{name: "openai"})

	db := openTestDB(t)
	routerEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	modelCatalog := catalog.New(reg, nil)
	h := handler.New(routerEngine, reg, nil, zap.NewNop(), modelCatalog, db)
	if store != nil {
		h.SetTraceStore(store)
	}

	app := fiber.New()
	authService := auth.NewService("test-key")
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("correlation_id", "test-corr-"+uuid.New().String()[:8])
		c.Locals("request_id", "test-req-"+uuid.New().String()[:8])
		key := c.Get("Authorization")
		if len(key) > 7 && key[:7] == "Bearer " {
			key = key[7:]
		}
		if err := authService.Authenticate(key); err != nil {
			return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}
		return c.Next()
	})
	h.Register(app)
	return app, db
}

// newTraceStore creates a real SQLiteTraceStore over a fresh migrated DB.
func newTraceStore(t *testing.T) *database.SQLiteTraceStore {
	t.Helper()
	db := openTestDB(t)
	return database.NewSQLiteTraceStore(db)
}

// traceFixture builds a canonical DecisionTrace with the requested identity.
// Optional modifiers allow per-test dimension customization.
func traceFixture(id string, ts time.Time, mods ...func(*router.DecisionTrace)) *router.DecisionTrace {
	tr := &router.DecisionTrace{
		DecisionID:      router.DecisionID(id),
		TraceSchemaVer:  router.TraceSchemaVersion(),
		Timestamp:       ts.UTC(),
		RequestedMode:   "auto",
		ResolvedMode:    router.ModeDefault,
		ModeSource:      "explicit",
		ModeDescription: "no strong signal",
		EffectiveWeights: router.Weights{
			Health: 0.25, Latency: 0.25, Cost: 0.25, Capability: 0.25,
		},
		RuntimeHash: strings.Repeat("ab", 32),
		CandidateScores: []router.CandidateScore{
			{Provider: "openai", ProviderID: "gpt-4o", TotalScore: 0.9, Selected: true},
		},
		Winner: &router.ResolvedRoute{
			ProviderName:    "openai",
			ProviderModelID: "gpt-4o",
			ModelID:         "gpt-4o",
		},
		StageResults: []*router.StageResult{
			{Name: "intent", DurationMs: 1, Status: router.StageStatusCompleted},
			{Name: "selection", DurationMs: 2, Status: router.StageStatusCompleted},
		},
		Events: []router.EventRecord{
			{Type: "decision.started", Timestamp: ts.UTC()},
			{Type: "decision.finished", Timestamp: ts.UTC()},
		},
	}
	for _, m := range mods {
		m(tr)
	}
	return tr
}

// seedTraces persists traces through the store.
func seedTraces(t *testing.T, store router.TraceStore, traces ...*router.DecisionTrace) {
	t.Helper()
	for _, tr := range traces {
		if err := store.Save(context.Background(), tr); err != nil {
			t.Fatalf("Save(%s): %v", tr.DecisionID, err)
		}
	}
}

// traceListResponse mirrors the list response envelope.
type traceListResponse struct {
	Data       []map[string]any `json:"data"`
	Pagination struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
		Count  int `json:"count"`
	} `json:"pagination"`
}

// getTraceList issues an authenticated GET against the trace list endpoint.
func getTraceList(t *testing.T, app *fiber.App, query string) (*http.Response, traceListResponse) {
	t.Helper()
	url := "/api/routing/traces"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	var payload traceListResponse
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal list: %v\nbody=%s", err, body)
	}
	return resp, payload
}

func TestListRoutingTraces(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	seedTraces(t, store,
		traceFixture("dec-1", base),
		traceFixture("dec-2", base.Add(time.Minute)),
		traceFixture("dec-3", base.Add(2*time.Minute)),
	)
	app, _ := setupTraceQueryApp(t, store)

	resp, payload := getTraceList(t, app, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(payload.Data) != 3 {
		t.Fatalf("data len = %d, want 3", len(payload.Data))
	}
	// Newest first.
	if payload.Data[0]["decision_id"] != "dec-3" || payload.Data[2]["decision_id"] != "dec-1" {
		t.Fatalf("order = %v, want newest-first", payload.Data[0]["decision_id"])
	}
	if payload.Pagination.Count != 3 || payload.Pagination.Limit != 50 || payload.Pagination.Offset != 0 {
		t.Fatalf("pagination = %+v", payload.Pagination)
	}
}

func TestGetRoutingTrace(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-single", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	req := httptest.NewRequest("GET", "/api/routing/traces/dec-single", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var trace router.DecisionTrace
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &trace); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if trace.DecisionID != "dec-single" {
		t.Fatalf("DecisionID = %q", trace.DecisionID)
	}
	if trace.Winner == nil || trace.Winner.ProviderName != "openai" {
		t.Fatalf("winner = %+v", trace.Winner)
	}
	if len(trace.CandidateScores) != 1 || len(trace.StageResults) != 2 {
		t.Fatalf("full payload incomplete: scores=%d stages=%d", len(trace.CandidateScores), len(trace.StageResults))
	}
}

func TestGetRoutingTraceNotFound(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-1", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	req := httptest.NewRequest("GET", "/api/routing/traces/does-not-exist", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var envelope map[string]map[string]any
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errObj, ok := envelope["error"]
	if !ok {
		t.Fatalf("missing error envelope: %s", body)
	}
	if errObj["code"] != "trace_not_found" {
		t.Fatalf("error code = %v, want trace_not_found", errObj["code"])
	}
}

func TestRoutingTraceLimitDefault(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	traces := make([]*router.DecisionTrace, 0, 5)
	for i := 0; i < 5; i++ {
		traces = append(traces, traceFixture(fmt.Sprintf("dec-d-%d", i), base.Add(time.Duration(i)*time.Minute)))
	}
	seedTraces(t, store, traces...)
	app, _ := setupTraceQueryApp(t, store)

	resp, payload := getTraceList(t, app, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if payload.Pagination.Limit != 50 {
		t.Fatalf("default limit = %d, want 50", payload.Pagination.Limit)
	}
	if payload.Pagination.Count != 5 {
		t.Fatalf("count = %d, want 5", payload.Pagination.Count)
	}
}

func TestRoutingTraceLimitMaximum(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	traces := make([]*router.DecisionTrace, 0, 5)
	for i := 0; i < 5; i++ {
		traces = append(traces, traceFixture(fmt.Sprintf("dec-m-%d", i), base.Add(time.Duration(i)*time.Minute)))
	}
	seedTraces(t, store, traces...)
	app, _ := setupTraceQueryApp(t, store)

	resp, payload := getTraceList(t, app, "limit=200")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if payload.Pagination.Limit != 200 {
		t.Fatalf("limit = %d, want 200", payload.Pagination.Limit)
	}
}

func TestRoutingTraceInvalidLimit(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-1", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	for _, q := range []string{"limit=0", "limit=-5", "limit=201", "limit=abc", "limit=1.5"} {
		resp, _ := getTraceList(t, app, q)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("limit=%q status = %d, want 400", q, resp.StatusCode)
		}
	}
}

func TestRoutingTraceInvalidOffset(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-1", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	for _, q := range []string{"offset=-1", "offset=abc", "offset=-100"} {
		resp, _ := getTraceList(t, app, q)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("offset=%q status = %d, want 400", q, resp.StatusCode)
		}
	}
}

func TestRoutingTraceInvalidTime(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-1", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	for _, q := range []string{
		"from=not-a-time",
		"to=2026-13-99T00:00:00Z",
		"from=2026-08-19", // date-only is not RFC3339
	} {
		resp, _ := getTraceList(t, app, q)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%q status = %d, want 400", q, resp.StatusCode)
		}
	}

	// from > to is rejected.
	resp, _ := getTraceList(t, app, "from=2026-08-20T00:00:00Z&to=2026-08-19T00:00:00Z")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("from>to status = %d, want 400", resp.StatusCode)
	}
}

func TestRoutingTraceTimeRange(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC().Add(-24 * time.Hour)
	seedTraces(t, store,
		traceFixture("dec-old", base),
		traceFixture("dec-mid", base.Add(6*time.Hour)),
		traceFixture("dec-new", base.Add(12*time.Hour)),
	)
	app, _ := setupTraceQueryApp(t, store)

	from := base.Add(5 * time.Hour).Format(time.RFC3339)
	to := base.Add(10 * time.Hour).Format(time.RFC3339)
	_, payload := getTraceList(t, app, "from="+from+"&to="+to)

	ids := make([]string, 0, len(payload.Data))
	for _, d := range payload.Data {
		ids = append(ids, d["decision_id"].(string))
	}
	if len(ids) != 1 || ids[0] != "dec-mid" {
		t.Fatalf("window = %v, want only dec-mid", ids)
	}
}

func TestRoutingTraceModeFilter(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC()
	agentic := traceFixture("dec-mode-agentic", base)
	agentic.RequestedMode = "agentic"
	agentic.ResolvedMode = router.ModeAgentic
	seedTraces(t, store,
		traceFixture("dec-mode-default", base.Add(-time.Minute)),
		agentic,
	)
	app, _ := setupTraceQueryApp(t, store)

	// Canonical filtering on resolved_mode; case-insensitive input accepted.
	_, payload := getTraceList(t, app, "mode=AGENTIC")
	if len(payload.Data) != 1 || payload.Data[0]["decision_id"] != "dec-mode-agentic" {
		t.Fatalf("mode filter = %v", payload.Data)
	}
	if payload.Data[0]["resolved_mode"] != "agentic" {
		t.Fatalf("resolved_mode = %v", payload.Data[0]["resolved_mode"])
	}

	// Unknown modes are rejected with a clear error.
	resp, _ := getTraceList(t, app, "mode=banana")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("mode=banana status = %d, want 400", resp.StatusCode)
	}
}

func TestRoutingTraceProviderFilter(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC()
	other := traceFixture("dec-prov-groq", base)
	other.Winner.ProviderName = "groq"
	other.Winner.ProviderModelID = "llama-3-8b"
	other.CandidateScores[0].Provider = "groq"
	seedTraces(t, store,
		traceFixture("dec-prov-openai", base.Add(-time.Minute)),
		other,
	)
	app, _ := setupTraceQueryApp(t, store)

	_, payload := getTraceList(t, app, "provider=groq")
	if len(payload.Data) != 1 || payload.Data[0]["decision_id"] != "dec-prov-groq" {
		t.Fatalf("provider filter = %v", payload.Data)
	}
	if payload.Data[0]["selected_provider"] != "groq" {
		t.Fatalf("selected_provider = %v", payload.Data[0]["selected_provider"])
	}
}

func TestRoutingTraceModelFilter(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC()
	other := traceFixture("dec-model-llama", base)
	other.Winner.ProviderModelID = "llama-3-8b"
	other.CandidateScores[0].ProviderID = "llama-3-8b"
	seedTraces(t, store,
		traceFixture("dec-model-gpt4o", base.Add(-time.Minute)),
		other,
	)
	app, _ := setupTraceQueryApp(t, store)

	_, payload := getTraceList(t, app, "model=llama-3-8b")
	if len(payload.Data) != 1 || payload.Data[0]["decision_id"] != "dec-model-llama" {
		t.Fatalf("model filter = %v", payload.Data)
	}
	if payload.Data[0]["selected_model"] != "llama-3-8b" {
		t.Fatalf("selected_model = %v", payload.Data[0]["selected_model"])
	}
}

func TestRoutingTraceRuntimeHashFilter(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC()
	other := traceFixture("dec-hash-b", base)
	other.RuntimeHash = strings.Repeat("cd", 32)
	seedTraces(t, store,
		traceFixture("dec-hash-a", base.Add(-time.Minute)),
		other,
	)
	app, _ := setupTraceQueryApp(t, store)

	_, payload := getTraceList(t, app, "runtime_hash="+strings.Repeat("cd", 32))
	if len(payload.Data) != 1 || payload.Data[0]["decision_id"] != "dec-hash-b" {
		t.Fatalf("runtime_hash filter = %v", payload.Data)
	}
}

func TestRoutingTraceOutcomeFilter(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC()
	failed := traceFixture("dec-failed", base)
	failed.Winner = nil
	failed.CandidateScores = nil
	failed.StageResults = []*router.StageResult{
		{Name: "intent", DurationMs: 5, Status: router.StageStatusFailed},
	}
	seedTraces(t, store,
		traceFixture("dec-selected", base.Add(-time.Minute)),
		failed,
	)
	app, _ := setupTraceQueryApp(t, store)

	_, payload := getTraceList(t, app, "outcome=failed")
	if len(payload.Data) != 1 || payload.Data[0]["decision_id"] != "dec-failed" {
		t.Fatalf("outcome filter = %v", payload.Data)
	}
	if payload.Data[0]["outcome"] != "failed" {
		t.Fatalf("outcome = %v, want failed", payload.Data[0]["outcome"])
	}
}

func TestRoutingTracePagination(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	traces := make([]*router.DecisionTrace, 0, 25)
	for i := 0; i < 25; i++ {
		traces = append(traces, traceFixture(fmt.Sprintf("dec-pg-%02d", i), base.Add(time.Duration(i)*time.Minute)))
	}
	seedTraces(t, store, traces...)
	app, _ := setupTraceQueryApp(t, store)

	resp1, page1 := getTraceList(t, app, "limit=10&offset=0")
	if resp1.StatusCode != http.StatusOK || len(page1.Data) != 10 {
		t.Fatalf("page1 = %d rows, status %d", len(page1.Data), resp1.StatusCode)
	}
	if page1.Data[0]["decision_id"] != "dec-pg-24" || page1.Data[9]["decision_id"] != "dec-pg-15" {
		t.Fatalf("page1 = %v", page1.Data[0]["decision_id"])
	}

	_, page3 := getTraceList(t, app, "limit=10&offset=20")
	if len(page3.Data) != 5 || page3.Data[0]["decision_id"] != "dec-pg-04" {
		t.Fatalf("page3 = %d rows, first %v", len(page3.Data), page3.Data[0]["decision_id"])
	}
	if page3.Pagination.Offset != 20 || page3.Pagination.Count != 5 {
		t.Fatalf("page3 pagination = %+v", page3.Pagination)
	}

	// Beyond the dataset: empty page, no error.
	_, empty := getTraceList(t, app, "limit=10&offset=100")
	if len(empty.Data) != 0 || empty.Pagination.Count != 0 {
		t.Fatalf("empty page = %+v", empty)
	}
}

func TestRoutingTraceStablePagination(t *testing.T) {
	store := newTraceStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	traces := make([]*router.DecisionTrace, 0, 25)
	for i := 0; i < 25; i++ {
		traces = append(traces, traceFixture(fmt.Sprintf("dec-st-%02d", i), base.Add(time.Duration(i)*time.Minute)))
	}
	seedTraces(t, store, traces...)
	app, _ := setupTraceQueryApp(t, store)

	// Identical queries must produce identical page boundaries.
	first := func() []string {
		_, p := getTraceList(t, app, "limit=10&offset=10")
		ids := make([]string, 0, len(p.Data))
		for _, d := range p.Data {
			ids = append(ids, d["decision_id"].(string))
		}
		return ids
	}
	a, b := first(), first()
	if len(a) != len(b) {
		t.Fatalf("page sizes differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("page %d unstable: %v vs %v", i, a, b)
		}
	}
}

func TestRoutingTraceSummaryShape(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-shape", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	_, payload := getTraceList(t, app, "")
	if len(payload.Data) != 1 {
		t.Fatalf("data = %d rows", len(payload.Data))
	}
	row := payload.Data[0]

	for _, key := range []string{
		"decision_id", "timestamp", "schema_version", "requested_mode",
		"resolved_mode", "mode_source", "task_type", "selected_provider",
		"selected_model", "runtime_hash", "selected_score",
		"candidate_count", "outcome", "created_at",
	} {
		if _, ok := row[key]; !ok {
			t.Fatalf("summary missing key %q: %v", key, row)
		}
	}
	// Compact: the full payload must NOT be embedded in list items.
	for _, forbidden := range []string{"payload_json", "candidate_scores", "stage_results", "events", "effective_weights", "winner"} {
		if _, ok := row[forbidden]; ok {
			t.Fatalf("summary must not carry %q: %v", forbidden, row)
		}
	}
	// Timestamps are RFC3339 strings.
	if _, err := time.Parse(time.RFC3339, row["timestamp"].(string)); err != nil {
		t.Fatalf("timestamp not RFC3339: %v", row["timestamp"])
	}
	if row["decision_id"] != "dec-shape" || row["outcome"] != "selected" {
		t.Fatalf("summary values wrong: %v", row)
	}
}

func TestRoutingTraceFullPayload(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-full", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	req := httptest.NewRequest("GET", "/api/routing/traces/dec-full", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Canonical trace keys are all present — NOT the summary view.
	for _, key := range []string{
		"decision_id", "trace_schema_ver", "timestamp", "requested_mode",
		"resolved_mode", "mode_source", "effective_weights", "mode_bonuses",
		"runtime_hash", "candidate_scores", "winner", "stage_results", "events",
	} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("full payload missing key %q: %v", key, payload)
		}
	}
	if len(payload["candidate_scores"].([]any)) != 1 {
		t.Fatalf("candidate_scores not full: %v", payload["candidate_scores"])
	}
	if len(payload["stage_results"].([]any)) != 2 {
		t.Fatalf("stage_results not full: %v", payload["stage_results"])
	}
}

func TestRoutingTraceNoSecrets(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-sec", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	// List response.
	_, payload := getTraceList(t, app, "")
	body, _ := json.Marshal(payload)
	for _, needle := range []string{"sk-", "api_key", "Authorization", "Bearer ", "secret", "token"} {
		if strings.Contains(strings.ToLower(string(body)), needle) {
			t.Fatalf("list response leaks %q: %s", needle, body)
		}
	}

	// Single trace response.
	req := httptest.NewRequest("GET", "/api/routing/traces/dec-sec", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	single, _ := io.ReadAll(resp.Body)
	for _, needle := range []string{"sk-", "api_key", "Authorization", "Bearer ", "secret", "token"} {
		if strings.Contains(strings.ToLower(string(single)), needle) {
			t.Fatalf("single trace response leaks %q: %s", needle, single)
		}
	}
}

func TestRoutingTraceNoPrompt(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-clean", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	_, payload := getTraceList(t, app, "")
	body, _ := json.Marshal(payload)
	if strings.Contains(strings.ToLower(string(body)), "prompt") {
		t.Fatalf("list response leaks prompt data: %s", body)
	}

	req := httptest.NewRequest("GET", "/api/routing/traces/dec-clean", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	single, _ := io.ReadAll(resp.Body)
	if strings.Contains(strings.ToLower(string(single)), "prompt") {
		t.Fatalf("single trace response leaks prompt data: %s", single)
	}
}

func TestRoutingTraceReadOnly(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-ro", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	// Write verbs are not registered for either endpoint. Fiber answers 405
	// when the path exists with another method, 404 when it does not — both
	// mean no write route exists.
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		for _, path := range []string{"/api/routing/traces", "/api/routing/traces/dec-ro"} {
			req := httptest.NewRequest(method, path, nil)
			req.Header.Set("Authorization", "Bearer test-key")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s status = %d, want 404/405 (no write routes)", method, path, resp.StatusCode)
			}
		}
	}

	// Data is untouched: the original trace is still queryable.
	req := httptest.NewRequest("GET", "/api/routing/traces/dec-ro", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trace changed after write attempts: status %d", resp.StatusCode)
	}
}

func TestRoutingTraceAuth(t *testing.T) {
	store := newTraceStore(t)
	seedTraces(t, store, traceFixture("dec-auth", time.Now().UTC()))
	app, _ := setupTraceQueryApp(t, store)

	for _, path := range []string{"/api/routing/traces", "/api/routing/traces/dec-auth"} {
		req := httptest.NewRequest("GET", path, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET %s without key status = %d, want 401", path, resp.StatusCode)
		}
	}

	// Wrong key is rejected too.
	req := httptest.NewRequest("GET", "/api/routing/traces", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d, want 401", resp.StatusCode)
	}
}

func TestRoutingTracePersistenceUnavailable(t *testing.T) {
	app, _ := setupTraceQueryApp(t, nil) // no trace store wired

	for _, path := range []string{"/api/routing/traces", "/api/routing/traces/any-id"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer test-key")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("GET %s status = %d, want 503; body=%s", path, resp.StatusCode, body)
		}
		var envelope map[string]map[string]any
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if envelope["error"]["code"] != "trace_store_unavailable" {
			t.Fatalf("error code = %v, want trace_store_unavailable", envelope["error"]["code"])
		}
	}
}
