package router

import (
	"testing"
)

func TestDefaultModeProfiles(t *testing.T) {
	profiles := DefaultModeProfiles()
	expectedModes := []Mode{ModeElite, ModeCoding, ModeReasoning, ModeVision, ModeFast, ModeDefault}
	for _, m := range expectedModes {
		if _, ok := profiles[m]; !ok {
			t.Errorf("expected profile for mode %q", m)
		}
	}
}

func TestModeProfileStructure(t *testing.T) {
	prefs := &RoutingWeightPreferences{
		Health:     0.5,
		Latency:    0.3,
		Cost:       0.1,
		Capability: 0.1,
	}
	profile := &ModeProfile{
		Mode:              ModeCoding,
		Requirements:      CapabilityProfile{Mode: ModeCoding, Confidence: 0.7},
		WeightPreferences: prefs,
	}
	if profile.Mode != ModeCoding {
		t.Error("expected ModeCoding")
	}
	if profile.WeightPreferences == nil {
		t.Fatal("expected non-nil weight preferences")
	}
	if profile.WeightPreferences.Health != 0.5 {
		t.Error("expected health weight 0.5")
	}
}
