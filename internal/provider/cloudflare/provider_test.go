package cloudflare_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/cloudflare"
)

var _ provider.Provider = (*cloudflare.Provider)(nil)

func TestProviderDefaults(t *testing.T) {
	p := cloudflare.NewProvider("test-token", "", 10*time.Second)
	if p.Name() != "cloudflare" {
		t.Fatalf("Name() = %q, want cloudflare", p.Name())
	}
	if got := p.GetMetadata().BaseURL; got != "https://api.cloudflare.com/client/v4" {
		t.Fatalf("default base URL = %q, want https://api.cloudflare.com/client/v4", got)
	}

	custom := cloudflare.NewProvider("test-token", "http://example.com/client/v4/accounts/ACC123", 10*time.Second)
	meta := custom.GetMetadata()
	if meta.BaseURL != "http://example.com/client/v4/accounts/ACC123" {
		t.Fatalf("base URL = %q", meta.BaseURL)
	}
	if meta.DisplayName != "Cloudflare Workers AI" {
		t.Fatalf("display name = %q, want Cloudflare Workers AI", meta.DisplayName)
	}
	if meta.Capabilities.Streaming {
		t.Fatal("expected Streaming = false")
	}
	if !meta.Capabilities.Reasoning || !meta.Capabilities.LongContext {
		t.Fatalf("capabilities = %+v, want reasoning and long context", meta.Capabilities)
	}
}

func TestChatCompletionRunsModelWithAuthAndPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/ai/run/@hf/meta-llama/llama-3-8b-instruct") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want Bearer test-token", got)
		}

		var req struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Prompt != "hello world" {
			t.Fatalf("prompt = %q, want hello world", req.Prompt)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result":  map[string]any{"response": "Hi there"},
			"success": true,
		})
	}))
	defer server.Close()

	p := cloudflare.NewProvider("test-token", server.URL+"/accounts/ACC123", 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "@hf/meta-llama/llama-3-8b-instruct",
		Messages: []apitypes.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hello world"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("len(choices) = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Fatalf("role = %q, want assistant", resp.Choices[0].Message.Role)
	}
	if got := resp.Choices[0].Message.Content; got != "Hi there" {
		t.Fatalf("content = %v, want Hi there", got)
	}
	if *resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish reason = %v, want stop", resp.Choices[0].FinishReason)
	}
	if resp.Usage.PromptTokens != 2 || resp.Usage.CompletionTokens != 2 || resp.Usage.TotalTokens != 4 {
		t.Fatalf("usage = %+v, want 2/2/4 estimated tokens", resp.Usage)
	}
}

func TestChatCompletionPicksLastUserStringMessage(t *testing.T) {
	var gotPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotPrompt = req.Prompt
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"response": "ok"}, "success": true})
	}))
	defer server.Close()

	p := cloudflare.NewProvider("test-token", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "@hf/meta-llama/llama-3-8b-instruct",
		Messages: []apitypes.Message{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "reply"},
			{Role: "user", Content: "second"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if gotPrompt != "second" {
		t.Fatalf("prompt = %q, want second", gotPrompt)
	}
}

func TestChatCompletionExtractsTextFromContentParts(t *testing.T) {
	var gotPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotPrompt = req.Prompt
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"response": "ok"}, "success": true})
	}))
	defer server.Close()

	p := cloudflare.NewProvider("test-token", server.URL, 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model: "@hf/meta-llama/llama-3-8b-instruct",
		Messages: []apitypes.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "http://example.com/img.png"}},
				{Type: apitypes.ContentPartText, Text: "what is in this image?"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if gotPrompt != "what is in this image?" {
		t.Fatalf("prompt = %q, want 'what is in this image?'", gotPrompt)
	}
}

func TestChatCompletionRejectsMissingUserMessage(t *testing.T) {
	p := cloudflare.NewProvider("test-token", "https://api.cloudflare.com/client/v4/accounts/ACC123", 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "@hf/meta-llama/llama-3-8b-instruct",
		Messages: []apitypes.Message{{Role: "system", Content: "be brief"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	provErr, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if provErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", provErr.StatusCode)
	}
	if provErr.Type != provider.ErrorTypeInvalidRequest {
		t.Fatalf("error type = %q, want invalid_request_error", provErr.Type)
	}
}

func TestChatCompletionReturnsUpstreamErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`model not found`))
	}))
	defer server.Close()

	p := cloudflare.NewProvider("test-token", server.URL+"/accounts/ACC123", 10*time.Second)
	_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "@hf/meta-llama/llama-3-8b-instruct",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	provErr, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if provErr.Type != provider.ErrorTypeServerError {
		t.Fatalf("error type = %q, want server_error", provErr.Type)
	}
	if !strings.Contains(provErr.Message, "model not found") {
		t.Fatalf("message = %q, want it to contain upstream body", provErr.Message)
	}
}

func TestChatCompletionReportsWorkersAIFailure(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantMsg string
	}{
		{
			name:    "first error message",
			payload: `{"result":null,"success":false,"errors":[{"message":"7301: model is disabled"}]}`,
			wantMsg: "7301: model is disabled",
		},
		{
			name:    "no error details",
			payload: `{"result":null,"success":false,"errors":[]}`,
			wantMsg: "workers ai request failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.payload))
			}))
			defer server.Close()

			p := cloudflare.NewProvider("test-token", server.URL+"/accounts/ACC123", 10*time.Second)
			_, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
				Model:    "@hf/meta-llama/llama-3-8b-instruct",
				Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
			})
			if err == nil {
				t.Fatal("expected error")
			}
			provErr, ok := err.(*provider.ProviderError)
			if !ok {
				t.Fatalf("expected *provider.ProviderError, got %T", err)
			}
			if provErr.Type != provider.ErrorTypeServerError {
				t.Fatalf("error type = %q, want server_error", provErr.Type)
			}
			if provErr.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status code = %d, want 500", provErr.StatusCode)
			}
			if provErr.Message != tt.wantMsg {
				t.Fatalf("message = %q, want %q", provErr.Message, tt.wantMsg)
			}
		})
	}
}

func TestChatCompletionDefaultsToFallbackModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/ai/run/@hf/meta-llama/llama-3-8b-instruct") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"response": "ok"}, "success": true})
	}))
	defer server.Close()

	p := cloudflare.NewProvider("test-token", server.URL+"/accounts/ACC123", 10*time.Second)
	resp, err := p.ChatCompletion(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("len(choices) = %d, want 1", len(resp.Choices))
	}
}

func TestChatCompletionStreamNotSupported(t *testing.T) {
	p := cloudflare.NewProvider("test-token", "https://api.cloudflare.com/client/v4/accounts/ACC123", 10*time.Second)
	_, err := p.ChatCompletionStream(context.Background(), &apitypes.ChatCompletionRequest{
		Model:    "@hf/meta-llama/llama-3-8b-instruct",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	provErr, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if provErr.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status code = %d, want 501", provErr.StatusCode)
	}
}

func TestEmbeddingsNotSupported(t *testing.T) {
	p := cloudflare.NewProvider("test-token", "https://api.cloudflare.com/client/v4/accounts/ACC123", 10*time.Second)
	_, err := p.Embeddings(context.Background(), &apitypes.EmbeddingRequest{Model: "@cf/baai/bge-base-en-v1.5", Input: "hi"})
	if err == nil {
		t.Fatal("expected error")
	}
	provErr, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("expected *provider.ProviderError, got %T", err)
	}
	if provErr.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status code = %d, want 501", provErr.StatusCode)
	}
}

func TestListModelsPrefixesCloudflareIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/ai/models") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if want := "/accounts/ACC123/ai/models"; !strings.HasSuffix(r.URL.Path, want) {
			t.Fatalf("account ID missing from models path: path = %s, want suffix %s", r.URL.Path, want)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want Bearer test-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"id": "meta-llama/llama-3-8b-instruct", "name": "Llama 3 8B Instruct"},
				{"id": "nousresearch/hermes-3-llama-3.1-405b", "name": "Hermes 3 405B"},
			},
			"success": true,
		})
	}))
	defer server.Close()

	p := cloudflare.NewProvider("test-token", server.URL+"/accounts/ACC123", 10*time.Second)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}
	for i, m := range models {
		if m.OwnedBy != "cloudflare" {
			t.Fatalf("models[%d].OwnedBy = %q, want cloudflare", i, m.OwnedBy)
		}
	}
	if models[0].ProviderModelID != "@meta-llama/llama-3-8b-instruct" ||
		models[0].ModelID != "@meta-llama/llama-3-8b-instruct" {
		t.Fatalf("models[0] = %+v, want @-prefixed ID", models[0])
	}
	if models[1].ProviderModelID != "@nousresearch/hermes-3-llama-3.1-405b" {
		t.Fatalf("models[1].ProviderModelID = %q", models[1].ProviderModelID)
	}
}

func TestGetPricingReturnsKnownModels(t *testing.T) {
	tests := []struct {
		model       string
		inputPrice  float64
		outputPrice float64
	}{
		{"@hf/meta-llama/llama-3-8b-instruct", 0.000011, 0.000011},
		{"@hf/nousresearch/hermes-3-llama-3.1-405b", 0.000022, 0.000022},
	}

	p := cloudflare.NewProvider("test-token", "", 10*time.Second)
	prices, err := p.GetPricing(context.Background())
	if err != nil {
		t.Fatalf("GetPricing: %v", err)
	}
	if len(prices) != len(tests) {
		t.Fatalf("len(prices) = %d, want %d", len(prices), len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, ok := prices[tt.model]
			if !ok {
				t.Fatalf("missing pricing for %q in %v", tt.model, prices)
			}
			if got.UnitType != provider.UnitToken || got.UnitSize != 1000 {
				t.Fatalf("unit = %+v, want token/1000", got)
			}
			if got.InputPrice != tt.inputPrice || got.OutputPrice != tt.outputPrice {
				t.Fatalf("prices = %v/%v, want %v/%v", got.InputPrice, got.OutputPrice, tt.inputPrice, tt.outputPrice)
			}
			if got.Currency != "USD" {
				t.Fatalf("currency = %q, want USD", got.Currency)
			}
		})
	}
}

func TestHealthCheckReportsHealthFromModelsEndpoint(t *testing.T) {
	tests := []struct {
		status  int
		healthy bool
	}{
		{http.StatusOK, true},
		{http.StatusUnauthorized, false},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/ai/models") {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			p := cloudflare.NewProvider("test-token", server.URL+"/accounts/ACC123", 10*time.Second)
			status, err := p.HealthCheck(context.Background())
			if err != nil {
				t.Fatalf("HealthCheck: %v", err)
			}
			if status.Provider != "cloudflare" {
				t.Fatalf("provider = %q, want cloudflare", status.Provider)
			}
			if status.IsHealthy != tt.healthy {
				t.Fatalf("IsHealthy = %v, want %v", status.IsHealthy, tt.healthy)
			}
		})
	}
}

func TestSupportsModelRequiresNonEmptyID(t *testing.T) {
	tests := []struct {
		modelID string
		want    bool
	}{
		{"@hf/meta-llama/llama-3-8b-instruct", true},
		{"x", true},
		{"", false},
	}

	p := cloudflare.NewProvider("test-token", "", 10*time.Second)
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			if got := p.SupportsModel(tt.modelID); got != tt.want {
				t.Fatalf("SupportsModel(%q) = %v, want %v", tt.modelID, got, tt.want)
			}
		})
	}
}
