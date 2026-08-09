package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/auth"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestHandleUsageReturnsTotalsAndBreakdowns(t *testing.T) {
	app, db := setupTestApp(t)
	seedUsage(db)

	req, _ := http.NewRequest("GET", "/api/usage", nil)
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

	var result handler.UsageSummary
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if result.Total.Requests != 3 {
		t.Fatalf("Total.Requests = %d, want 3", result.Total.Requests)
	}
	if result.Total.PromptTokens != 30 {
		t.Fatalf("Total.PromptTokens = %d, want 30", result.Total.PromptTokens)
	}
	if _, ok := result.ByProvider["openai"]; !ok {
		t.Fatalf("missing openai in ByProvider: %v", result.ByProvider)
	}
	if _, ok := result.ByModel["gpt-4o"]; !ok {
		t.Fatalf("missing gpt-4o in ByModel: %v", result.ByModel)
	}
	if result.ByProvider["openai"].Requests != 2 {
		t.Fatalf("openai requests = %d, want 2", result.ByProvider["openai"].Requests)
	}
}

func TestHandleUsageRequiresAPIKey(t *testing.T) {
	app, _ := setupTestApp(t)
	req, _ := http.NewRequest("GET", "/api/usage", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestHandleLogsReturnsRecentRequestLogs(t *testing.T) {
	app, db := setupTestApp(t)
	now := time.Now().UTC()
	db.DB.Create(&database.RequestLog{
		ID:         "log-1",
		RequestID:  "req-1",
		Method:     "POST",
		Path:       "/v1/chat/completions",
		StatusCode: 200,
		Provider:   "openai",
		Model:      "gpt-4o",
		CreatedAt:  now,
	})

	req, _ := http.NewRequest("GET", "/api/logs", nil)
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

	var payload map[string][]database.RequestLog
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	logs := payload["logs"]
	if len(logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(logs))
	}
	if logs[0].RequestID != "req-1" {
		t.Fatalf("RequestID = %q, want req-1", logs[0].RequestID)
	}
}

func TestHandleDashboardModelsReturnsMergedCatalog(t *testing.T) {
	app, _ := setupTestApp(t)
	req, _ := http.NewRequest("GET", "/api/models", nil)
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
	var payload map[string][]catalog.Entry
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := payload["models"]; !ok {
		t.Fatalf("missing models key: %v", payload)
	}
}

func TestHandleMetricsReturnsPrometheusFormat(t *testing.T) {
	app, _ := setupTestApp(t)
	req, _ := http.NewRequest("GET", "/api/metrics", nil)
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

	contentType := resp.Header.Get("Content-Type")
	if contentType != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; version=0.0.4; charset=utf-8", contentType)
	}

	body, _ := io.ReadAll(resp.Body)
	output := string(body)

	// Verify key metrics are present
	if !strings.Contains(output, "conductor_requests_total") {
		t.Fatal("missing conductor_requests_total in metrics output")
	}
	if !strings.Contains(output, "conductor_errors_total") {
		t.Fatal("missing conductor_errors_total in metrics output")
	}
	if !strings.Contains(output, "conductor_provider_latency_ms") {
		t.Fatal("missing conductor_provider_latency_ms in metrics output")
	}
	if !strings.Contains(output, "conductor_uptime_seconds") {
		t.Fatal("missing conductor_uptime_seconds in metrics output")
	}
	if !strings.Contains(output, "conductor_build_info") {
		t.Fatal("missing conductor_build_info in metrics output")
	}
}

func setupTestApp(t *testing.T) (*fiber.App, *database.Database) {
	t.Helper()
	cfg := &config.Config{APIKey: "test-key"}
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{name: "openai"})
	reg.Register(&stubProvider{name: "groq"})

	db := openTestDB(t)
	routerEngine := router.NewEngine(cfg, reg)
	modelCatalog := catalog.New(reg, nil)
	usageTracker := usage.NewTracker(db, nil, zap.NewNop())
	h := handler.New(routerEngine, reg, usageTracker, zap.NewNop(), modelCatalog, db)

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

func seedUsage(db *database.Database) {
	now := time.Now().UTC()
	records := []database.UsageRecord{
		{ID: "u1", RequestID: "r1", ModelID: "gpt-4o", ProviderModelID: "gpt-4o", Provider: "openai", PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Requests: 1, LatencyMs: 100, DurationMs: 100, StatusCode: 200, CreatedAt: now},
		{ID: "u2", RequestID: "r2", ModelID: "gpt-4o", ProviderModelID: "gpt-4o", Provider: "openai", PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30, Requests: 1, LatencyMs: 200, DurationMs: 200, StatusCode: 200, CreatedAt: now},
		{ID: "u3", RequestID: "r3", ModelID: "llama3-8b", ProviderModelID: "llama3-8b", Provider: "groq", PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0, Requests: 1, LatencyMs: 50, DurationMs: 50, StatusCode: 200, CreatedAt: now},
	}
	for _, r := range records {
		if err := db.DB.Create(&r).Error; err != nil {
			panic(err)
		}
	}
}

func openTestDB(t testing.TB) *database.Database {
	t.Helper()
	db, err := database.Connect(&config.DatabaseConfig{
		Driver:       "sqlite",
		DSN:          "file:" + uuid.New().String() + "?mode=memory&cache=shared",
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

type stubProvider struct {
	name   string
	models []provider.ModelInfo
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return s.models, nil
}

func (s *stubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}

func (s *stubProvider) SupportsModel(string) bool { return false }

func (s *stubProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}
