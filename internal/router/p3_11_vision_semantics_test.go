package router_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// TestP311VisionModeWithoutImageNoHardReject verifies mode=vision with NO
// actual image content does not trigger the vision hard filter: the
// text-only provider must stay eligible (Rejected=false) and a selection
// must exist (vision mode is a weight preference, not a filter).
func TestP311VisionModeWithoutImageNoHardReject(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "vision-p", supportsAll: true, vision: true, latencyMs: 150, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "text-p", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "vision-p", runtime.StateHealthy, 150)
	setHealth(t, store, "text-p", runtime.StateHealthy, 50)

	req := execReq("vision", "explain computer vision")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider == "" {
		t.Fatal("expected a selection (no hard reject without image)")
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Rejected {
			t.Errorf("provider %s must not be hard-rejected (no image in request): %s", cs.Provider, cs.RejectionReason)
		}
	}
}

// TestP311TextMentionOfImageNotMultimodal verifies that merely TALKING about
// an image in text does not set the vision hint (hint requires an actual
// ContentPartImageURL), so the text-only provider stays eligible.
func TestP311TextMentionOfImageNotMultimodal(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "vision-p", supportsAll: true, vision: true, latencyMs: 150, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "text-p", supportsAll: true, vision: false, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "vision-p", runtime.StateHealthy, 150)
	setHealth(t, store, "text-p", runtime.StateHealthy, 50)

	req := execReq("auto", "describe this image: [img src=data:image/png;base64,AAAA]")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider == "" {
		t.Fatal("expected a selection")
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Rejected {
			t.Errorf("provider %s must not be hard-rejected (text-only mention): %s", cs.Provider, cs.RejectionReason)
		}
	}
}

// TestP312UppercaseModeNormalized verifies the public mode API normalizes
// case: uppercase and mixed-case variants resolve to the canonical mode
// instead of being rejected (P3.12 normalization contract).
func TestP312UppercaseModeNormalized(t *testing.T) {
	for _, m := range []string{"AUTO", "Vision", "FAST", "Long_Horizon"} {
		got, err := router.ParseMode(m)
		if err != nil {
			t.Errorf("ParseMode(%q) unexpected error: %v", m, err)
			continue
		}
		want, _ := router.ParseMode(strings.ToLower(m))
		if got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", m, got, want)
		}
	}
}

// TestP312WhitespaceModeNormalized verifies whitespace-padded mode strings are
// trimmed to the canonical mode (P3.12 normalization contract).
func TestP312WhitespaceModeNormalized(t *testing.T) {
	for _, m := range []string{" auto", "auto ", " planning", "fast\n"} {
		got, err := router.ParseMode(m)
		if err != nil {
			t.Errorf("ParseMode(%q) unexpected error: %v", m, err)
			continue
		}
		want, _ := router.ParseMode(strings.TrimSpace(m))
		if got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", m, got, want)
		}
	}
}

// TestP312PipelineAcceptsNormalizedMode verifies the pipeline accepts
// non-canonical spellings and routes with the canonical mode.
func TestP312PipelineAcceptsNormalizedMode(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "p", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "p", runtime.StateHealthy, 100)
	req := execReq("AUTO", "hi")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("expected no error for uppercase mode, got: %v", err)
	}
	if res.Decision.SelectedProvider != "p" {
		t.Fatalf("expected selection, got %s", res.Decision.SelectedProvider)
	}
}

// TestP311ExactPublicModeStringsAccepted verifies every documented public mode
// string parses to the canonical Mode and no more.
func TestP311ExactPublicModeStringsAccepted(t *testing.T) {
	want := []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"}
	got := make(map[router.Mode]bool)
	for _, s := range want {
		m, err := router.ParseMode(s)
		if err != nil {
			t.Fatalf("ParseMode(%q): %v", s, err)
		}
		got[m] = true
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d distinct modes, got %d", len(want), len(got))
	}
}
