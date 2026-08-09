package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Counter is a thread-safe monotonic counter.
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
type Metric struct {
	Name   string
	Labels map[string]string
	Value  int64
}

// providerStreamLive holds live per-provider stream statistics.
type providerStreamLive struct {
	started         *Counter
	completed       *Counter
	cancelled       *Counter
	timeout         *Counter
	errors          *Counter
	chunks          *Counter
	bytes           *Counter
	duration        *Histogram
	chunksPerStream *Histogram
	bytesPerStream  *Histogram
}

// ProviderStreamStats is a point-in-time snapshot of per-provider stream
// statistics.
type ProviderStreamStats struct {
	Started         int64
	Completed       int64
	Cancelled       int64
	Timeout         int64
	Errors          int64
	Chunks          int64
	Bytes           int64
	Duration        MetricHistogram
	ChunksPerStream MetricHistogram
	BytesPerStream  MetricHistogram
}

// providerStream returns the live stats for a provider, creating them on
// first use.
func (c *Collector) providerStream(provider string) *providerStreamLive {
	c.mu.RLock()
	s, ok := c.streamStatsByProvider[provider]
	c.mu.RUnlock()
	if ok {
		return s
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streamStatsByProvider == nil {
		c.streamStatsByProvider = make(map[string]*providerStreamLive)
	}
	if s, ok = c.streamStatsByProvider[provider]; ok {
		return s
	}
	s = &providerStreamLive{
		started:         &Counter{},
		completed:       &Counter{},
		cancelled:       &Counter{},
		timeout:         &Counter{},
		errors:          &Counter{},
		chunks:          &Counter{},
		bytes:           &Counter{},
		duration:        NewHistogram([]float64{50, 100, 250, 500, 1000, 2500, 5000, 10000}),
		chunksPerStream: NewHistogram([]float64{1, 5, 10, 25, 50, 100, 250, 500, 1000}),
		bytesPerStream:  NewHistogram([]float64{1024, 4096, 16384, 65536, 262144, 1048576}),
	}
	c.streamStatsByProvider[provider] = s
	return s
}

// Collector collects metrics for the gateway.
type Collector struct {
	mu sync.RWMutex

	// Request counters
	requestsTotal     *Counter
	errorsTotal       *Counter
	streamsTotal      *Counter
	streamErrorsTotal *Counter

	// Stream lifecycle counters
	streamStarted     *Counter
	streamCompleted   *Counter
	streamCancelled   *Counter
	streamTimeout     *Counter
	streamChunksTotal *Counter
	streamBytesTotal  *Counter

	// Stream duration histogram
	streamDurationMs *Histogram

	// Active stream gauge (high/low via atomic counter)
	activeStreams *Counter

	// Per-stream histograms (one observation per completed stream)
	streamChunks *Histogram
	streamBytes  *Histogram

	// Per-provider stream statistics
	streamStatsByProvider map[string]*providerStreamLive

	// Provider latency (milliseconds)
	providerLatency           *Histogram
	providerLatencyByProvider map[string]*Histogram

	// Token counters
	promptTokensTotal     *Counter
	completionTokensTotal *Counter
	totalTokensTotal      *Counter

	// Retry counters
	retriesTotal *Counter

	// Health probe counters
	probesTotal  *Counter
	probeSuccess *Counter
	probeFailure *Counter

	// Circuit breaker counters
	breakerRejections *Counter
	breakerOpens      *Counter

	// Routing counters
	routingDecisions *Counter
	routingLatency   *Histogram

	// Cache counters
	cacheHits          *Counter
	cacheMisses        *Counter
	cacheStores        *Counter
	cacheEvictions     *Counter
	cacheLookupLatency *Histogram
	cacheStoreLatency  *Histogram

	// Provider registry counters
	providerRegistrations   *Counter
	providerUnregistrations *Counter
	providerLookups         *Counter
	providerLookupLatency   *Histogram

	// Uptime
	startTime time.Time
}

// NewCollector creates a new metrics collector.
func NewCollector() *Collector {
	return &Collector{
		requestsTotal:           &Counter{},
		errorsTotal:             &Counter{},
		streamsTotal:            &Counter{},
		streamErrorsTotal:       &Counter{},
		streamStarted:           &Counter{},
		streamCompleted:         &Counter{},
		streamCancelled:         &Counter{},
		streamTimeout:           &Counter{},
		streamChunksTotal:       &Counter{},
		streamBytesTotal:        &Counter{},
		streamDurationMs:        NewHistogram([]float64{50, 100, 250, 500, 1000, 2500, 5000, 10000}),
		activeStreams:           &Counter{},
		streamChunks:            NewHistogram([]float64{1, 5, 10, 25, 50, 100, 250, 500, 1000}),
		streamBytes:             NewHistogram([]float64{1024, 4096, 16384, 65536, 262144, 1048576}),
		providerLatency:         NewHistogram([]float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}),
		probesTotal:             &Counter{},
		probeSuccess:            &Counter{},
		probeFailure:            &Counter{},
		breakerRejections:       &Counter{},
		breakerOpens:            &Counter{},
		promptTokensTotal:       &Counter{},
		completionTokensTotal:   &Counter{},
		totalTokensTotal:        &Counter{},
		retriesTotal:            &Counter{},
		routingDecisions:        &Counter{},
		routingLatency:          NewHistogram([]float64{1, 5, 10, 25, 50, 100, 250}),
		cacheHits:               &Counter{},
		cacheMisses:             &Counter{},
		cacheStores:             &Counter{},
		cacheEvictions:          &Counter{},
		cacheLookupLatency:      NewHistogram([]float64{1, 2, 5, 10, 25, 50, 100}),
		cacheStoreLatency:       NewHistogram([]float64{1, 2, 5, 10, 25, 50, 100}),
		providerRegistrations:   &Counter{},
		providerUnregistrations: &Counter{},
		providerLookups:         &Counter{},
		providerLookupLatency:   NewHistogram([]float64{1, 2, 5, 10, 25, 50}),
		startTime:               time.Now().UTC(),
	}
}

// IncrementRequests increments the total request counter.
func (c *Collector) IncrementRequests() {
	c.requestsTotal.Inc(1)
}

// IncrementErrors increments the total error counter.
func (c *Collector) IncrementErrors() {
	c.errorsTotal.Inc(1)
}

// IncrementStreams increments the stream counter.
func (c *Collector) IncrementStreams() {
	c.streamsTotal.Inc(1)
}

// IncrementStreamErrors increments the stream error counter.
func (c *Collector) IncrementStreamErrors() {
	c.streamErrorsTotal.Inc(1)
}

// IncrementStreamStarted increments the stream started counter.
func (c *Collector) IncrementStreamStarted() {
	c.streamStarted.Inc(1)
}

// IncrementActiveStreams increments the active stream gauge.
func (c *Collector) IncrementActiveStreams() {
	c.activeStreams.Inc(1)
}

// DecrementActiveStreams decrements the active stream gauge.
func (c *Collector) DecrementActiveStreams() {
	c.activeStreams.Inc(-1)
}

// ActiveStreams returns the number of currently active streams.
func (c *Collector) ActiveStreams() int64 {
	return c.activeStreams.Get()
}

// IncrementStreamCompleted increments the stream completed counter.
func (c *Collector) IncrementStreamCompleted() {
	c.streamCompleted.Inc(1)
}

// IncrementStreamCancelled increments the stream cancelled counter.
func (c *Collector) IncrementStreamCancelled() {
	c.streamCancelled.Inc(1)
}

// IncrementStreamTimeout increments the stream timeout counter.
func (c *Collector) IncrementStreamTimeout() {
	c.streamTimeout.Inc(1)
}

// RecordStreamChunk increments the stream chunks counter.
func (c *Collector) RecordStreamChunk(n int) {
	c.streamChunksTotal.Inc(int64(n))
}

// RecordStreamBytes increments the stream bytes counter.
func (c *Collector) RecordStreamBytes(n int) {
	c.streamBytesTotal.Inc(int64(n))
}

// ObserveStreamChunks records the number of chunks emitted by a single
// stream into the per-stream histogram.
func (c *Collector) ObserveStreamChunks(n int) {
	c.streamChunks.Observe(float64(n))
}

// ObserveStreamBytes records the number of bytes written by a single
// stream into the per-stream histogram.
func (c *Collector) ObserveStreamBytes(n int) {
	c.streamBytes.Observe(float64(n))
}

// StreamOutcome describes how a streaming session ended.
type StreamOutcome int

const (
	// StreamCompleted means the stream ended normally (upstream [DONE]).
	StreamCompleted StreamOutcome = iota
	// StreamCancelled means the stream was aborted before completion
	// (client disconnect or request cancellation).
	StreamCancelled
	// StreamTimeout means the provider stopped producing chunks for the
	// configured idle timeout.
	StreamTimeout
	// StreamError means the stream failed partway (provider error chunk,
	// truncated provider stream, or a stream that never started).
	StreamError
)

func (o StreamOutcome) String() string {
	switch o {
	case StreamCompleted:
		return "completed"
	case StreamCancelled:
		return "cancelled"
	case StreamTimeout:
		return "timeout"
	case StreamError:
		return "error"
	default:
		return "unknown"
	}
}

// RecordStreamOutcome records the final outcome of a stream: the outcome
// counter, duration/chunks/bytes histograms, and the per-provider stats.
func (c *Collector) RecordStreamOutcome(provider string, outcome StreamOutcome, chunks, bytes int, durationMs int64) {
	c.RecordStreamDuration(durationMs)
	c.ObserveStreamChunks(chunks)
	c.ObserveStreamBytes(bytes)
	switch outcome {
	case StreamCompleted:
		c.IncrementStreamCompleted()
	case StreamCancelled:
		c.IncrementStreamCancelled()
	case StreamTimeout:
		c.IncrementStreamTimeout()
	case StreamError:
		c.IncrementStreamErrors()
	}

	s := c.providerStream(provider)
	s.duration.Observe(float64(durationMs))
	s.chunksPerStream.Observe(float64(chunks))
	s.bytesPerStream.Observe(float64(bytes))
	s.chunks.Inc(int64(chunks))
	s.bytes.Inc(int64(bytes))
	switch outcome {
	case StreamCompleted:
		s.completed.Inc(1)
	case StreamCancelled:
		s.cancelled.Inc(1)
	case StreamTimeout:
		s.timeout.Inc(1)
	case StreamError:
		s.errors.Inc(1)
	}
}

// RecordStreamStarted records that a stream began for a provider.
func (c *Collector) RecordStreamStarted(provider string) {
	c.IncrementStreamStarted()
	c.providerStream(provider).started.Inc(1)
}

// RecordStreamDuration records a stream duration in milliseconds.
func (c *Collector) RecordStreamDuration(latencyMs int64) {
	c.streamDurationMs.Observe(float64(latencyMs))
}

// RecordProviderLatency records a provider call latency in milliseconds.
func (c *Collector) RecordProviderLatency(latencyMs int64) {
	c.providerLatency.Observe(float64(latencyMs))
}

// RecordProviderLatencyForProvider records a provider call latency for a specific provider.
func (c *Collector) RecordProviderLatencyForProvider(provider string, latencyMs int64) {
	c.mu.RLock()
	h, ok := c.providerLatencyByProvider[provider]
	c.mu.RUnlock()
	if !ok {
		c.mu.Lock()
		if c.providerLatencyByProvider == nil {
			c.providerLatencyByProvider = make(map[string]*Histogram)
		}
		c.providerLatencyByProvider[provider] = NewHistogram([]float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000})
		h = c.providerLatencyByProvider[provider]
		c.mu.Unlock()
	}
	h.Observe(float64(latencyMs))
}

// RecordPromptTokens records prompt token usage.
func (c *Collector) RecordPromptTokens(n int) {
	c.promptTokensTotal.Inc(int64(n))
}

// RecordCompletionTokens records completion token usage.
func (c *Collector) RecordCompletionTokens(n int) {
	c.completionTokensTotal.Inc(int64(n))
}

// RecordTotalTokens records total token usage.
func (c *Collector) RecordTotalTokens(n int) {
	c.totalTokensTotal.Inc(int64(n))
}

// IncrementRetries increments the retry counter.
func (c *Collector) IncrementRetries() {
	c.retriesTotal.Inc(1)
}

// IncrementProbes increments the probe counter.
func (c *Collector) IncrementProbes() {
	c.probesTotal.Inc(1)
}

// IncrementProbeSuccess increments the successful probe counter.
func (c *Collector) IncrementProbeSuccess() {
	c.probeSuccess.Inc(1)
}

// IncrementProbeFailure increments the failed probe counter.
func (c *Collector) IncrementProbeFailure() {
	c.probeFailure.Inc(1)
}

// IncrementBreakerRejections increments the breaker rejection counter.
func (c *Collector) IncrementBreakerRejections() {
	c.breakerRejections.Inc(1)
}

// RecordBreakerOpen increments the breaker open event counter.
func (c *Collector) RecordBreakerOpen() {
	c.breakerOpens.Inc(1)
}

// IncrementRoutingDecisions increments the routing decision counter.
func (c *Collector) IncrementRoutingDecisions() {
	c.routingDecisions.Inc(1)
}

// RecordRoutingLatency records a routing decision latency in milliseconds.
func (c *Collector) RecordRoutingLatency(latencyMs int64) {
	c.routingLatency.Observe(float64(latencyMs))
}

// IncrementCacheHits increments the cache hit counter.
func (c *Collector) IncrementCacheHits() {
	c.cacheHits.Inc(1)
}

// IncrementCacheMisses increments the cache miss counter.
func (c *Collector) IncrementCacheMisses() {
	c.cacheMisses.Inc(1)
}

// IncrementCacheStores increments the cache store counter.
func (c *Collector) IncrementCacheStores() {
	c.cacheStores.Inc(1)
}

// IncrementCacheEvictions increments the cache eviction counter.
func (c *Collector) IncrementCacheEvictions() {
	c.cacheEvictions.Inc(1)
}

// RecordCacheLookupLatency records a cache lookup latency in milliseconds.
func (c *Collector) RecordCacheLookupLatency(latencyMs int64) {
	c.cacheLookupLatency.Observe(float64(latencyMs))
}

// RecordCacheStoreLatency records a cache store latency in milliseconds.
func (c *Collector) RecordCacheStoreLatency(latencyMs int64) {
	c.cacheStoreLatency.Observe(float64(latencyMs))
}

// IncrementProviderRegistrations increments the provider registration counter.
func (c *Collector) IncrementProviderRegistrations() {
	c.providerRegistrations.Inc(1)
}

// IncrementProviderUnregistrations increments the provider unregistration counter.
func (c *Collector) IncrementProviderUnregistrations() {
	c.providerUnregistrations.Inc(1)
}

// IncrementProviderLookups increments the provider lookup counter.
func (c *Collector) IncrementProviderLookups() {
	c.providerLookups.Inc(1)
}

// RecordProviderLookupLatency records a provider lookup latency in milliseconds.
func (c *Collector) RecordProviderLookupLatency(latencyMs int64) {
	c.providerLookupLatency.Observe(float64(latencyMs))
}

// StartTime returns when the collector was created.
func (c *Collector) StartTime() time.Time {
	return c.startTime
}

// Snapshot returns a point-in-time snapshot of all metrics.
func (c *Collector) Snapshot() MetricSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snap := MetricSnapshot{
		RequestsTotal:     c.requestsTotal.Get(),
		ErrorsTotal:       c.errorsTotal.Get(),
		StreamsTotal:      c.streamsTotal.Get(),
		StreamErrorsTotal: c.streamErrorsTotal.Get(),
		ActiveStreams:     c.activeStreams.Get(),
		StreamStarted:     c.streamStarted.Get(),
		StreamCompleted:   c.streamCompleted.Get(),
		StreamCancelled:   c.streamCancelled.Get(),
		StreamTimeout:     c.streamTimeout.Get(),
		StreamChunksTotal: c.streamChunksTotal.Get(),
		StreamBytesTotal:  c.streamBytesTotal.Get(),
		StreamDurationMs: MetricHistogram{
			Sum:     c.streamDurationMs.Sum(),
			Count:   c.streamDurationMs.Count(),
			Min:     c.streamDurationMs.Min(),
			Max:     c.streamDurationMs.Max(),
			Buckets: c.streamDurationMs.Buckets(),
			Counts:  c.streamDurationMs.Counts(),
		},
		StreamChunks: MetricHistogram{
			Sum:     c.streamChunks.Sum(),
			Count:   c.streamChunks.Count(),
			Min:     c.streamChunks.Min(),
			Max:     c.streamChunks.Max(),
			Buckets: c.streamChunks.Buckets(),
			Counts:  c.streamChunks.Counts(),
		},
		StreamBytes: MetricHistogram{
			Sum:     c.streamBytes.Sum(),
			Count:   c.streamBytes.Count(),
			Min:     c.streamBytes.Min(),
			Max:     c.streamBytes.Max(),
			Buckets: c.streamBytes.Buckets(),
			Counts:  c.streamBytes.Counts(),
		},
		ProviderLatency: MetricHistogram{
			Sum:     c.providerLatency.Sum(),
			Count:   c.providerLatency.Count(),
			Min:     c.providerLatency.Min(),
			Max:     c.providerLatency.Max(),
			Buckets: c.providerLatency.Buckets(),
			Counts:  c.providerLatency.Counts(),
		},
		PromptTokensTotal:     c.promptTokensTotal.Get(),
		CompletionTokensTotal: c.completionTokensTotal.Get(),
		TotalTokensTotal:      c.totalTokensTotal.Get(),
		RetriesTotal:          c.retriesTotal.Get(),
		ProbesTotal:           c.probesTotal.Get(),
		ProbeSuccess:          c.probeSuccess.Get(),
		ProbeFailure:          c.probeFailure.Get(),
		BreakerRejections:     c.breakerRejections.Get(),
		BreakerOpens:          c.breakerOpens.Get(),
		UptimeSeconds:         time.Since(c.startTime).Seconds(),
		RoutingLatency: MetricHistogram{
			Sum:     c.routingLatency.Sum(),
			Count:   c.routingLatency.Count(),
			Min:     c.routingLatency.Min(),
			Max:     c.routingLatency.Max(),
			Buckets: c.routingLatency.Buckets(),
			Counts:  c.routingLatency.Counts(),
		},
		CacheHits:      c.cacheHits.Get(),
		CacheMisses:    c.cacheMisses.Get(),
		CacheStores:    c.cacheStores.Get(),
		CacheEvictions: c.cacheEvictions.Get(),
		CacheLookupLatency: MetricHistogram{
			Sum:     c.cacheLookupLatency.Sum(),
			Count:   c.cacheLookupLatency.Count(),
			Min:     c.cacheLookupLatency.Min(),
			Max:     c.cacheLookupLatency.Max(),
			Buckets: c.cacheLookupLatency.Buckets(),
			Counts:  c.cacheLookupLatency.Counts(),
		},
		CacheStoreLatency: MetricHistogram{
			Sum:     c.cacheStoreLatency.Sum(),
			Count:   c.cacheStoreLatency.Count(),
			Min:     c.cacheStoreLatency.Min(),
			Max:     c.cacheStoreLatency.Max(),
			Buckets: c.cacheStoreLatency.Buckets(),
			Counts:  c.cacheStoreLatency.Counts(),
		},
		ProviderRegistrations:   c.providerRegistrations.Get(),
		ProviderUnregistrations: c.providerUnregistrations.Get(),
		ProviderLookups:         c.providerLookups.Get(),
		ProviderLookupLatency: MetricHistogram{
			Sum:     c.providerLookupLatency.Sum(),
			Count:   c.providerLookupLatency.Count(),
			Min:     c.providerLookupLatency.Min(),
			Max:     c.providerLookupLatency.Max(),
			Buckets: c.providerLookupLatency.Buckets(),
			Counts:  c.providerLookupLatency.Counts(),
		},
	}

	// Copy per-provider latencies
	snap.ProviderLatencyByProvider = make(map[string]MetricHistogram, len(c.providerLatencyByProvider))
	for name, h := range c.providerLatencyByProvider {
		snap.ProviderLatencyByProvider[name] = MetricHistogram{
			Sum:     h.Sum(),
			Count:   h.Count(),
			Min:     h.Min(),
			Max:     h.Max(),
			Buckets: h.Buckets(),
			Counts:  h.Counts(),
		}
	}

	// Copy per-provider stream statistics
	snap.StreamStatsByProvider = make(map[string]ProviderStreamStats, len(c.streamStatsByProvider))
	for name, s := range c.streamStatsByProvider {
		snap.StreamStatsByProvider[name] = ProviderStreamStats{
			Started:   s.started.Get(),
			Completed: s.completed.Get(),
			Cancelled: s.cancelled.Get(),
			Timeout:   s.timeout.Get(),
			Errors:    s.errors.Get(),
			Chunks:    s.chunks.Get(),
			Bytes:     s.bytes.Get(),
			Duration: MetricHistogram{
				Sum:     s.duration.Sum(),
				Count:   s.duration.Count(),
				Min:     s.duration.Min(),
				Max:     s.duration.Max(),
				Buckets: s.duration.Buckets(),
				Counts:  s.duration.Counts(),
			},
			ChunksPerStream: MetricHistogram{
				Sum:     s.chunksPerStream.Sum(),
				Count:   s.chunksPerStream.Count(),
				Min:     s.chunksPerStream.Min(),
				Max:     s.chunksPerStream.Max(),
				Buckets: s.chunksPerStream.Buckets(),
				Counts:  s.chunksPerStream.Counts(),
			},
			BytesPerStream: MetricHistogram{
				Sum:     s.bytesPerStream.Sum(),
				Count:   s.bytesPerStream.Count(),
				Min:     s.bytesPerStream.Min(),
				Max:     s.bytesPerStream.Max(),
				Buckets: s.bytesPerStream.Buckets(),
				Counts:  s.bytesPerStream.Counts(),
			},
		}
	}

	return snap
}

// MetricSnapshot is a point-in-time snapshot of all metrics.
type MetricSnapshot struct {
	RequestsTotal             int64
	ErrorsTotal               int64
	StreamsTotal              int64
	StreamErrorsTotal         int64
	ActiveStreams             int64
	StreamStarted             int64
	StreamCompleted           int64
	StreamCancelled           int64
	StreamTimeout             int64
	StreamChunksTotal         int64
	StreamBytesTotal          int64
	StreamDurationMs          MetricHistogram
	StreamChunks              MetricHistogram
	StreamBytes               MetricHistogram
	ProviderLatency           MetricHistogram
	PromptTokensTotal         int64
	CompletionTokensTotal     int64
	TotalTokensTotal          int64
	RetriesTotal              int64
	ProbesTotal               int64
	ProbeSuccess              int64
	ProbeFailure              int64
	BreakerRejections         int64
	BreakerOpens              int64
	UptimeSeconds             float64
	RoutingLatency            MetricHistogram
	ProviderLatencyByProvider map[string]MetricHistogram
	StreamStatsByProvider     map[string]ProviderStreamStats
	CacheHits                 int64
	CacheMisses               int64
	CacheStores               int64
	CacheEvictions            int64
	CacheLookupLatency        MetricHistogram
	CacheStoreLatency         MetricHistogram
	ProviderRegistrations     int64
	ProviderUnregistrations   int64
	ProviderLookups           int64
	ProviderLookupLatency     MetricHistogram
}

// MetricHistogram is a histogram snapshot.
type MetricHistogram struct {
	Sum     float64
	Count   int64
	Min     float64
	Max     float64
	Buckets []float64
	Counts  []int64
}
