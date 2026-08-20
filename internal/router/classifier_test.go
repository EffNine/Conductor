package router

import (
	"testing"
)

func TestClassifyRequestVision(t *testing.T) {
	tests := []string{
		"what is in this image",
		"describe this picture",
		"look at the screenshot",
		"what's in this diagram",
		"analyze this chart photo",
	}
	for _, tt := range tests {
		profile := ClassifyRequest(tt)
		if profile.Mode != ModeVision {
			t.Errorf("ClassifyRequest(%q) = %q, want %q", tt, profile.Mode, ModeVision)
		}
		if profile.Confidence != 0.8 {
			t.Errorf("ClassifyRequest(%q) confidence = %f, want 0.8", tt, profile.Confidence)
		}
	}
}

func TestClassifyRequestCoding(t *testing.T) {
	tests := []string{
		"write a function to sort an array",
		"debug this compile error",
		"refactor the repository",
		"create a unit test case",
		"fix the runtime exception stack trace",
		"build a git pull request",
	}
	for _, tt := range tests {
		profile := ClassifyRequest(tt)
		if profile.Mode != ModeCoding {
			t.Errorf("ClassifyRequest(%q) = %q, want %q", tt, profile.Mode, ModeCoding)
		}
	}
}

func TestClassifyRequestReasoning(t *testing.T) {
	tests := []string{
		"analyze the tradeoffs of this architecture",
		"explain why this works step by step",
		"compare the advantages and disadvantages",
		"prove this theorem derive the solution",
		"solve this problem with reasoning",
	}
	for _, tt := range tests {
		profile := ClassifyRequest(tt)
		if profile.Mode != ModeReasoning {
			t.Errorf("ClassifyRequest(%q) = %q, want %q", tt, profile.Mode, ModeReasoning)
		}
	}
}

func TestClassifyRequestFast(t *testing.T) {
	tests := []string{
		"hi there",
		"quick question",
		"brief hello",
		"just say thanks",
		"one word answer",
	}
	for _, tt := range tests {
		profile := ClassifyRequest(tt)
		if profile.Mode != ModeFast {
			t.Errorf("ClassifyRequest(%q) = %q, want %q", tt, profile.Mode, ModeFast)
		}
	}
}

func TestClassifyRequestElite(t *testing.T) {
	tests := []string{
		"architect and implement a complex end-to-end distributed system",
		"design a system and build the full backend infrastructure",
		"create a full multi-step microservice application",
	}
	for _, tt := range tests {
		profile := ClassifyRequest(tt)
		if profile.Mode != ModeElite {
			t.Errorf("ClassifyRequest(%q) = %q, want %q", tt, profile.Mode, ModeElite)
		}
	}
}

func TestClassifyRequestDefault(t *testing.T) {
	tests := []string{
		"tell me about the weather",
		"general conversation",
		"what is quantum physics",
		"",
	}
	for _, tt := range tests {
		profile := ClassifyRequest(tt)
		if profile.Mode != ModeDefault {
			t.Errorf("ClassifyRequest(%q) = %q, want %q", tt, profile.Mode, ModeDefault)
		}
	}
}

func TestClassifyRequestEmpty(t *testing.T) {
	profile := ClassifyRequest("")
	if profile.Mode != ModeDefault {
		t.Errorf("ClassifyRequest(\"\") = %q, want %q", profile.Mode, ModeDefault)
	}
	if profile.Confidence != 0.3 {
		t.Errorf("ClassifyRequest(\"\") confidence = %f, want 0.3", profile.Confidence)
	}
}

func TestClassifyRequestVisionTakesPriority(t *testing.T) {
	// Vision keywords should match even when other keywords are present.
	text := "hi look at this image and analyze it"
	profile := ClassifyRequest(text)
	if profile.Mode != ModeVision {
		t.Errorf("ClassifyRequest(vision priority) = %q, want %q", profile.Mode, ModeVision)
	}
}

func TestModeFromProfile(t *testing.T) {
	tests := []struct {
		input    string
		expected Mode
	}{
		{"elite", ModeElite},
		{"coding", ModeCoding},
		{"reasoning", ModeReasoning},
		{"vision", ModeVision},
		{"fast", ModeFast},
		{"default", ModeDefault},
		{"unknown", ModeDefault},
	}
	for _, tt := range tests {
		got := ModeFromProfile(tt.input)
		if got != tt.expected {
			t.Errorf("ModeFromProfile(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestCapabilityProfileStructure(t *testing.T) {
	profile := &CapabilityProfile{
		Mode:        ModeCoding,
		Confidence:  0.7,
		Description: "test description",
		Metadata:    map[string]any{"key": "value"},
	}
	if profile.Mode != ModeCoding {
		t.Error("expected ModeCoding")
	}
	if profile.Confidence != 0.7 {
		t.Error("expected 0.7 confidence")
	}
	if profile.Description != "test description" {
		t.Error("expected test description")
	}
	if profile.Metadata["key"] != "value" {
		t.Error("expected metadata key=value")
	}
}
