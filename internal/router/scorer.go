package router

import (
	"sync"
	"sync/atomic"
	"time"
)

// Scorer computes a composite score for a provider candidate.
// Each factor returns a score in [0, 1] and is combined using configurable weights.
type Scorer struct {
	mu      sync.RWMutex
	weights Weights
}

// NewScorer creates a scorer with the given raw weights.
func NewScorer(raw RawWeights) *Scorer {
	return &Scorer{weights: Normalize(raw)}
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

// CompositeScore combines all factor scores using the current weights.
func (s *Scorer) CompositeScore(c Candidate, capHint CapabilityHint) float64 {
	w := s.LoadWeights()
	factors := []Factor{
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
		NewFactor("cost", func(c Candidate) float64 {
			if c.CostPerToken == nil {
				return 0.5
			}
			cost := *c.CostPerToken
			if cost <= 0 {
				return 1.0
			}
			if cost >= 0.001 {
				return 0.0
			}
			return 1.0 - cost/0.001
		}),
		NewFactor("capability", func(c Candidate) float64 {
			if !c.IsAvailable {
				return 0.0
			}
			return matchScore(capHint, c.Capabilities)
		}),
	}

	total := 0.0
	total += w.Health * factorScore(factors[0], c)
	total += w.Latency * factorScore(factors[1], c)
	total += w.Cost * factorScore(factors[2], c)
	total += w.Capability * factorScore(factors[3], c)
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
func (s *Scorer) FactorScores(c Candidate, capHint CapabilityHint) map[string]float64 {
	w := s.LoadWeights()
	factors := []Factor{
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
			score := 1.0 - 0.8*float64(c.LatencyMs-100)/float64(4900)
			if score < 0 {
				score = 0
			}
			return score
		}),
		NewFactor("cost", func(c Candidate) float64 {
			if c.CostPerToken == nil {
				return 0.5
			}
			cost := *c.CostPerToken
			if cost <= 0 {
				return 1.0
			}
			if cost >= 0.001 {
				return 0.0
			}
			return 1.0 - cost/0.001
		}),
		NewFactor("capability", func(c Candidate) float64 {
			if !c.IsAvailable {
				return 0.0
			}
			return matchScore(capHint, c.Capabilities)
		}),
	}
	out := make(map[string]float64, len(factors))
	for _, f := range factors {
		out[f.Name()] = factorScore(f, c)
	}
	_ = w
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
