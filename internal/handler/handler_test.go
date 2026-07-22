package handler_test

import (
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

func TestListModelsExcludesAliases(t *testing.T) {
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
	engine := router.NewEngine(cfg, reg)
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
		t.Fatalf("ReadAll: %v", err)
	}

	var list apitypes.ModelList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("Unmarshal: %v\nbody=%s", err, body)
	}

	ids := make([]string, 0, len(list.Data))
	foundGPT := false
	for _, m := range list.Data {
		ids = append(ids, m.ID)
		if m.ID == "fast" {
			t.Fatalf("alias %q must not appear in /v1/models: %v", m.ID, ids)
		}
		if m.ID == "openai/gpt-4o" {
			foundGPT = true
			if m.Name != "gpt-4o" {
				t.Fatalf("name = %q, want gpt-4o (short display without provider prefix)", m.Name)
			}
		}
	}
	if !foundGPT {
		t.Fatalf("missing openai/gpt-4o in %v", ids)
	}
}
