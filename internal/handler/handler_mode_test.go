package handler_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// TestHandlerInvalidModeReturnsBadRequest verifies that an invalid mode value
// produces a 400 Bad Request with the existing error format.
func TestHandlerInvalidModeReturnsBadRequest(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name: "openai",
		models: []provider.ModelInfo{
			{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"},
		},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)

	app := fiber.New()
	h.Register(app)

	body := strings.NewReader(`{"model":"gpt-4o","mode":"reasonning","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != fiber.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, string(b))
	}

	var errResp apitypes.ErrorResponse
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &errResp); err != nil {
		t.Fatalf("Unmarshal error response: %v", err)
	}
	if errResp.Error.Param != "mode" {
		t.Errorf("error param = %q, want %q", errResp.Error.Param, "mode")
	}
	if errResp.Error.Code != "invalid_request" {
		t.Errorf("error code = %q, want %q", errResp.Error.Code, "invalid_request")
	}
}

// TestHandlerValidModeForwardedToPipeline verifies that a valid explicit mode
// is accepted and forwarded through the pipeline.
func TestHandlerValidModeForwardedToPipeline(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name: "openai",
		models: []provider.ModelInfo{
			{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"},
		},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	groq := &routingChatStubProvider{
		name: "groq",
		models: []provider.ModelInfo{
			{ProviderModelID: "llama-3.1-8b-instruct", ModelID: "llama-3.1-8b-instruct", OwnedBy: "groq"},
		},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-2", Model: "llama-3.1-8b-instruct",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "hi"}}},
		},
	}
	reg.Register(openai)
	reg.Register(groq)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("openai", openai))
	_ = store.Register(runtime.NewProviderRuntime("groq", groq))
	manager := runtime.NewManager(store)
	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(200)
		return nil
	})

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.RoutingWeights{Health: 10, Latency: 80, Cost: 5, Capability: 5},
	})

	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	h.SetRoutingEngine(routingEngine)

	app := fiber.New()
	h.Register(app)

	body := strings.NewReader(`{"model":"gpt-4o","mode":"fast","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	// With fast mode (latency-dominant), openai (50ms) should win over groq (200ms).
	if openai.callCount != 1 {
		t.Fatalf("expected openai (lowest latency) called once, got %d", openai.callCount)
	}
}

// TestHandlerOmittedModePreservesBackwardCompatibility verifies that requests
// without a mode field continue to work exactly as before.
func TestHandlerOmittedModePreservesBackwardCompatibility(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name: "openai",
		models: []provider.ModelInfo{
			{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"},
		},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)

	app := fiber.New()
	h.Register(app)

	body := strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	if openai.callCount != 1 {
		t.Fatalf("expected openai called once, got %d", openai.callCount)
	}
}

// TestHandlerEmbeddingsIgnoresMode verifies that the embeddings endpoint does
// not accidentally apply chat mode routing semantics.
func TestHandlerEmbeddingsIgnoresMode(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name: "openai",
		models: []provider.ModelInfo{
			{ProviderModelID: "text-embedding-3-small", ModelID: "text-embedding-3-small", OwnedBy: "openai"},
		},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "text-embedding-3-small",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"embedding": {Provider: "openai", ModelID: "text-embedding-3-small"},
		},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)

	app := fiber.New()
	h.Register(app)

	embBody := `{"model":"embedding","input":["hello world"],"mode":"fast"}`
	req := httptest.NewRequest("POST", "/v1/embeddings", strings.NewReader(embBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for embeddings, got %d: %s", resp.StatusCode, string(b))
	}
}
