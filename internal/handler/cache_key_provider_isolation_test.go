package handler

import (
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
)

// TestCacheKeyProviderIdentityIsolation locks the P3.12 cache identity
// contract: two requests that resolve to the SAME provider model slug on
// DIFFERENT providers must never share a cache entry, because the provider
// can legally return a different response for the same model (and different
// providers imply different availability/failure behavior).
func TestCacheKeyProviderIdentityIsolation(t *testing.T) {
	h := testCacheHandler(t)
	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Mode:     "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}

	keyOpenAI := h.buildCacheKey(req, &router.ResolvedRoute{ProviderName: "openai", ProviderModelID: "gpt-4o"})
	keyAzure := h.buildCacheKey(req, &router.ResolvedRoute{ProviderName: "azure", ProviderModelID: "gpt-4o"})

	if keyOpenAI == "" || keyAzure == "" {
		t.Fatal("cache key must not be empty when cache engine is set")
	}
	if keyOpenAI == keyAzure {
		t.Fatal("cache keys must differ when the same model slug resolves to different providers")
	}
}

// TestCacheKeyProviderPrefixRoutesIsolate verifies the real-world
// collision vector: provider-prefixed model IDs ("openai/gpt-4o" vs
// "azure/gpt-4o") must produce distinct keys when the prefix routes to
// different providers.
func TestCacheKeyProviderPrefixRoutesIsolate(t *testing.T) {
	h := testCacheHandler(t)
	base := &apitypes.ChatCompletionRequest{
		Model:    "openai/gpt-4o",
		Mode:     "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	other := *base
	other.Model = "azure/gpt-4o"

	keyOpenAI := h.buildCacheKey(base, &router.ResolvedRoute{ProviderName: "openai", ProviderModelID: "gpt-4o"})
	keyAzure := h.buildCacheKey(&other, &router.ResolvedRoute{ProviderName: "azure", ProviderModelID: "gpt-4o"})

	if keyOpenAI == keyAzure {
		t.Fatal("provider-prefixed model IDs resolving to different providers must not share a cache key")
	}
}

// TestCacheKeyFallbackRouteChangesProvider verifies that when a primary
// route falls back to a second provider for the same resolved model, the
// cache entries stay isolated.
func TestCacheKeyFallbackRouteChangesProvider(t *testing.T) {
	h := testCacheHandler(t)
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}

	keyPrimary := h.buildCacheKey(req, &router.ResolvedRoute{ProviderName: "primary", ProviderModelID: "m"})
	keyFallback := h.buildCacheKey(req, &router.ResolvedRoute{ProviderName: "fallback", ProviderModelID: "m"})

	if keyPrimary == keyFallback {
		t.Fatal("primary and fallback routes to different providers must not share a cache key")
	}
}

// TestCacheKeyAliasToDifferentProvidersIsolate verifies two aliases that
// resolve to the SAME provider+model share a key, while aliases resolving to
// DIFFERENT providers do not.
func TestCacheKeyAliasToDifferentProvidersIsolate(t *testing.T) {
	h := testCacheHandler(t)
	aliasA := &apitypes.ChatCompletionRequest{
		Model:    "alias-a",
		Mode:     "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	aliasB := *aliasA
	aliasB.Model = "alias-b"

	keySame := h.buildCacheKey(aliasA, &router.ResolvedRoute{ProviderName: "p1", ProviderModelID: "gpt-4o"})
	keySameAliasB := h.buildCacheKey(&aliasB, &router.ResolvedRoute{ProviderName: "p1", ProviderModelID: "gpt-4o"})
	if keySame != keySameAliasB {
		t.Fatal("aliases resolving to the same provider+model must share a cache key")
	}

	keyOtherProvider := h.buildCacheKey(aliasA, &router.ResolvedRoute{ProviderName: "p2", ProviderModelID: "gpt-4o"})
	if keySame == keyOtherProvider {
		t.Fatal("aliases resolving to different providers must not share a cache key")
	}
}

// TestCacheKeyCanonicalModeNormalization verifies the mode dimension of
// the key uses the CANONICAL mode: equivalent spellings share a key, while
// omitted mode stays distinct from explicit "auto" (locked P3.11 rule).
func TestCacheKeyCanonicalModeNormalization(t *testing.T) {
	h := testCacheHandler(t)
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	resolved := &router.ResolvedRoute{ProviderName: "p", ProviderModelID: "m"}
	keyAuto := h.buildCacheKey(req, resolved)

	upper := *req
	upper.Mode = "AUTO"
	if h.buildCacheKey(&upper, resolved) != keyAuto {
		t.Fatal("'AUTO' must canonicalize to the same cache key as 'auto'")
	}

	padded := *req
	padded.Mode = " auto "
	if h.buildCacheKey(&padded, resolved) != keyAuto {
		t.Fatal("' auto ' must canonicalize to the same cache key as 'auto'")
	}

	omitted := *req
	omitted.Mode = ""
	if h.buildCacheKey(&omitted, resolved) == keyAuto {
		t.Fatal("omitted mode must stay distinct from explicit 'auto' in the cache key")
	}
}

// TestCacheKeyModeAndProviderOrthogonal verifies mode and provider
// dimensions are independent: changing either one changes the key, and
// identical (mode, provider) pairs agree.
func TestCacheKeyModeAndProviderOrthogonal(t *testing.T) {
	h := testCacheHandler(t)
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}

	base := h.buildCacheKey(req, &router.ResolvedRoute{ProviderName: "p1", ProviderModelID: "m"})
	modeFast := *req
	modeFast.Mode = "fast"
	keyModeFast := h.buildCacheKey(&modeFast, &router.ResolvedRoute{ProviderName: "p1", ProviderModelID: "m"})
	keyOtherProvider := h.buildCacheKey(req, &router.ResolvedRoute{ProviderName: "p2", ProviderModelID: "m"})

	if base == keyModeFast {
		t.Fatal("different modes on the same provider must not share a cache key")
	}
	if base == keyOtherProvider {
		t.Fatal("same mode on different providers must not share a cache key")
	}
}
