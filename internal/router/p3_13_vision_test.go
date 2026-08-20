package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 7: Vision semantics.
//
// mode=vision is a PROFILE, not a content declaration. The hard filter keys
// off actual image content (capHint.Vision), which is mode-independent:
//   - mode=vision without image  -> vision profile applies, no hard rejection
//   - actual image content       -> non-vision candidates hard-rejected in
//     EVERY mode (the hard filter is not vision-mode-specific)
//   - mode=vision + image        -> vision hard requirement
//   - explicit provider route    -> candidate set constrained, no expansion

// runMode executes a request under an explicit mode and returns the result.
func runMode(t *testing.T, pipeline *router.DecisionPipeline, mode string, req *apitypes.ChatCompletionRequest) *router.SelectionResult {
	t.Helper()
	c := cloneReq(req)
	c.Mode = mode
	res, err := pipeline.Execute(context.Background(), c, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("%s Execute: %v", mode, err)
	}
	return res
}

func scoreOf(t *testing.T, res *router.SelectionResult, name string) router.CandidateScore {
	t.Helper()
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == name {
			return cs
		}
	}
	t.Fatalf("no candidate %q", name)
	return router.CandidateScore{}
}

// TestP313VisionModeWithoutImageNoHardReject: mode=vision with text-only
// content applies the vision weight profile but rejects nothing; a non-vision
// provider can win on health.
func TestP313VisionModeWithoutImageNoHardReject(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateDegraded, 100)

	res := runMode(t, pipeline, "vision", execReq("vision", "describe this"))
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("vision-without-image: expected aaa (healthy, no hard filter), got %q", res.Decision.SelectedProvider)
	}
	if cs := scoreOf(t, res, "aaa"); cs.Rejected {
		t.Fatal("non-vision candidate must not be rejected without actual image content")
	}
}

// TestP313VisionTextMentionNoHardReject: text that merely MENTIONS an image
// ("describe this image") carries no image content part — no hard rejection.
func TestP313VisionTextMentionNoHardReject(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)

	req := execReq("vision", "describe this image in detail")
	res := runMode(t, pipeline, "vision", req)
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("vision text-mention: expected aaa eligible, got %q", res.Decision.SelectedProvider)
	}
	if cs := scoreOf(t, res, "aaa"); cs.Rejected {
		t.Fatal("text mention of image must not hard-reject")
	}
}

// TestP313VisionImageContentHardRejectsInEveryMode: actual image content
// rejects non-vision candidates under EVERY mode, including fast.
func TestP313VisionImageContentHardRejectsInEveryMode(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: false, reasoning: true, toolCalling: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, latencyMs: 1000, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 20)
	setHealth(t, store, "zzz", runtime.StateDegraded, 1000)

	for _, mode := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		res := runMode(t, pipeline, mode, imageReq(mode))
		if res.Decision.SelectedProvider != "zzz" {
			t.Fatalf("mode=%s with image: expected zzz (vision), got %q", mode, res.Decision.SelectedProvider)
		}
		if cs := scoreOf(t, res, "aaa"); !cs.Rejected {
			t.Fatalf("mode=%s: non-vision candidate must be hard-rejected with image content", mode)
		}
	}
}

// TestP313VisionExplicitRouteRejectedNoExpansion: an explicit route to a
// non-vision provider with image content is rejected and the candidate set is
// NOT expanded to other providers.
func TestP313VisionExplicitRouteRejectedNoExpansion(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: false, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 20)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	stub := &calibStubProvider{name: "aaa", supportsAll: true, vision: false, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000}
	explicit := []router.ResolvedRoute{{Provider: stub, ProviderName: "aaa", ProviderModelID: "m", ModelID: "m"}}

	req := imageReq("vision")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, explicit)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "" {
		t.Fatalf("explicit non-vision route with image: expected no selection, got %q", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "zzz" {
			t.Fatal("candidate set must NOT expand to the vision-capable provider")
		}
	}
}

// TestP313VisionExplicitRouteEligible: the same explicit route WITHOUT image
// content is eligible — mode=vision does not veto a non-vision provider by
// itself.
func TestP313VisionExplicitRouteEligible(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: false, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 20)

	stub := &calibStubProvider{name: "aaa", supportsAll: true, vision: false, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000}
	explicit := []router.ResolvedRoute{{Provider: stub, ProviderName: "aaa", ProviderModelID: "m", ModelID: "m"}}

	req := execReq("vision", "describe this")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, explicit)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("explicit non-vision route without image: expected aaa, got %q", res.Decision.SelectedProvider)
	}
}

// TestP313VisionUnknownCapability: a provider with NO vision metadata (name
// not in DefaultCapabilities, no model override, no "vision"/"vl" model name)
// is treated as not-vision-capable: rejected with image content, eligible
// without it. Unknown is NOT a separate eligible state — documented semantic.
func TestP313VisionUnknownCapability(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: false, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 0},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 20)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// aaa has no metadata and no model-level override: capabilities all false.
	res := runMode(t, pipeline, "vision", imageReq("vision"))
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("unknown-vision with image: expected zzz, got %q", res.Decision.SelectedProvider)
	}
	if cs := scoreOf(t, res, "aaa"); !cs.Rejected {
		t.Fatal("unknown vision capability must be treated as not-vision-capable with image content")
	}

	res = runMode(t, pipeline, "vision", execReq("vision", "describe this"))
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("unknown-vision without image: expected aaa eligible, got %q", res.Decision.SelectedProvider)
	}
}
