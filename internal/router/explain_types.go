package router

// Signal represents a factor that influenced a routing decision.
type Signal struct {
	Name     string  `json:"name"`
	Value    float64 `json:"value"`
	Weight   float64 `json:"weight"`
	Source   string  `json:"source"`
	Directed bool    `json:"directed"` // true = pro-selection, false = anti-selection
}

// Penalty represents a factor that reduced a candidate's score.
type Penalty struct {
	Candidate string  `json:"candidate"`
	Name      string  `json:"name"`
	Amount    float64 `json:"amount"`
	Reason    string  `json:"reason,omitempty"`
}

// CandidateComparison holds the structured comparison data for one candidate.
type CandidateComparison struct {
	ProviderName    string             `json:"provider_name"`
	ProviderModelID string             `json:"provider_model_id"`
	Scores          map[string]float64 `json:"scores"`
	TotalScore      float64            `json:"total_score"`
	Selected        bool               `json:"selected"`
	Rejected        bool               `json:"rejected"`
	RejectionReason string             `json:"rejection_reason,omitempty"`
	Signals         []Signal           `json:"signals,omitempty"`
	Penalties       []Penalty          `json:"penalties,omitempty"`
}

// DecisionExplanation is the structured explanation for a routing decision.
// It contains no natural-language text; all reasoning is encoded in structured fields.
type DecisionExplanation struct {
	DecisionID           DecisionID            `json:"decision_id"`
	SelectedProvider     string                `json:"selected_provider"`
	SelectedModelID      string                `json:"selected_model_id"`
	CandidateComparisons []CandidateComparison `json:"candidate_comparisons"`
	WinningSignals       []Signal              `json:"winning_signals,omitempty"`
	WinningPenalties     []Penalty             `json:"winning_penalties,omitempty"`
	RoutingDurationMs    int64                 `json:"routing_duration_ms"`
}

// NewDecisionExplanation creates an explanation from a RoutingDecision and candidates.
func NewDecisionExplanation(decID DecisionID, decision RoutingDecision, candidates []Candidate) *DecisionExplanation {
	exp := &DecisionExplanation{
		DecisionID:        decID,
		SelectedProvider:  decision.SelectedProvider,
		SelectedModelID:   decision.SelectedModelID,
		RoutingDurationMs: decision.RoutingDurationMs,
	}

	for _, cs := range decision.CandidateScores {
		cc := CandidateComparison{
			ProviderName:    cs.Provider,
			ProviderModelID: cs.ProviderID,
			Scores: map[string]float64{
				"health":     cs.HealthScore,
				"latency":    cs.LatencyScore,
				"cost":       cs.CostScore,
				"capability": cs.CapScore,
			},
			TotalScore:      cs.TotalScore,
			Selected:        cs.Selected,
			Rejected:        cs.Rejected,
			RejectionReason: cs.RejectionReason,
		}
		exp.CandidateComparisons = append(exp.CandidateComparisons, cc)
	}

	// Extract winning signals (top positive contributors).
	for _, comp := range exp.CandidateComparisons {
		if !comp.Selected {
			continue
		}
		for name, score := range comp.Scores {
			if score >= 0.7 {
				exp.WinningSignals = append(exp.WinningSignals, Signal{
					Name:     name,
					Value:    score,
					Weight:   1.0,
					Source:   "scorer",
					Directed: true,
				})
			}
		}
	}

	// Extract winning penalties.
	for _, comp := range exp.CandidateComparisons {
		if !comp.Selected {
			continue
		}
		for _, rr := range decision.RejectionReasons {
			if rr.Provider == comp.ProviderName {
				exp.WinningPenalties = append(exp.WinningPenalties, Penalty{
					Candidate: comp.ProviderName,
					Name:      "rejection",
					Amount:    0.0,
					Reason:    rr.Reason,
				})
			}
		}
	}

	return exp
}
