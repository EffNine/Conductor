package handler

import (
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/cache"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

func testCacheHandler(t *testing.T) *Handler {
	t.Helper()
	h := &Handler{logger: zap.NewNop()}
	cfg := config.CacheConfig{}
	h.SetCacheEngine(cache.NewEngine(cfg, nil, zap.NewNop()))
	return h
}

// TestP311CacheKeyModeIsolation verifies the handler cache key includes the
// mode field, so a cached response produced under one mode can never be
// served under a different mode's routing semantics.
func TestP311CacheKeyModeIsolation(t *testing.T) {
	h := testCacheHandler(t)
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	resolved := &router.ResolvedRoute{ProviderName: "p", ProviderModelID: "m"}

	fast := *req
	fast.Mode = "fast"
	reasoning := *req
	reasoning.Mode = "reasoning"
	none := *req

	keyFast := h.buildCacheKey(&fast, resolved)
	keyReasoning := h.buildCacheKey(&reasoning, resolved)
	keyNone := h.buildCacheKey(&none, resolved)

	if keyFast == "" || keyReasoning == "" || keyNone == "" {
		t.Fatal("cache key must not be empty when cache engine is set")
	}
	if keyFast == keyReasoning {
		t.Fatal("cache keys must differ for different modes")
	}
	if keyFast == keyNone {
		t.Fatal("cache keys must differ when mode is set vs omitted")
	}
}

// TestP311CacheKeyOmittedVsExplicitAuto verifies omitted mode and explicit
// "auto" produce distinct keys (they are distinct routing inputs at the API
// boundary even though they resolve to the same default profile).
func TestP311CacheKeyOmittedVsExplicitAuto(t *testing.T) {
	h := testCacheHandler(t)
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	resolved := &router.ResolvedRoute{ProviderName: "p", ProviderModelID: "m"}

	auto := *req
	auto.Mode = "auto"
	if h.buildCacheKey(req, resolved) == h.buildCacheKey(&auto, resolved) {
		t.Fatal("omitted mode and explicit 'auto' must produce different cache keys")
	}
}

// TestP311CacheKeyResolvedModelID verifies the resolved provider model ID
// participates in the key: an aliased model resolving to the same provider
// model shares a key, while a different provider model does not.
func TestP311CacheKeyResolvedModelID(t *testing.T) {
	h := testCacheHandler(t)
	req := &apitypes.ChatCompletionRequest{
		Model:    "alias",
		Mode:     "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}

	keyA := h.buildCacheKey(req, &router.ResolvedRoute{ProviderName: "p1", ProviderModelID: "gpt-4o"})
	keyB := h.buildCacheKey(req, &router.ResolvedRoute{ProviderName: "p1", ProviderModelID: "gpt-4o-mini"})

	if keyA == keyB {
		t.Fatal("cache keys must differ when the resolved provider model differs")
	}
	// Same resolved model via different alias spellings of the request must
	// share a key (request-level model alias is not part of the key).
	req2 := *req
	req2.Model = "different-alias"
	if h.buildCacheKey(&req2, &router.ResolvedRoute{ProviderName: "p1", ProviderModelID: "gpt-4o"}) != keyA {
		t.Fatal("cache key must depend on the resolved provider model, not the request alias")
	}
}

// TestP311CacheKeyCapabilityFields verifies capability-affecting request
// fields (tools, reasoning, response_format) participate in the key so
// routing-relevant requests never share a cache entry.
func TestP311CacheKeyCapabilityFields(t *testing.T) {
	h := testCacheHandler(t)
	base := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	resolved := &router.ResolvedRoute{ProviderName: "p", ProviderModelID: "m"}
	keyBase := h.buildCacheKey(base, resolved)

	withTools := *base
	withTools.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "t"}}}
	if h.buildCacheKey(&withTools, resolved) == keyBase {
		t.Fatal("cache keys must differ when tools are added")
	}

	withReasoning := *base
	withReasoning.ReasoningEffort = "high"
	if h.buildCacheKey(&withReasoning, resolved) == keyBase {
		t.Fatal("cache keys must differ when reasoning params are added")
	}

	withFormat := *base
	withFormat.ResponseFormat = map[string]interface{}{"type": "json_object"}
	if h.buildCacheKey(&withFormat, resolved) == keyBase {
		t.Fatal("cache keys must differ when response_format is added")
	}

	withImage := *base
	withImage.Messages = []apitypes.Message{{Role: "user", Content: []apitypes.ContentPart{
		{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "data:image/png;base64,AA"}},
	}}}
	if h.buildCacheKey(&withImage, resolved) == keyBase {
		t.Fatal("cache keys must differ when image content is added")
	}
}
