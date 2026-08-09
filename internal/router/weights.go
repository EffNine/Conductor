package router

import "sync"

// Weights holds the configured (and normalized) scoring weights for the routing engine.
type Weights struct {
	Health    float64
	Latency   float64
	Cost      float64
	Capability float64
}

// Normalize converts raw weights to fractions that sum to 1.
// If the total is zero or negative, equal weights are returned.
func Normalize(raw RawWeights) Weights {
	total := raw.Health + raw.Latency + raw.Cost + raw.Capability
	if total <= 0 {
		return Weights{0.25, 0.25, 0.25, 0.25}
	}
	return Weights{
		Health:    raw.Health / total,
		Latency:   raw.Latency / total,
		Cost:      raw.Cost / total,
		Capability: raw.Capability / total,
	}
}

// RawWeights holds the un-normalized weight values from configuration.
type RawWeights struct {
	Health    float64
	Latency   float64
	Cost      float64
	Capability float64
}

// Store is a thread-safe weights store used by the router engine.
type Store struct {
	mu     sync.RWMutex
	values Weights
}

// NewWeightsStore creates a store with the given raw weights.
func NewWeightsStore(raw RawWeights) *Store {
	return &Store{values: Normalize(raw)}
}

// Load returns the current normalized weights.
func (s *Store) Load() Weights {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values
}

// Update replaces the current weights.
func (s *Store) Update(raw RawWeights) {
	s.mu.Lock()
	s.values = Normalize(raw)
	s.mu.Unlock()
}
