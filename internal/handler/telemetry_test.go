package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
	runtimeadapter "github.com/EffNine/conductor/internal/runtime/adapter"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

func TestTelemetryRecordsProviderAndModel(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:     "openai",
		models:   []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{Model: "gpt-4o", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}}},
	}
	reg.Register(openai)

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("openai", openai))
	manager := runtime.NewManager(store)
	eventBus := eventbus.NewEventBus()
	execAdapter := runtimeadapter.NewExecutionToRuntimeAdapter(store, eventBus)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"gpt-4o": {Provider: "openai"},
		},
	}
	engine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)
	h.SetRuntimeManager(manager)
	h.SetExecutionAdapter(execAdapter)

	app := fiber.New()
	h.Register(app)

	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	got, _ := store.Get("openai")
	snap := got.Snapshot(context.Background())
	if snap.ExecutionSuccessCount != 1 {
		t.Errorf("expected 1 execution success, got %d", snap.ExecutionSuccessCount)
	}
}

func TestTelemetryDoesNotChangeNonStreamingBehavior(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:     "openai",
		models:   []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{Model: "gpt-4o", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}}},
	}
	reg.Register(openai)

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("openai", openai))
	manager := runtime.NewManager(store)
	execAdapter := runtimeadapter.NewExecutionToRuntimeAdapter(store, nil)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"gpt-4o": {Provider: "openai"},
		},
	}
	engine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)
	h.SetRuntimeManager(manager)
	h.SetExecutionAdapter(execAdapter)

	app := fiber.New()
	h.Register(app)

	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result apitypes.ChatCompletionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Choices[0].Message.Content != "ok" {
		t.Errorf("expected content 'ok', got %q", result.Choices[0].Message.Content)
	}
}

func TestTelemetryDoesNotChangeStreamingBehavior(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:     "openai",
		models:   []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{Model: "gpt-4o", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}}},
	}
	reg.Register(openai)

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("openai", openai))
	manager := runtime.NewManager(store)
	execAdapter := runtimeadapter.NewExecutionToRuntimeAdapter(store, nil)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"gpt-4o": {Provider: "openai"},
		},
	}
	engine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)
	h.SetRuntimeManager(manager)
	h.SetExecutionAdapter(execAdapter)

	app := fiber.New()
	h.Register(app)

	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}, Stream: true}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
