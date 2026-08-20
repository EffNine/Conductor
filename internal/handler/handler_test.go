package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// routingChatStubProvider is a stub provider for chat completion tests.
type routingChatStubProvider struct {
	name      string
	models    []provider.ModelInfo
	response  *apitypes.ChatCompletionResponse
	callCount int
}

func (s *routingChatStubProvider) Name() string                 { return s.name }
func (s *routingChatStubProvider) SupportsModel(id string) bool { return true }
func (s *routingChatStubProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	s.callCount++
	return s.response, nil
}
func (s *routingChatStubProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	s.callCount++
	ch := make(chan apitypes.StreamChunk, 1)
	ch <- apitypes.StreamChunk{
		Choices: []apitypes.Choice{{
			Index: 0,
			Message: &apitypes.Message{
				Role:    "assistant",
				Content: "ok",
			},
		}},
	}
	close(ch)
	return ch, nil
}
func (s *routingChatStubProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	s.callCount++
	return &apitypes.EmbeddingResponse{
		Object: "list",
		Data: []apitypes.EmbeddingData{
			{
				Object:    "embedding",
				Embedding: []float64{0.1, 0.2, 0.3},
				Index:     0,
			},
		},
		Model: req.Model,
		Usage: &apitypes.Usage{
			PromptTokens:     1,
			CompletionTokens: 0,
			TotalTokens:      1,
		},
	}, nil
}
func (s *routingChatStubProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return s.models, nil
}
func (s *routingChatStubProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *routingChatStubProvider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true, LatencyMs: 10}, nil
}
func (s *routingChatStubProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}

func TestListModelsIncludesVirtualModels(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "openai",
		models: []provider.ModelInfo{
			{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"},
		},
	})

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"gpt-4o": {Provider: "openai"},
		},
		Aliases: map[string]string{
			"fast": "gpt-4o",
		},
	}
	engine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)

	app := fiber.New()
	h.Register(app)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v\nbody=%s", err, body)
	}

	var list apitypes.ModelList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("Unmarshal: %v\nbody=%s", err, body)
	}

	ids := make([]string, 0, len(list.Data))
	foundVirtual := false
	for _, m := range list.Data {
		ids = append(ids, m.ID)
		// Check that virtual models are present
		if m.ID == "frontier" || m.ID == "coding" || m.ID == "reasoning" || m.ID == "agentic" || m.ID == "planning" || m.ID == "long_horizon" || m.ID == "fast" || m.ID == "light" || m.ID == "vision" || m.ID == "auto" {
			foundVirtual = true
			if m.OwnedBy != "conductor" {
				t.Fatalf("virtual model %q must have owned_by=conductor, got %q", m.ID, m.OwnedBy)
			}
		}
	}
	if !foundVirtual {
		t.Fatalf("virtual models must be advertised in %v", ids)
	}
	// The alias "fast" should appear as a virtual model (owned_by=conductor), not as a non-virtual entry
	for _, m := range list.Data {
		if m.ID == "fast" && m.OwnedBy != "conductor" {
			t.Fatalf("alias %q must appear as virtual in /v1/models: %v", m.ID, ids)
		}
	}
	// Raw provider model IDs must NOT appear in /v1/models
	for _, m := range list.Data {
		if m.ID != "frontier" && m.ID != "coding" && m.ID != "reasoning" && m.ID != "agentic" && m.ID != "planning" && m.ID != "long_horizon" && m.ID != "fast" && m.ID != "light" && m.ID != "vision" && m.ID != "auto" {
			t.Fatalf("raw provider ID %q must not appear in /v1/models: %v", m.ID, ids)
		}
	}
}
