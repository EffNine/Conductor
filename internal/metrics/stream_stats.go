package metrics

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
