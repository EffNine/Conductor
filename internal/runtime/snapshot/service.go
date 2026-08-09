// Package snapshot provides immutable runtime snapshots with hashing.
package snapshot

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/runtime"
)

// Snapshot is an immutable point-in-time view of all provider runtimes.
type Snapshot struct {
	Version     int                      `json:"version"`
	Timestamp   time.Time                `json:"timestamp"`
	Providers   map[string]ProviderState `json:"providers"`
	GlobalState GlobalState              `json:"global_state"`
	Hash        string                   `json:"hash"`
}

// ProviderState represents the state of a single provider in a snapshot.
type ProviderState struct {
	Name            string            `json:"name"`
	State           runtime.ProviderState `json:"state"`
	LatencyMs       int64             `json:"latency_ms"`
	ErrorRate       float64           `json:"error_rate"`
	Capacity        float64           `json:"capacity"`
	SuccessCount    int64             `json:"success_count"`
	FailureCount    int64             `json:"failure_count"`
	TotalRequests   int64             `json:"total_requests"`
	IsHealthy       bool              `json:"is_healthy"`
	LastError       string            `json:"last_error,omitempty"`
	LastHealthCheck time.Time         `json:"last_health_check"`
	Metadata        map[string]any    `json:"metadata,omitempty"`
}

// GlobalState captures system-wide runtime state.
type GlobalState struct {
	TotalProviders     int     `json:"total_providers"`
	HealthyProviders   int     `json:"healthy_providers"`
	DegradedProviders  int     `json:"degraded_providers"`
	UnhealthyProviders int     `json:"unhealthy_providers"`
	AvgLatencyMs       int64   `json:"avg_latency_ms"`
	TotalRequests      int64   `json:"total_requests"`
	OverallErrorRate   float64 `json:"overall_error_rate"`
}

// Service manages snapshot creation and versioning.
type Service struct {
	currentVersion int
	lastHash       string
}

// NewService creates a new snapshot service.
func NewService() *Service {
	return &Service{
		currentVersion: 0,
		lastHash:       "",
	}
}

// Create creates a new immutable snapshot from the runtime store.
func (s *Service) Create(providers map[string]runtime.ProviderRuntime) *Snapshot {
	s.currentVersion++

	providerStates := make(map[string]ProviderState)
	healthy := 0
	degraded := 0
	unhealthy := 0
	var totalLatency int64
	var totalRequests int64
	var totalFailures int64

	for name, runtime := range providers {
		snap := runtime.Snapshot(nil)
		stats := getStats(runtime)

		ps := ProviderState{
			Name:            name,
			State:           snap.State,
			LatencyMs:       snap.LatencyMs,
			ErrorRate:       snap.ErrorRate,
			Capacity:        snap.Capacity,
			SuccessCount:    stats.SuccessCount,
			FailureCount:    stats.FailureCount,
			TotalRequests:   stats.TotalRequests,
			IsHealthy:       runtime.IsHealthy(),
			LastError:       stats.LastError,
			LastHealthCheck: snap.LastHealthCheck,
		}

		providerStates[name] = ps
		totalLatency += snap.LatencyMs
		totalRequests += stats.TotalRequests
		totalFailures += stats.FailureCount

		switch snap.State {
		case "healthy":
			healthy++
		case "degraded":
			degraded++
		case "unhealthy", "recovering":
			unhealthy++
		}
	}

	var avgLatency int64
	if len(providers) > 0 {
		avgLatency = totalLatency / int64(len(providers))
	}

	var overallErrorRate float64
	if totalRequests > 0 {
		overallErrorRate = float64(totalFailures) / float64(totalRequests)
	}

	snap := &Snapshot{
		Version: s.currentVersion,
		Timestamp: time.Now().UTC(),
		Providers: providerStates,
		GlobalState: GlobalState{
			TotalProviders:     len(providers),
			HealthyProviders:   healthy,
			DegradedProviders:  degraded,
			UnhealthyProviders: unhealthy,
			AvgLatencyMs:       avgLatency,
			TotalRequests:      totalRequests,
			OverallErrorRate:   overallErrorRate,
		},
	}

	// Compute hash
	snap.Hash = s.computeHash(snap)
	s.lastHash = snap.Hash

	return snap
}

// GetVersion returns the current snapshot version.
func (s *Service) GetVersion() int {
	return s.currentVersion
}

// GetLastHash returns the hash of the last snapshot.
func (s *Service) GetLastHash() string {
	return s.lastHash
}

// IsSame compares two snapshots by hash.
func IsSame(a, b *Snapshot) bool {
	return a != nil && b != nil && a.Hash == b.Hash
}

// computeHash generates a SHA256 hash of the snapshot.
func (s *Service) computeHash(snapshot *Snapshot) string {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Sprintf("error:%v", err)
	}
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:8]) // Use first 8 bytes for brevity
}

// getStats extracts stats from a ProviderRuntime.
func getStats(r runtime.ProviderRuntime) runtime.ProviderStats {
	// Use reflection or type assertion to get stats
	// For now, we'll use a placeholder
	return runtime.ProviderStats{}
}
