package metrics

import (
	"sync"
	"sync/atomic"
)

type Counter struct {
	value int64
}

// Inc increments the counter by delta.
func (c *Counter) Inc(delta int64) {
	atomic.AddInt64(&c.value, delta)
}

// Get returns the current value.
func (c *Counter) Get() int64 {
	return atomic.LoadInt64(&c.value)
}

// Histogram tracks value distributions with configurable buckets.
type Histogram struct {
	mu       sync.Mutex
	buckets  []float64
	counts   []int64
	sum      float64
	count    int64
	min      float64
	max      float64
	observed bool
}

// NewHistogram creates a histogram with the given bucket boundaries.
// Buckets are upper boundaries; the last bucket is "+Inf".
func NewHistogram(buckets []float64) *Histogram {
	if len(buckets) == 0 {
		buckets = []float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}
	}
	h := &Histogram{
		buckets: make([]float64, len(buckets)),
		counts:  make([]int64, len(buckets)),
	}
	copy(h.buckets, buckets)
	h.min = 0
	h.max = 0
	return h
}

// Observe records a value.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sum += v
	h.count++
	if !h.observed || v < h.min {
		h.min = v
	}
	if !h.observed || v > h.max {
		h.max = v
	}
	h.observed = true
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i]++
		}
	}
}

// Sum returns the sum of all observed values.
func (h *Histogram) Sum() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sum
}

// Count returns the number of observations.
func (h *Histogram) Count() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// Min returns the minimum observed value.
func (h *Histogram) Min() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.observed {
		return 0
	}
	return h.min
}

// Max returns the maximum observed value.
func (h *Histogram) Max() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.observed {
		return 0
	}
	return h.max
}

// Buckets returns a copy of the bucket boundaries.
func (h *Histogram) Buckets() []float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]float64, len(h.buckets))
	copy(out, h.buckets)
	return out
}

// Counts returns a copy of the bucket counts.
func (h *Histogram) Counts() []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]int64, len(h.counts))
	copy(out, h.counts)
	return out
}

// Reset zeroes all state.
func (h *Histogram) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buckets = h.buckets[:0]
	h.counts = h.counts[:0]
	h.sum = 0
	h.count = 0
	h.min = 0
	h.max = 0
	h.observed = false
}

// Metric represents a single named metric with optional labels.
