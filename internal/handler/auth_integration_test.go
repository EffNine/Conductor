package handler_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/auth"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/middleware"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

const integrationAuthKey = "test-secret-key"

// setupAuthIntegrationApp wires the production-equivalent middleware stack
// (correlation → request context → auth) in front of the real handler routes,
// with a working openai stub so authenticated inference requests succeed.
func setupAuthIntegrationApp(t *testing.T) *fiber.App {
	t.Helper()
	reg := provider.NewRegistry()
	reg.Register(&routingChatStubProvider{
		name: "openai",
		models: []provider.ModelInfo{
			{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"},
		},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	})

	cfg := &config.Config{
		APIKey: integrationAuthKey,
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	engine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)
	h.SetConfig(cfg)

	app := fiber.New()
	app.Use(middleware.CorrelationID())
	app.Use(middleware.RequestContextID())
	app.Use(middleware.Auth(auth.NewService(integrationAuthKey)))
	h.Register(app)
	return app
}

// authErrorCode extracts the error.code from a Conductor error envelope.
func authErrorCode(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("unmarshal error envelope: %v; body=%s", err, body)
	}
	return envelope.Error.Code
}

// chatBody is a minimal valid chat completion request for the stub route.
func chatBody(t *testing.T, stream bool) *strings.Reader {
	t.Helper()
	b, err := json.Marshal(apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
		Stream:   stream,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return strings.NewReader(string(b))
}

func TestAuthIntegration_ProtectedInferenceEndpoints(t *testing.T) {
	app := setupAuthIntegrationApp(t)

	t.Run("chat completions missing key", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", chatBody(t, false))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode, body)
		}
		if got := authErrorCode(t, string(body)); got != "missing_api_key" {
			t.Fatalf("error code = %q, want missing_api_key", got)
		}
	})

	t.Run("chat completions wrong key", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", chatBody(t, false))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer wrong-key")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode, body)
		}
		if got := authErrorCode(t, string(body)); got != "invalid_api_key" {
			t.Fatalf("error code = %q, want invalid_api_key", got)
		}
	})

	t.Run("chat completions valid key proceeds", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", chatBody(t, false))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+integrationAuthKey)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
		}
	})

	t.Run("models list protected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/models", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}

		req = httptest.NewRequest("GET", "/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+integrationAuthKey)
		resp, err = app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("embeddings protected", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/embeddings", strings.NewReader(`{"model":"gpt-4o","input":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}

		req = httptest.NewRequest("POST", "/v1/embeddings", strings.NewReader(`{"model":"gpt-4o","input":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+integrationAuthKey)
		resp, err = app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})
}

func TestAuthIntegration_StreamingEndpoint(t *testing.T) {
	app := setupAuthIntegrationApp(t)

	t.Run("streaming without key rejected before stream starts", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", chatBody(t, true))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
			t.Fatalf("auth failure must not start an SSE stream; Content-Type = %q", ct)
		}
		if got := authErrorCode(t, string(body)); got != "missing_api_key" {
			t.Fatalf("error code = %q, want missing_api_key", got)
		}
	})

	t.Run("streaming with valid key streams", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", chatBody(t, true))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+integrationAuthKey)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
			t.Fatalf("Content-Type = %q, want text/event-stream", ct)
		}
		if !strings.Contains(string(body), "data:") {
			t.Fatalf("stream body missing SSE data frames: %s", body)
		}
	})
}

func TestAuthIntegration_HealthEndpointOpen(t *testing.T) {
	app := setupAuthIntegrationApp(t)
	req := httptest.NewRequest("GET", "/health", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
}

func TestAuthIntegration_DashboardEndpointsProtected(t *testing.T) {
	app := setupAuthIntegrationApp(t)
	for _, path := range []string{
		"/api/usage",
		"/api/config",
		"/api/metrics",
		"/api/models",
		"/api/routing/traces",
	} {
		req := httptest.NewRequest("GET", path, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET %s status = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestAuthIntegration_ConfigRedacted(t *testing.T) {
	app := setupAuthIntegrationApp(t)

	req := httptest.NewRequest("GET", "/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+integrationAuthKey)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	if strings.Contains(string(body), integrationAuthKey) {
		t.Fatalf("/api/config leaks the gateway API key: %s", body)
	}
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	// The existing /api/config JSON contract exposes the field as "APIKey"
	// (Go default casing); the value must always be redacted.
	if cfg["APIKey"] != "[REDACTED]" {
		t.Fatalf("APIKey = %v, want [REDACTED]", cfg["APIKey"])
	}
}

func TestAuthIntegration_TraceQueryBehindAuth(t *testing.T) {
	app := setupAuthIntegrationApp(t)

	// No trace store wired: auth must gate first (401), then the endpoint
	// answers 503 for authenticated callers — P3.16 contract preserved.
	req := httptest.NewRequest("GET", "/api/routing/traces", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without credentials", resp.StatusCode)
	}

	req = httptest.NewRequest("GET", "/api/routing/traces", nil)
	req.Header.Set("Authorization", "Bearer "+integrationAuthKey)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (store unavailable); body=%s", resp.StatusCode, body)
	}
}

func TestAuthIntegration_NoKeyLeakInAnyResponse(t *testing.T) {
	app := setupAuthIntegrationApp(t)

	// 401 challenge with a distinctive bogus credential must not echo it.
	req := httptest.NewRequest("POST", "/v1/chat/completions", chatBody(t, false))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer bogus-credential-ABCDEF123456")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, needle := range []string{"bogus-credential-ABCDEF123456", integrationAuthKey, "Authorization"} {
		if strings.Contains(string(body), needle) {
			t.Fatalf("401 response leaks %q: %s", needle, body)
		}
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q, want Bearer", got)
	}
}
