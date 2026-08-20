package router

import (
	"sync"
	"sync/atomic"
	"time"
)

// Scorer computes a composite score for a provider candidate.
// Each factor returns a score in [0, 1] and is combined using configurable weights.
type Scorer struct {
	mu          sync.RWMutex
	weights     Weights
	costCeiling float64
}

// NewScorer creates a scorer with the given raw weights.
func NewScorer(raw RawWeights) *Scorer {
	return &Scorer{weights: Normalize(raw), costCeiling: 0.001}
}

// SetCostCeiling sets the cost-per-token ceiling used by the cost factor.
// It must match the engine's cost ceiling so that recorded CostScore
// components describe the composite score (trace explainability contract).
func (s *Scorer) SetCostCeiling(ceiling float64) {
	if ceiling <= 0 {
		ceiling = 0.001
	}
	s.mu.Lock()
	s.costCeiling = ceiling
	s.mu.Unlock()
}

// UpdateWeights replaces the current weights.
func (s *Scorer) UpdateWeights(raw RawWeights) {
	s.mu.Lock()
	s.weights = Normalize(raw)
	s.mu.Unlock()
}

// LoadWeights returns the current normalized weights.
func (s *Scorer) LoadWeights() Weights {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.weights
}

// Factor is a scoring factor that computes a score in [0, 1].
type Factor interface {
	Name() string
	Score(candidate Candidate) float64
}

// Candidate is the data available for scoring one provider.
type Candidate struct {
	ProviderName    string
	ProviderModelID string
	HealthScore     float64  // from health status
	LatencyMs       int64    // from latest probe / rolling average
	CostPerToken    *float64 // nil if unknown
	Capabilities    Capabilities
	IsAvailable     bool // false when breaker is open or provider disabled
	RejectionReason string
}

// FactorFn is a function adapter for Factor.
type FactorFn struct {
	name string
	fn   func(Candidate) float64
}

func (f *FactorFn) Name() string              { return f.name }
func (f *FactorFn) Score(c Candidate) float64 { return f.fn(c) }

// NewFactor creates a Factor from a function.
func NewFactor(name string, fn func(Candidate) float64) *FactorFn {
	return &FactorFn{name: name, fn: fn}
}

// HealthFactor scores based on provider health status.
type HealthFactor struct {
	// healthyLatencyMs is the expected healthy latency for normalization.
	healthyLatencyMs int64
}

// NewHealthFactor creates a factor that scores health and latency together.
// healthyLatencyMs is the target latency; scores degrade linearly above it.
func NewHealthFactor(healthyLatencyMs int64) *HealthFactor {
	if healthyLatencyMs <= 0 {
		healthyLatencyMs = 1000
	}
	return &HealthFactor{healthyLatencyMs: healthyLatencyMs}
}

func (f *HealthFactor) Name() string { return "health" }

func (f *HealthFactor) Score(c Candidate) float64 {
	if !c.IsAvailable {
		return 0.0
	}
	score := c.HealthScore
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	// Latency component: penalize high latency.
	var latencyScore float64
	if c.LatencyMs <= 0 {
		latencyScore = 0.5 // unknown latency is neutral
	} else if c.LatencyMs <= f.healthyLatencyMs {
		latencyScore = 1.0
	} else {
		latencyScore = 1.0 - 0.5*float64(c.LatencyMs-f.healthyLatencyMs)/float64(f.healthyLatencyMs)
		if latencyScore < 0 {
			latencyScore = 0
		}
	}
	return 0.6*score + 0.4*latencyScore
}

// CostFactor scores based on cost (lower is better).
type CostFactor struct {
	// maxCostPerToken is the cost above which a provider scores 0.
	maxCostPerToken float64
}

// NewCostFactor creates a cost factor. maxCostPerToken should be a realistic ceiling.
func NewCostFactor(maxCostPerToken float64) *CostFactor {
	if maxCostPerToken <= 0 {
		maxCostPerToken = 0.001 // $1 per 1K tokens
	}
	return &CostFactor{maxCostPerToken: maxCostPerToken}
}

func (f *CostFactor) Name() string { return "cost" }

func (f *CostFactor) Score(c Candidate) float64 {
	if c.CostPerToken == nil {
		return 0.5 // unknown cost is neutral
	}
	cost := *c.CostPerToken
	if cost <= 0 {
		return 1.0
	}
	if cost >= f.maxCostPerToken {
		return 0.0
	}
	return 1.0 - cost/f.maxCostPerToken
}

// CapabilityFactor scores based on capability match.
type CapabilityFactor struct{}

func (f *CapabilityFactor) Name() string { return "capability" }

func (f *CapabilityFactor) Score(c Candidate) float64 {
	if !c.IsAvailable {
		return 0.0
	}
	// Use the capabilities score embedded in the candidate.
	// In practice this is computed by the selection engine from the request hint.
	return 1.0
}

// buildFactors constructs the four scoring factors for a candidate.
// Capability bonuses are applied separately by CompositeScoreWithBonuses as a
// mode-preference term; they are intentionally NOT folded into the capability
// factor here to avoid double counting and score capping.
func (s *Scorer) buildFactors(c Candidate, capHint CapabilityHint) []Factor {
	capFn := func(c Candidate) float64 {
		if !c.IsAvailable {
			return 0.0
		}
		return matchScore(capHint, c.Capabilities)
	}

	s.mu.RLock()
	costCeiling := s.costCeiling
	s.mu.RUnlock()

	return []Factor{
		NewFactor("health", func(c Candidate) float64 {
			if !c.IsAvailable {
				return 0.0
			}
			return c.HealthScore
		}),
		NewFactor("latency", func(c Candidate) float64 {
			if !c.IsAvailable {
				return 0.0
			}
			if c.LatencyMs <= 0 {
				return 0.5
			}
			// Exponential decay from 1.0 at 100ms to 0 at 5000ms.
			score := 1.0 - 0.8*float64(c.LatencyMs-100)/float64(4900)
			if score < 0 {
				score = 0
			}
			return score
		}),
		NewFactor("cost", NewCostFactor(costCeiling).Score),
		NewFactor("capability", capFn),
	}
}

// capabilityBonusContributions computes the mode bonus terms that
// CompositeScoreWithBonuses adds to a candidate's base score. It is the single
// source of truth for the bonus arithmetic, shared by the scorer (which adds
// the terms) and the selection engine (which records them on CandidateScore
// for trace explainability).
//
// Returns:
//   - modeBonus: 0.05 * (tool_calling + reasoning + structured bonuses that
//     match the candidate's capabilities)
//   - contextBonus: 0.05 * context_capacity bonus when the candidate has a
//     known MaxContext (the "context bonus" component of the trace)
func capabilityBonusContributions(c Candidate, bonuses CapabilityBonuses) (modeBonus float64, contextBonus float64) {
	if bonuses.IsZero() {
		return 0, 0
	}
	pref := 0.0
	if bonuses.ToolCalling > 0 && c.Capabilities.ToolCalling {
		pref += bonuses.ToolCalling
	}
	if bonuses.Reasoning > 0 && c.Capabilities.Reasoning {
		pref += bonuses.Reasoning
	}
	if bonuses.Structured > 0 && c.Capabilities.Structured {
		pref += bonuses.Structured
	}
	if bonuses.ContextCapacity > 0 && c.Capabilities.MaxContext > 0 {
		contextBonus = 0.05 * bonuses.ContextCapacity
	}
	modeBonus = 0.05 * pref
	return modeBonus, contextBonus
}

// CompositeScore combines all factor scores using the stored weights.
func (s *Scorer) CompositeScore(c Candidate, capHint CapabilityHint) float64 {
	return s.compositeWith(c, capHint, s.LoadWeights(), CapabilityBonuses{})
}

// CompositeScoreWithWeights combines all factor scores using the provided
// weights instead of the stored ones. Weights must already be normalized
// (sum to 1).
func (s *Scorer) CompositeScoreWithWeights(c Candidate, capHint CapabilityHint, weights Weights) float64 {
	return s.compositeWith(c, capHint, weights, CapabilityBonuses{})
}

// CompositeScoreWithBonuses combines all factor scores using the provided
// weights and applies mode-specific capability bonuses as a separate
// preference score. The bonuses are added as a small additive term rather
// than modifying the capability factor directly, to avoid capping issues.
func (s *Scorer) CompositeScoreWithBonuses(c Candidate, capHint CapabilityHint, weights Weights, bonuses CapabilityBonuses) float64 {
	base := s.compositeWith(c, capHint, weights, CapabilityBonuses{})

	// Mode preference score: providers matching mode-relevant capabilities
	// receive a small additive bonus. This is separate from the main scoring
	// dimensions to ensure meaningful differentiation without capping issues.
	modeBonus, contextBonus := capabilityBonusContributions(c, bonuses)
	return base + modeBonus + contextBonus
}

func (s *Scorer) compositeWith(c Candidate, capHint CapabilityHint, weights Weights, _ CapabilityBonuses) float64 {
	factors := s.buildFactors(c, capHint)
	total := 0.0
	total += weights.Health * factorScore(factors[0], c)
	total += weights.Latency * factorScore(factors[1], c)
	total += weights.Cost * factorScore(factors[2], c)
	total += weights.Capability * factorScore(factors[3], c)
	return total
}

func factorScore(f Factor, c Candidate) float64 {
	s := f.Score(c)
	if s < 0 {
		s = 0
	}
	if s > 1 {
		s = 1
	}
	return s
}

// FactorScores returns the individual factor scores for a candidate.
// It reuses buildFactors so the factor definitions stay in sync with the
// composite scoring path (single source of truth).
func (s *Scorer) FactorScores(c Candidate, capHint CapabilityHint) map[string]float64 {
	factors := s.buildFactors(c, capHint)
	out := make(map[string]float64, len(factors))
	for _, f := range factors {
		out[f.Name()] = factorScore(f, c)
	}
	return out
}

// RollingLatency tracks a rolling average latency for a provider.
type RollingLatency struct {
	mu     sync.Mutex
	values [64]int64
	idx    int
	count  int
	sum    int64
}

// NewRollingLatency creates a rolling latency tracker with a window of 64 samples.
func NewRollingLatency() *RollingLatency {
	return &RollingLatency{}
}

// Record adds a new latency sample.
func (r *RollingLatency) Record(ms int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.idx % 64
	if r.count < 64 {
		r.sum += ms
	} else {
		r.sum -= r.values[i]
		r.sum += ms
	}
	r.values[i] = ms
	r.idx++
	if r.count < 64 {
		r.count++
	}
}

// Average returns the rolling average latency in ms, or 0 if no samples.
func (r *RollingLatency) Average() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count == 0 {
		return 0
	}
	return r.sum / int64(r.count)
}

// ProviderMetrics holds per-provider metrics for the routing engine.
type ProviderMetrics struct {
	mu              sync.Mutex
	latency         *RollingLatency
	requestCount    atomic.Int64
	successCount    atomic.Int64
	failureCount    atomic.Int64
	lastRequestTime time.Time
}

// NewProviderMetrics creates a new ProviderMetrics.
func NewProviderMetrics() *ProviderMetrics {
	return &ProviderMetrics{
		latency: NewRollingLatency(),
	}
}

// RecordResult records a request result for a provider.
func (m *ProviderMetrics) RecordResult(latencyMs int64, success bool) {
	m.latency.Record(latencyMs)
	m.requestCount.Add(1)
	if success {
		m.successCount.Add(1)
	} else {
		m.failureCount.Add(1)
	}
	m.lastRequestTime = time.Now()
}

// HealthScore returns a health score in [0, 1] based on recent success rate.
func (m *ProviderMetrics) HealthScore() float64 {
	m.mu.Lock()
	req := m.requestCount.Load()
	succ := m.successCount.Load()
	fail := m.failureCount.Load()
	m.mu.Unlock()

	if req == 0 {
		return 0.5 // unknown
	}
	return float64(succ) / float64(succ+fail)
}

// RollingLatencyMs returns the rolling average latency.
func (m *ProviderMetrics) RollingLatencyMs() int64 {
	return m.latency.Average()
}

// MetricsStore holds per-provider metrics.
type MetricsStore struct {
	mu      sync.RWMutex
	metrics map[string]*ProviderMetrics
}

// NewMetricsStore creates an empty metrics store.
func NewMetricsStore() *MetricsStore {
	return &MetricsStore{
		metrics: make(map[string]*ProviderMetrics),
	}
}

// Get returns the metrics for a provider, creating if absent.
func (s *MetricsStore) Get(name string) *ProviderMetrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.metrics[name]; ok {
		return m
	}
	m := NewProviderMetrics()
	s.metrics[name] = m
	return m
}

// LoadAll returns a copy of all provider metrics.
func (s *MetricsStore) LoadAll() map[string]*ProviderMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*ProviderMetrics, len(s.metrics))
	for k, v := range s.metrics {
		out[k] = v
	}
	return out
}
