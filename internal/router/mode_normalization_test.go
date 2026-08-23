package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// TestNormalizeModeContract locks the canonical normalization function:
// lowercase + trim; empty input stays empty (distinct from "auto").
func TestNormalizeModeContract(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"auto", "auto"},
		{"AUTO", "auto"},
		{"Auto", "auto"},
		{"Vision", "vision"},
		{" planning", "planning"},
		{"planning ", "planning"},
		{"  PLANNING  ", "planning"},
		{"fast\n", "fast"},
		{"\tlong_horizon\t", "long_horizon"},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := router.NormalizeMode(tc.in); got != tc.want {
			t.Errorf("NormalizeMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseModeAcceptsNormalizedSpellings verifies ParseMode accepts
// non-canonical spellings and maps them to the canonical Mode.
func TestParseModeAcceptsNormalizedSpellings(t *testing.T) {
	cases := []struct {
		in   string
		want router.Mode
	}{
		{"Coding", router.ModeCoding},
		{" REASONING ", router.ModeReasoning},
		{"\tVision", router.ModeVision},
		{"fast\n", router.ModeFast},
		{" PLANNING ", router.ModePlanning},
		{"agentic", router.ModeAgentic},
		{"LONG_HORIZON", router.ModeLongHorizon},
		{"  ", router.ModeDefault},
	}
	for _, tc := range cases {
		got, err := router.ParseMode(tc.in)
		if err != nil {
			t.Errorf("ParseMode(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseModeStillRejectsUnknownValues verifies normalization does NOT
// make unknown values valid: non-mode strings and internal modes still error.
func TestParseModeStillRejectsUnknownValues(t *testing.T) {
	for _, m := range []string{"bogus", "elite", " DEFAULT ", "auto-2"} {
		if _, err := router.ParseMode(m); err == nil {
			t.Errorf("ParseMode(%q) expected error, got nil", m)
		}
	}
}

// TestNormalizedModeStillRejectsInvalidPipelineInput verifies the
// pipeline surfaces an invalid-mode error for truly unknown mode strings
// even after normalization.
func TestNormalizedModeStillRejectsInvalidPipelineInput(t *testing.T) {
	pipeline, _, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "p", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	req := execReq(" BOGUS ", "hi")
	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
	if !contains(err.Error(), "invalid mode") {
		t.Fatalf("expected 'invalid mode' error, got: %v", err)
	}
}

// TestRequestedModePreservedRaw verifies the raw user-supplied mode string
// is preserved in DecisionContext.RequestedMode (auditability) while the
// resolved ModeProfile uses the canonical mode.
func TestRequestedModePreservedRaw(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "p", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "p", runtime.StateHealthy, 100)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     " CODING ",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}

	dc := router.NewDecisionContext(req, store.Snapshot(context.Background()), router.ConfigSnapshot{}, router.TaskMetadata{}, router.Environment{}, nil, nil)
	defer dc.Close()
	if err := pipeline.Stages()[0].Execute(context.Background(), dc); err != nil {
		t.Fatalf("stage execute: %v", err)
	}

	if dc.RequestedMode() != " CODING " {
		t.Errorf("requested_mode = %q, want raw %q", dc.RequestedMode(), " CODING ")
	}
	if dc.ModeProfile() == nil || dc.ModeProfile().Mode != router.ModeCoding {
		t.Errorf("resolved mode = %v, want %q", dc.ModeProfile(), router.ModeCoding)
	}
	if dc.ModeSource() != "explicit" {
		t.Errorf("mode_source = %q, want %q", dc.ModeSource(), "explicit")
	}
}
