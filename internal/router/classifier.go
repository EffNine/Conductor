package router

import (
	"fmt"
	"strings"
)

// Mode represents a request classification mode.
type Mode string

const (
	ModeElite       Mode = "elite"        // complex agentic coding / reasoning — internal only
	ModeCoding      Mode = "coding"       // code generation, debugging, refactoring
	ModeReasoning   Mode = "reasoning"    // analysis, comparison, multi-step logic
	ModeVision      Mode = "vision"       // image / screenshot understanding
	ModeFast        Mode = "fast"         // short, simple, low-latency requests
	ModeDefault     Mode = "default"      // no strong signal
	ModePlanning    Mode = "planning"     // long-horizon task planning — reasoning + tool-calling required
	ModeAgentic     Mode = "agentic"      // autonomous agent loops — execution-reliability weighted
	ModeLongHorizon Mode = "long_horizon" // extended context horizon — hard context enforcement
)

// publicModes is the set of mode strings exposed through the public API.
// Internal-only classifiers such as "elite" are intentionally excluded.
var publicModes = map[string]Mode{
	"auto":         ModeDefault,
	"coding":       ModeCoding,
	"reasoning":    ModeReasoning,
	"vision":       ModeVision,
	"fast":         ModeFast,
	"planning":     ModePlanning,
	"agentic":      ModeAgentic,
	"long_horizon": ModeLongHorizon,
}

// NormalizeMode canonicalizes a user-supplied mode string to its canonical
// form: lowercased with surrounding whitespace trimmed. Empty input remains
// empty — the empty string is a distinct API input ("mode omitted") from
// "auto" and must never be conflated with it.
//
// NormalizeMode is the single source of truth for mode canonicalization. Every
// consumer that compares or hashes user-supplied mode strings MUST use it
// (ParseMode normalizes internally; the handler cache key uses it directly).
// The raw user-supplied string is preserved for auditability in
// DecisionContext.RequestedMode.
func NormalizeMode(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ParseMode validates a public mode string and returns the canonical Mode.
// The input is normalized first (case-insensitive, whitespace-trimmed), so
// "Coding" and " coding " resolve to ModeCoding. It returns an error for
// unrecognized or internal-only values.
func ParseMode(s string) (Mode, error) {
	s = NormalizeMode(s)
	if s == "" {
		return ModeDefault, nil
	}
	m, ok := publicModes[s]
	if !ok {
		return "", fmt.Errorf("invalid mode %q: supported values are auto, coding, reasoning, vision, fast, planning, agentic, long_horizon", s)
	}
	return m, nil
}

// CapabilityProfile describes the semantic classification of a request.
// It is produced by the canonical classifier and consumed by the decision
// pipeline to inform scoring and candidate filtering.
type CapabilityProfile struct {
	// Mode is the classified request mode.
	Mode Mode
	// Confidence in [0, 1].
	Confidence float64
	// Description is a human-readable summary of the classification.
	Description string
	// Metadata carries ancillary classification signals.
	Metadata map[string]any
}

// ClassifyRequest classifies a request text into a CapabilityProfile using
// keyword heuristics. This is the single canonical classifier for the system.
func ClassifyRequest(text string) *CapabilityProfile {
	lower := strings.ToLower(text)

	// Vision is the most specific signal.
	if matchesAny(lower, []string{
		"image", "picture", "screenshot", "vision", "look at", "describe this",
		"what is in", "what's in", "diagram", "chart", "photo",
	}) {
		return &CapabilityProfile{
			Mode:        ModeVision,
			Confidence:  0.8,
			Description: "image or visual content understanding",
		}
	}

	// Elite / complex agentic coding.
	if matchesAny(lower, []string{"implement", "refactor", "architect", "design a system", "build a", "create a full", "end-to-end", "multi-step", "complex"}) &&
		matchesAny(lower, []string{"code", "function", "api", "service", "module", "app", "application", "system", "distributed", "microservice", "backend", "infrastructure"}) {
		return &CapabilityProfile{
			Mode:        ModeElite,
			Confidence:  0.75,
			Description: "complex agentic coding task requiring multi-step execution",
		}
	}

	// Coding tasks.
	if matchesAny(lower, []string{
		"code", "coding", "program", "function", "debug", "fix", "refactor",
		"implementation", "script", "algorithm", "test case", "unit test",
		"pull request", "commit", "git", "repo", "repository", "syntax",
		"compile", "build error", "runtime error", "stack trace", "exception",
		"write a", "create a", "build a",
	}) {
		return &CapabilityProfile{
			Mode:        ModeCoding,
			Confidence:  0.7,
			Description: "code generation, debugging, or refactoring",
		}
	}

	// Reasoning / analysis.
	if matchesAny(lower, []string{
		"analyze", "compare", "evaluate", "explain", "reason", "why", "how does",
		"trade-off", "tradeoff", "pros and cons", "advantages", "disadvantages",
		"summarize and", "step by step", "prove", "derive", "solve",
	}) {
		return &CapabilityProfile{
			Mode:        ModeReasoning,
			Confidence:  0.65,
			Description: "analysis, comparison, or multi-step logical reasoning",
		}
	}

	// Fast / trivial tasks.
	if matchesAny(lower, []string{
		"hi", "hello", "hey", "quick", "short", "brief", "one sentence",
		"one word", "simple", "just", "only", "greeting", "thank", "thanks",
	}) {
		return &CapabilityProfile{
			Mode:        ModeFast,
			Confidence:  0.6,
			Description: "short, simple request requiring minimal processing",
		}
	}

	return &CapabilityProfile{
		Mode:        ModeDefault,
		Confidence:  0.3,
		Description: "general task with no strong signal",
	}
}

// ModeFromProfile converts a profile string to a Mode.
// It preserves backward compatibility with the legacy automode.TaskType names.
func ModeFromProfile(profile string) Mode {
	switch profile {
	case "elite":
		return ModeElite
	case "coding":
		return ModeCoding
	case "reasoning":
		return ModeReasoning
	case "vision":
		return ModeVision
	case "fast":
		return ModeFast
	default:
		return ModeDefault
	}
}

func matchesAny(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}
