package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EffNine/conductor/internal/auth"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/middleware"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const p45AuthKey = "p45-secret-key"

// setupFailuresApp builds the production-equivalent auth middleware stack in
// front of the failure analytics endpoints with a seeded attempt store.
func setupFailuresApp(t *testing.T, seed func(store *database.AttemptStore)) *fiber.App {
	t.Helper()
	reg := provider.NewRegistry()
	cfg := &config.Config{APIKey: p45AuthKey}
	engine, err := router.NewEngine(cfg, reg)
	require.NoError(t, err)

	db := openTestDB(t)
	require.NoError(t, db.Migrate())
	store := database.NewAttemptStore(db)
	if seed != nil {
		seed(store)
	}

	cat := catalog.New(reg, nil)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)
	h.SetConfig(cfg)
	h.SetAttemptStore(store)

	app := fiber.New()
	app.Use(middleware.CorrelationID())
	app.Use(middleware.RequestContextID())
	app.Use(middleware.Auth(auth.NewService(p45AuthKey)))
	h.Register(app)
	return app
}

func p45Get(t *testing.T, app *fiber.App, path, key string) (*http.Response, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()

	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)
	return resp, body
}

func p45Seed(store *database.AttemptStore, rec database.AttemptRecord) {
	t := struct{ Fail func(string, error) }{}
	_ = t
	if err := store.Save(context.Background(), rec); err != nil {
		panic(err)
	}
}

func TestFailuresEndpointsRequireAuth(t *testing.T) {
	for _, path := range []string{"/api/failures", "/api/failures/summary"} {
		app := setupFailuresApp(t, nil)

		resp, _ := p45Get(t, app, path, "")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "%s without key", path)

		resp, body := p45Get(t, app, path, p45AuthKey)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "%s with key", path)
		assert.NotContains(t, body, "error")
	}
}

func TestFailuresListFiltersPaginationAndEmptyStates(t *testing.T) {
	var store *database.AttemptStore
	app := setupFailuresApp(t, func(s *database.AttemptStore) {
		store = s
	})
	require.NotNil(t, store)

	mk := func(reqID, provider, class string) database.AttemptRecord {
		return database.AttemptRecord{
			RequestID: reqID, CorrelationID: "c-" + reqID,
			VirtualModel: "coding", Mode: "coding",
			Provider: provider, ProviderModelID: provider + "-model",
			CandidateIndex: 0, AttemptIndex: 0,
			FailureClass: class, Outcome: database.AttemptOutcomeFailed,
			HTTPStatus: 500,
		}
	}
	p45Seed(store, mk("r1", "groq", "rate_limited"))
	p45Seed(store, mk("r2", "groq", "upstream_error"))
	p45Seed(store, mk("r3", "openai", "rate_limited"))
	// Successes are never returned by the failures API.
	p45Seed(store, database.AttemptRecord{
		RequestID: "ok-1", Provider: "groq",
		Outcome: database.AttemptOutcomeSuccess,
	})

	_, all := p45Get(t, app, "/api/failures", p45AuthKey)
	assert.Equal(t, float64(3), all["total"], "successes must be excluded")

	_, byClass := p45Get(t, app, "/api/failures?class=rate_limited", p45AuthKey)
	assert.Equal(t, float64(2), byClass["total"])

	_, byProvider := p45Get(t, app, "/api/failures?provider=openai", p45AuthKey)
	assert.Equal(t, float64(1), byProvider["total"])

	_, byModel := p45Get(t, app, "/api/failures?model=coding", p45AuthKey)
	assert.Equal(t, float64(3), byModel["total"])

	_, combined := p45Get(t, app, "/api/failures?provider=groq&class=upstream_error", p45AuthKey)
	assert.Equal(t, float64(1), combined["total"])

	_, page1 := p45Get(t, app, "/api/failures?limit=2&offset=0", p45AuthKey)
	attempts1 := page1["attempts"].([]any)
	assert.Len(t, attempts1, 2)
	assert.Equal(t, float64(3), page1["total"], "total is pagination-independent")

	_, page2 := p45Get(t, app, "/api/failures?limit=2&offset=2", p45AuthKey)
	attempts2 := page2["attempts"].([]any)
	assert.Len(t, attempts2, 1)

	// Unknown filter values yield empty states, not errors.
	_, empty := p45Get(t, app, "/api/failures?provider=nobody", p45AuthKey)
	assert.Equal(t, float64(0), empty["total"])
	emptyAttempts := empty["attempts"].([]any)
	assert.Empty(t, emptyAttempts)

	// Empty summary state needs a fresh, unseeded store: freshly seeded
	// rows are inside any positive window by construction.
	fresh := setupFailuresApp(t, nil)
	_, emptySummary := p45Get(t, fresh, "/api/failures/summary?window=1m&bucket=1m", p45AuthKey)
	assert.Equal(t, float64(0), emptySummary["total_failures"])
	assert.NotContains(t, emptySummary, "error")
}

func TestFailuresSummaryAggregation(t *testing.T) {
	app := setupFailuresApp(t, func(store *database.AttemptStore) {
		p45Seed(store, database.AttemptRecord{
			RequestID: "s1", Provider: "groq", FailureClass: "rate_limited",
			Outcome: database.AttemptOutcomeFailed, HTTPStatus: 429,
		})
		p45Seed(store, database.AttemptRecord{
			RequestID: "s2", Provider: "groq", FailureClass: "timeout",
			Outcome: database.AttemptOutcomeFailed, HTTPStatus: 504,
		})
		p45Seed(store, database.AttemptRecord{
			RequestID: "s3", Provider: "openai", FailureClass: "rate_limited",
			Outcome: database.AttemptOutcomeSkipped, SkipReason: "circuit_breaker_open",
		})
	})

	resp, summary := p45Get(t, app, "/api/failures/summary?window=24h&bucket=6h", p45AuthKey)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, float64(3), summary["total_failures"])

	byProvider := summary["by_provider"].(map[string]any)
	assert.Equal(t, float64(2), byProvider["groq"])
	assert.Equal(t, float64(1), byProvider["openai"])

	byClass := summary["by_class"].(map[string]any)
	assert.Equal(t, float64(2), byClass["rate_limited"])
	assert.Equal(t, float64(1), byClass["timeout"])

	buckets := summary["buckets"].([]any)
	totalInBuckets := int64(0)
	for _, b := range buckets {
		bucketMap := b.(map[string]any)
		n := bucketMap["count"].(float64)
		totalInBuckets += int64(n)
		assert.Contains(t, bucketMap, "bucket_start")
	}
	assert.Equal(t, int64(3), totalInBuckets, "buckets must cover all failures")
}

func TestFailuresValidationErrors(t *testing.T) {
	app := setupFailuresApp(t, nil)

	cases := []struct {
		path string
		want int
	}{
		{"/api/failures?window=nope", http.StatusBadRequest},
		{"/api/failures?limit=-5", http.StatusBadRequest},
		{"/api/failures?offset=abc", http.StatusBadRequest},
		{"/api/failures/summary?window=zero", http.StatusBadRequest},
		{"/api/failures/summary?bucket=fugitive", http.StatusBadRequest},
	}
	for _, tc := range cases {
		resp, _ := p45Get(t, app, tc.path, p45AuthKey)
		assert.Equal(t, tc.want, resp.StatusCode, tc.path)
	}
}

func TestFailuresUnavailableWithoutStore(t *testing.T) {
	reg := provider.NewRegistry()
	cfg := &config.Config{APIKey: p45AuthKey}
	engine, err := router.NewEngine(cfg, reg)
	require.NoError(t, err)
	h := handler.New(engine, reg, nil, zap.NewNop(), catalog.New(reg, nil), openTestDB(t))
	h.SetConfig(cfg)
	app := fiber.New()
	app.Use(middleware.Auth(auth.NewService(p45AuthKey)))
	h.Register(app)

	for _, path := range []string{"/api/failures", "/api/failures/summary"} {
		resp, body := p45Get(t, app, path, p45AuthKey)
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, path)
		errObj, ok := body["error"].(map[string]any)
		require.True(t, ok, path)
		assert.Equal(t, "attempts_unavailable", errObj["code"], path)
	}
}
