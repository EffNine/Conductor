package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// TestP311AddingWorseCandidateCannotChangeWinner verifies the selection
// invariant that adding a worse candidate never changes the winner.
func TestP311AddingWorseCandidateCannotChangeWinner(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "best", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001},
		&calibStubProvider{name: "worse", supportsAll: true, latencyMs: 4900, healthState: runtime.StateDegraded, costPerUnit: 0.0009},
	)
	setHealth(t, store, "best", runtime.StateHealthy, 100)
	setHealth(t, store, "worse", runtime.StateDegraded, 4900)

	req := execReq("auto", "hi")

	resAlone, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, []router.ResolvedRoute{
		{ProviderName: "best", ProviderModelID: "m", ModelID: "m"},
	})
	if err != nil {
		t.Fatalf("Execute (alone): %v", err)
	}
	if resAlone.Decision.SelectedProvider != "best" {
		t.Fatalf("expected best alone, got %s", resAlone.Decision.SelectedProvider)
	}

	resBoth, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, []router.ResolvedRoute{
		{ProviderName: "best", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "worse", ProviderModelID: "m", ModelID: "m"},
	})
	if err != nil {
		t.Fatalf("Execute (both): %v", err)
	}
	if resBoth.Decision.SelectedProvider != "best" {
		t.Fatalf("adding a worse candidate changed the winner: got %s", resBoth.Decision.SelectedProvider)
	}
}

// TestP311RemovingWinnerSelectsBestRemaining verifies that when the winner is
// removed from the explicit candidate set, the best remaining candidate is
// selected.
func TestP311RemovingWinnerSelectsBestRemaining(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "a", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "b", supportsAll: true, latencyMs: 200, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "c", supportsAll: true, latencyMs: 300, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "a", runtime.StateHealthy, 100)
	setHealth(t, store, "b", runtime.StateHealthy, 200)
	setHealth(t, store, "c", runtime.StateHealthy, 300)

	req := execReq("auto", "hi")
	all := []router.ResolvedRoute{
		{ProviderName: "a", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "b", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "c", ProviderModelID: "m", ModelID: "m"},
	}

	resAll, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, all)
	if err != nil {
		t.Fatalf("Execute (all): %v", err)
	}
	if resAll.Decision.SelectedProvider != "a" {
		t.Fatalf("expected a, got %s", resAll.Decision.SelectedProvider)
	}

	resRest, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, all[1:])
	if err != nil {
		t.Fatalf("Execute (rest): %v", err)
	}
	if resRest.Decision.SelectedProvider != "b" {
		t.Fatalf("expected b (best remaining), got %s", resRest.Decision.SelectedProvider)
	}
}

// TestP311ReorderingExplicitCandidates verifies explicit candidate order only
// affects the outcome when scores are exactly tied.
func TestP311ReorderingExplicitCandidates(t *testing.T) {
	_, store, eng := setupCalibPipeline(t,
		&calibStubProvider{name: "fast", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "slow", supportsAll: true, latencyMs: 300, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "fast", runtime.StateHealthy, 100)
	setHealth(t, store, "slow", runtime.StateHealthy, 300)

	req := execReq("auto", "hi")
	fastR := router.ResolvedRoute{ProviderName: "fast", ProviderModelID: "m", ModelID: "m"}
	slowR := router.ResolvedRoute{ProviderName: "slow", ProviderModelID: "m", ModelID: "m"}

	// Distinct scores: order must not matter.
	res1, err := eng.SelectFromRoutesWithSnapshot(context.Background(), []router.ResolvedRoute{fastR, slowR}, req, store.Snapshot(context.Background()))
	if err != nil {
		t.Fatalf("select 1: %v", err)
	}
	res2, err := eng.SelectFromRoutesWithSnapshot(context.Background(), []router.ResolvedRoute{slowR, fastR}, req, store.Snapshot(context.Background()))
	if err != nil {
		t.Fatalf("select 2: %v", err)
	}
	if res1.Decision.SelectedProvider != "fast" || res2.Decision.SelectedProvider != "fast" {
		t.Fatalf("reordering must not change winner with distinct scores: got %s and %s",
			res1.Decision.SelectedProvider, res2.Decision.SelectedProvider)
	}

	// Equal scores (identical candidates, different names): first wins.
	dupA := router.ResolvedRoute{ProviderName: "dupA", ProviderModelID: "m", ModelID: "m"}
	dupB := router.ResolvedRoute{ProviderName: "dupB", ProviderModelID: "m", ModelID: "m"}
	// Register identical duplicates so both are scoreable.
	dup := &calibStubProvider{name: "dup", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy}
	_ = store.Register(runtime.NewProviderRuntime("dupA", dup))
	_ = store.Register(runtime.NewProviderRuntime("dupB", dup))
	setHealth(t, store, "dupA", runtime.StateHealthy, 100)
	setHealth(t, store, "dupB", runtime.StateHealthy, 100)

	res3, err := eng.SelectFromRoutesWithSnapshot(context.Background(), []router.ResolvedRoute{dupA, dupB}, req, store.Snapshot(context.Background()))
	if err != nil {
		t.Fatalf("select 3: %v", err)
	}
	res4, err := eng.SelectFromRoutesWithSnapshot(context.Background(), []router.ResolvedRoute{dupB, dupA}, req, store.Snapshot(context.Background()))
	if err != nil {
		t.Fatalf("select 4: %v", err)
	}
	if res3.Decision.SelectedProvider != "dupA" {
		t.Fatalf("expected first candidate dupA on tie, got %s", res3.Decision.SelectedProvider)
	}
	if res4.Decision.SelectedProvider != "dupB" {
		t.Fatalf("expected first candidate dupB on tie, got %s", res4.Decision.SelectedProvider)
	}
}

// TestP311ModeProfileMutationIsolation verifies mode profiles are copied per
// decision: mutating one decision's profile must not affect another.
func TestP311ModeProfileMutationIsolation(t *testing.T) {
	profiles := router.DefaultModeProfiles()
	fast := profiles[router.ModeFast]
	coding := profiles[router.ModeCoding]
	if fast == coding {
		t.Fatal("expected distinct profile pointers")
	}

	// Mutate the fast profile.
	before := fast.WeightPreferences.Latency
	fast.WeightPreferences.Latency = 99

	// The default profiles map must not be affected by mutation of the returned
	// pointers only if copies were made; DefaultModeProfiles constructs fresh
	// profiles each call, so a fresh fetch must show the original values.
	fresh := router.DefaultModeProfiles()[router.ModeFast]
	if fresh.WeightPreferences.Latency != before {
		t.Fatalf("DefaultModeProfiles returned a shared profile: got %v, want %v",
			fresh.WeightPreferences.Latency, before)
	}
}

// TestP311HardRejectedNeverSelected verifies the invariant that a
// hard-rejected candidate can never be selected, even when it is the only
// candidate.
func TestP311HardRejectedNeverSelected(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "no-vision", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "no-vision", runtime.StateHealthy, 50)

	req := execReq("auto", "hi")
	req.Messages = []apitypes.Message{{Role: "user", Content: []apitypes.ContentPart{
		{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "data:image/png;base64,AA"}},
	}}}
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection (only candidate hard-rejected), got %s", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Selected && cs.Rejected {
			t.Fatal("rejected candidate must never be marked selected")
		}
	}
}
