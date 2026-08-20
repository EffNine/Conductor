package router

// ModeProfile represents a mode-specific configuration for routing.
// It binds a mode to its capability requirements, weight preferences, and
// capability bonuses that bias provider selection toward mode-relevant features.
//
// A mode expresses:
//   - hard requirements (must-have capabilities; enforced by matchScore)
//   - soft preferences (preferred capabilities, penalized if missing via matchScore)
//   - routing weight preferences (relative scoring weights overriding globals)
//   - capability bonuses (additive score when provider has mode-relevant capability)
//   - a static policy description (Description + Traits) for trace explainability
type ModeProfile struct {
	// Mode is the mode identifier (e.g. "coding", "reasoning").
	Mode Mode
	// Active indicates whether this mode has implemented routing semantics.
	// Inactive modes are recognized by the public API but must not be routed.
	Active bool
	// Requirements are the hard and soft capability requirements for this mode.
	// NOTE: currently unused by routing — hard requirements are enforced in
	// selection.go. Kept for future consolidation; see P3.14 report.
	Requirements CapabilityProfile
	// WeightPreferences are relative routing weight overrides for this mode.
	// When nil, global defaults are used.
	WeightPreferences *RoutingWeightPreferences
	// CapabilityBonuses are additive bonuses to the capability factor when
	// a provider advertises the matching capability. These make mode-relevant
	// providers score higher without making the capability a hard requirement.
	CapabilityBonuses CapabilityBonuses
	// Description is a static, machine-stable prose summary of the mode's
	// routing policy. It is trace metadata, never generated per request.
	Description string
	// Traits are static machine-readable tags describing the mode's policy.
	// They are trace metadata, never generated per request.
	Traits []string
}

// RoutingWeightPreferences holds mode-specific weight overrides.
// Values are raw (un-normalized); they are normalized at use time.
type RoutingWeightPreferences struct {
	Health     float64
	Latency    float64
	Cost       float64
	Capability float64
}

// CapabilityBonuses adds a bonus to the capability factor score when the
// provider has the corresponding capability. Positive values reward the
// capability; zero or negative values are ignored.
type CapabilityBonuses struct {
	ToolCalling     float64
	Reasoning       float64
	Structured      float64
	ContextCapacity float64 // bonus for models with larger context headroom
}

// IsZero reports whether all bonuses are zero or negative.
func (b CapabilityBonuses) IsZero() bool {
	return b.ToolCalling <= 0 && b.Reasoning <= 0 && b.Structured <= 0 && b.ContextCapacity <= 0
}

// DefaultModeProfiles returns the set of mode profiles with their
// routing-relevant configurations. Each mode defines explicit weight
// overrides and capability bonuses so that the classifier's mode signal
// meaningfully influences provider selection.
func DefaultModeProfiles() map[Mode]*ModeProfile {
	return map[Mode]*ModeProfile{
		// Elite is deferred — no mode-specific behavior yet.
		ModeElite: {
			Mode:   ModeElite,
			Active: false,
		},

		// Coding: strong capability emphasis, bonus for tool-calling and reasoning.
		ModeCoding: {
			Mode:        ModeCoding,
			Active:      true,
			Description: "tool_calling/reasoning capability preference; capability-weighted scoring",
			Traits:      []string{"tool_calling_preference", "reasoning_preference", "capability_weighted"},
			WeightPreferences: &RoutingWeightPreferences{
				Health:     25,
				Latency:    10,
				Cost:       5,
				Capability: 60,
			},
			CapabilityBonuses: CapabilityBonuses{
				ToolCalling: 0.25,
				Reasoning:   0.15,
				Structured:  0.1,
			},
		},

		// Reasoning: strongest capability emphasis, bonus for reasoning.
		ModeReasoning: {
			Mode:        ModeReasoning,
			Active:      true,
			Description: "reasoning-capability preference; capability-weighted scoring",
			Traits:      []string{"reasoning_preference", "capability_weighted"},
			WeightPreferences: &RoutingWeightPreferences{
				Health:     20,
				Latency:    10,
				Cost:       5,
				Capability: 65,
			},
			CapabilityBonuses: CapabilityBonuses{
				Reasoning: 0.35,
			},
		},

		// Vision: baseline weights; vision is already a hard filter via matchScore.
		ModeVision: {
			Mode:        ModeVision,
			Active:      true,
			Description: "vision capability hard requirement when the request carries image content",
			Traits:      []string{"vision_hard_requirement", "baseline_weights"},
			WeightPreferences: &RoutingWeightPreferences{
				Health:     40,
				Latency:    20,
				Cost:       15,
				Capability: 25,
			},
		},

		// Fast: latency dominates, health still matters to avoid broken endpoints.
		ModeFast: {
			Mode:        ModeFast,
			Active:      true,
			Description: "latency-sensitive; health-protected; capability-neutral",
			Traits:      []string{"latency_sensitive", "health_protected"},
			WeightPreferences: &RoutingWeightPreferences{
				Health:     55,
				Latency:    40,
				Cost:       3,
				Capability: 2,
			},
		},

		// Default: explicit copy of global defaults for clarity.
		ModeDefault: {
			Mode:        ModeDefault,
			Active:      true,
			Description: "baseline/general routing with global default weights",
			Traits:      []string{"baseline"},
			WeightPreferences: &RoutingWeightPreferences{
				Health:     40,
				Latency:    25,
				Cost:       15,
				Capability: 20,
			},
		},

		// Planning — reasoning + tool-aware decomposition with execution reliability.
		ModePlanning: {
			Mode:        ModePlanning,
			Active:      true,
			Description: "reasoning + tool_calling hard requirement; execution reliability preference",
			Traits:      []string{"reasoning_tool_hard_requirement", "execution_reliability_preference"},
			WeightPreferences: &RoutingWeightPreferences{
				Health:     40,
				Latency:    10,
				Cost:       5,
				Capability: 45,
			},
			CapabilityBonuses: CapabilityBonuses{
				ToolCalling: 0.20,
				Reasoning:   0.25,
			},
		},

		// Agentic — sustained multi-step reasoning and tool execution with
		// strong execution reliability and context depth. Stricter than Planning
		// on execution/telemetry signals while sharing the same hard capability
		// requirements (Reasoning + ToolCalling).
		ModeAgentic: {
			Mode:        ModeAgentic,
			Active:      true,
			Description: "reasoning + tool_calling hard requirement; context capacity hard requirement; stronger execution reliability preference",
			Traits:      []string{"reasoning_tool_hard_requirement", "context_hard_requirement", "execution_reliability_preference_strong"},
			WeightPreferences: &RoutingWeightPreferences{
				Health:     55,
				Latency:    10,
				Cost:       5,
				Capability: 30,
			},
			CapabilityBonuses: CapabilityBonuses{
				ToolCalling:     0.30,
				Reasoning:       0.30,
				ContextCapacity: 0.10,
			},
		},

		// LongHorizon — extended context horizon with hard context enforcement.
		ModeLongHorizon: {
			Mode:        ModeLongHorizon,
			Active:      true,
			Description: "context capacity hard requirement; sustained reliability preference",
			Traits:      []string{"context_hard_requirement", "reliability_preference"},
			WeightPreferences: &RoutingWeightPreferences{
				Health:     40,
				Latency:    10,
				Cost:       5,
				Capability: 45,
			},
			CapabilityBonuses: CapabilityBonuses{
				ContextCapacity: 0.10,
			},
		},
	}
}
