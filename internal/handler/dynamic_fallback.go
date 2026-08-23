package handler

import (
	"context"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/router"
)

// defaultDynamicFallbackCandidates bounds the dynamic tail when the config
// value is unset or non-positive.
const defaultDynamicFallbackCandidates = 3

// dynamicFallbackCacheTTL is how long a ranked candidate list for one request
// category may be reused. Building the list requires listing every provider's
// model catalog (upstream HTTP), so it must not happen per request. Eligibility
// within a category does not change faster than this in practice, and breaker
// gating plus execution-time health checks stay live regardless.
const dynamicFallbackCacheTTL = 60 * time.Second

// rankedCacheEntry is the cached ranked candidate list for one request
// category (vision × mode).
type rankedCacheEntry struct {
	entries []router.CandidateScore
	expires time.Time
}

// dynamicFallbackRoutes appends capability-matched alternates after the
// primary route and any configured static fallbacks. Candidates come from
// the catalog-backed auto resolver, which applies the request's mode hard
// filters (vision, tools, planning/agentic requirements, context capacity)
// before ranking by health/latency/cost/capability — so failover stays
// within the semantic category of the request and a model only goes down
// when no eligible model can serve it.
//
// When disabled via config, or when the resolver/catalog machinery is not
// wired, it returns nil and legacy behaviour is unchanged.
func (h *Handler) dynamicFallbackRoutes(ctx context.Context, req *apitypes.ChatCompletionRequest, primary *router.ResolvedRoute, existing []router.ResolvedRoute) []router.ResolvedRoute {
	if h.cfg == nil || !h.cfg.Routing.DynamicFallback.Enabled || h.autoResolver == nil {
		return nil
	}
	maxCandidates := h.cfg.Routing.DynamicFallback.MaxCandidates
	if maxCandidates <= 0 {
		maxCandidates = defaultDynamicFallbackCandidates
	}

	scored := h.rankedAlternates(ctx, req)
	if len(scored) == 0 {
		return nil
	}

	type candidateKey struct{ provider, model string }
	taken := make(map[candidateKey]bool, len(existing)+1)
	taken[candidateKey{primary.ProviderName, primary.ProviderModelID}] = true
	for _, fb := range existing {
		taken[candidateKey{fb.ProviderName, fb.ProviderModelID}] = true
	}

	breakers := h.router.BreakerPool()
	appended := make([]router.ResolvedRoute, 0, maxCandidates)
	for _, cs := range scored {
		if len(appended) >= maxCandidates {
			break
		}
		if cs.Rejected || taken[candidateKey{cs.Provider, cs.ProviderID}] {
			continue
		}
		p, found := h.registry.Get(cs.Provider)
		if !found {
			continue
		}
		var b *breaker.Breaker
		if breakers != nil {
			b = breakers.Get(cs.Provider)
			if b != nil && b.State() == breaker.StateOpen {
				continue
			}
		}
		taken[candidateKey{cs.Provider, cs.ProviderID}] = true
		appended = append(appended, router.ResolvedRoute{
			Provider:        p,
			ProviderName:    cs.Provider,
			ProviderModelID: cs.ProviderID,
			ModelID:         primary.ModelID,
			Breaker:         b,
		})
	}
	if len(appended) == 0 {
		return nil
	}
	return appended
}

// rankedAlternates returns eligible candidates ranked by descending score for
// the request's category. Results are memoized per (vision, mode) key because
// eligibility depends only on those request traits; long-horizon and agentic
// modes bypass the cache since their context-capacity filter depends on the
// exact token estimate of each request.
func (h *Handler) rankedAlternates(ctx context.Context, req *apitypes.ChatCompletionRequest) []router.CandidateScore {
	cacheKey, cacheable := dynamicFallbackCacheKey(req)
	if cacheable {
		h.rankedMu.Lock()
		if entry, ok := h.rankedCache[cacheKey]; ok && time.Now().Before(entry.expires) {
			entries := entry.entries
			h.rankedMu.Unlock()
			return entries
		}
		h.rankedMu.Unlock()
	}

	scored, err := h.autoResolver.RankedEligible(ctx, req)
	if err != nil {
		scored = nil
	}

	if cacheable {
		h.rankedMu.Lock()
		if h.rankedCache == nil {
			h.rankedCache = make(map[string]rankedCacheEntry)
		}
		h.rankedCache[cacheKey] = rankedCacheEntry{
			entries: scored,
			expires: time.Now().Add(dynamicFallbackCacheTTL),
		}
		h.rankedMu.Unlock()
	}
	return scored
}

// dynamicFallbackCacheKey derives the memoization key for a request. The bool
// result reports whether the request is cacheable at all.
func dynamicFallbackCacheKey(req *apitypes.ChatCompletionRequest) (string, bool) {
	mode := router.ModeDefault
	if req.Mode != "" {
		parsed, err := router.ParseMode(req.Mode)
		if err != nil {
			parsed = router.ModeDefault
		}
		mode = parsed
	}
	// Context-capacity filters depend on the exact per-request token estimate.
	if mode == router.ModeLongHorizon || mode == router.ModeAgentic {
		return "", false
	}

	hasImage := false
	for _, m := range req.Messages {
		if m.HasContentParts() {
			for _, part := range m.Content.([]apitypes.ContentPart) {
				if part.Type == apitypes.ContentPartImageURL && part.ImageURL != nil && part.ImageURL.URL != "" {
					hasImage = true
				}
			}
		}
	}
	var b strings.Builder
	b.WriteString(string(mode))
	b.WriteByte('|')
	b.WriteString("vision=")
	if hasImage {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	return b.String(), true
}
