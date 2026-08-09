package cache

import (
	"sync/atomic"
	"time"
)

// StatsCollector tracks cache statistics with thread-safe access.
type StatsCollector struct {
	hits             atomic.Int64
	misses           atomic.Int64
	evictions        atomic.Int64
	expirations      atomic.Int64
	setOperations    atomic.Int64
	getOperations    atomic.Int64
	deleteOperations atomic.Int64
	clearOperations  atomic.Int64
	currentEntries   atomic.Int64
	latencySum       atomic.Int64 // cumulative latency in nanoseconds
	latencyCount     atomic.Int64
	maxLatency       atomic.Int64 // max latency in nanoseconds
	minLatency       atomic.Int64 // min latency in nanoseconds (stored as nanoseconds, initialized to max int64)
	minLatencySet    atomic.Bool
	lastAccessTime   atomic.Int64 // unix nanoseconds
}

// NewStatsCollector creates a new statistics collector.
func NewStatsCollector() *StatsCollector {
	return &StatsCollector{
		minLatency: atomic.Int64{},
	}
}

// RecordHit records a cache hit.
func (s *StatsCollector) RecordHit(latency time.Duration) {
	s.hits.Add(1)
	s.recordLatency(latency)
	s.lastAccessTime.Store(time.Now().UnixNano())
}

// RecordMiss records a cache miss.
func (s *StatsCollector) RecordMiss(latency time.Duration) {
	s.misses.Add(1)
	s.recordLatency(latency)
}

// RecordEviction records a cache eviction.
func (s *StatsCollector) RecordEviction() {
	s.evictions.Add(1)
}

// RecordExpiration records a cache expiration.
func (s *StatsCollector) RecordExpiration() {
	s.expirations.Add(1)
}

// RecordSet records a cache set operation.
func (s *StatsCollector) RecordSet() {
	s.setOperations.Add(1)
}

// RecordGet records a cache get operation.
func (s *StatsCollector) RecordGet() {
	s.getOperations.Add(1)
}

// RecordDelete records a cache delete operation.
func (s *StatsCollector) RecordDelete() {
	s.deleteOperations.Add(1)
}

// RecordClear records a cache clear operation.
func (s *StatsCollector) RecordClear() {
	s.clearOperations.Add(1)
}

// IncrementEntries increments the current entry count.
func (s *StatsCollector) IncrementEntries(n int) {
	s.currentEntries.Add(int64(n))
}

// DecrementEntries decrements the current entry count.
func (s *StatsCollector) DecrementEntries(n int) {
	s.currentEntries.Add(-int64(n))
}

// SetEntries sets the current entry count.
func (s *StatsCollector) SetEntries(n int) {
	s.currentEntries.Store(int64(n))
}

// GetHitRate returns the cache hit rate as a float in [0, 1].
func (s *StatsCollector) GetHitRate() float64 {
	h := s.hits.Load()
	m := s.misses.Load()
	total := h + m
	if total == 0 {
		return 0.0
	}
	return float64(h) / float64(total)
}

// GetAverageLatency returns the average cache operation latency.
func (s *StatsCollector) GetAverageLatency() time.Duration {
	count := s.latencyCount.Load()
	if count == 0 {
		return 0
	}
	return time.Duration(s.latencySum.Load() / count)
}

// GetMaxLatency returns the maximum cache operation latency.
func (s *StatsCollector) GetMaxLatency() time.Duration {
	return time.Duration(s.maxLatency.Load())
}

// GetMinLatency returns the minimum cache operation latency.
func (s *StatsCollector) GetMinLatency() time.Duration {
	if !s.minLatencySet.Load() {
		return 0
	}
	return time.Duration(s.minLatency.Load())
}

// GetLastAccessTime returns the last time an entry was accessed.
func (s *StatsCollector) GetLastAccessTime() time.Time {
	return time.Unix(0, s.lastAccessTime.Load())
}

// Snapshot returns a point-in-time snapshot of all statistics.
func (s *StatsCollector) Snapshot() Stats {
	return Stats{
		Hits:             s.hits.Load(),
		Misses:           s.misses.Load(),
		Evictions:        s.evictions.Load(),
		Expirations:      s.expirations.Load(),
		SetOperations:    s.setOperations.Load(),
		GetOperations:    s.getOperations.Load(),
		DeleteOperations: s.deleteOperations.Load(),
		ClearOperations:  s.clearOperations.Load(),
		CurrentEntries:   s.currentEntries.Load(),
	}
}

func (s *StatsCollector) recordLatency(latency time.Duration) {
	ns := latency.Nanoseconds()
	s.latencySum.Add(ns)
	s.latencyCount.Add(1)

	// Update max.
	for {
		old := s.maxLatency.Load()
		if ns <= old {
			break
		}
		if s.maxLatency.CompareAndSwap(old, ns) {
			break
		}
	}

	// Update min.
	if !s.minLatencySet.Load() {
		s.minLatency.Store(ns)
		s.minLatencySet.Store(true)
	} else {
		for {
			old := s.minLatency.Load()
			if ns >= old {
				break
			}
			if s.minLatency.CompareAndSwap(old, ns) {
				break
			}
		}
	}
}

// Reset zeroes all statistics.
func (s *StatsCollector) Reset() {
	s.hits.Store(0)
	s.misses.Store(0)
	s.evictions.Store(0)
	s.expirations.Store(0)
	s.setOperations.Store(0)
	s.getOperations.Store(0)
	s.deleteOperations.Store(0)
	s.clearOperations.Store(0)
	s.currentEntries.Store(0)
	s.latencySum.Store(0)
	s.latencyCount.Store(0)
	s.maxLatency.Store(0)
	s.minLatency.Store(0)
	s.minLatencySet.Store(false)
	s.lastAccessTime.Store(0)
}
