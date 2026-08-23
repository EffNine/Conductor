package metrics

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCounterIncrement(t *testing.T) {
	c := &Counter{}
	c.Inc(1)
	c.Inc(1)
	c.Inc(3)
	assert.Equal(t, int64(5), c.Get())
}

func TestHistogramObserve(t *testing.T) {
	h := NewHistogram([]float64{10, 20, 30})
	h.Observe(5)  // <=10, <=20, <=30
	h.Observe(15) // <=20, <=30
	h.Observe(25) // <=30
	h.Observe(50) // none

	assert.Equal(t, int64(4), h.Count())
	assert.Equal(t, float64(95), h.Sum())
	assert.Equal(t, 5.0, h.Min())
	assert.Equal(t, 50.0, h.Max())

	// Cumulative bucket counts: 5<=10, 15<=20, 25<=30
	assert.Equal(t, []int64{1, 2, 3}, h.Counts())
}

func TestCollectorSnapshot(t *testing.T) {
	c := NewCollector()
	c.IncrementRequests()
	c.IncrementRequests()
	c.IncrementErrors()
	c.IncrementStreams()
	c.IncrementRetries()
	c.RecordPromptTokens(100)
	c.RecordCompletionTokens(50)
	c.RecordTotalTokens(150)
	c.RecordProviderLatency(42)
	c.RecordProviderLatencyForProvider("openai", 100)
	c.IncrementProbes()
	c.IncrementProbeSuccess()

	snap := c.Snapshot()
	assert.Equal(t, int64(2), snap.RequestsTotal)
	assert.Equal(t, int64(1), snap.ErrorsTotal)
	assert.Equal(t, int64(1), snap.StreamsTotal)
	assert.Equal(t, int64(1), snap.RetriesTotal)
	assert.Equal(t, int64(100), snap.PromptTokensTotal)
	assert.Equal(t, int64(50), snap.CompletionTokensTotal)
	assert.Equal(t, int64(150), snap.TotalTokensTotal)
	assert.Equal(t, int64(1), snap.ProbesTotal)
	assert.Equal(t, int64(1), snap.ProbeSuccess)
	assert.Equal(t, int64(0), snap.ProbeFailure)
	// Provider latency: 42 (global only) + 100 (openai separate histogram)
	assert.Equal(t, int64(1), snap.ProviderLatency.Count)
	assert.NotNil(t, snap.ProviderLatencyByProvider["openai"])
	assert.Equal(t, int64(1), snap.ProviderLatencyByProvider["openai"].Count)
	// Uptime is a float, just check it's positive
	assert.Greater(t, snap.UptimeSeconds, float64(0))
}

func TestExportPrometheus(t *testing.T) {
	c := NewCollector()
	c.IncrementRequests()
	c.IncrementErrors()
	c.RecordProviderLatency(50)
	c.RecordPromptTokens(10)
	c.RecordCompletionTokens(5)

	// Wait a bit so uptime > 0
	time.Sleep(10 * time.Millisecond)

	snap := c.Snapshot()
	output := ExportPrometheus(snap)

	assert.Contains(t, output, "conductor_requests_total 1")
	assert.Contains(t, output, "conductor_errors_total 1")
	assert.Contains(t, output, "conductor_prompt_tokens_total 10")
	assert.Contains(t, output, "conductor_completion_tokens_total 5")
	assert.Contains(t, output, "conductor_provider_latency_ms_sum")
	assert.Contains(t, output, "conductor_provider_latency_ms_count")
	assert.Contains(t, output, "conductor_build_info")
	assert.Contains(t, output, "conductor_uptime_seconds")
}

func TestExportPrometheusWithProviderLabel(t *testing.T) {
	c := NewCollector()
	c.RecordProviderLatencyForProvider("openai", 100)
	c.RecordProviderLatencyForProvider("anthropic", 200)

	snap := c.Snapshot()
	output := ExportPrometheus(snap)

	// Check that provider-specific buckets have labels
	assert.True(t, strings.Contains(output, "provider=\"openai\""))
	assert.True(t, strings.Contains(output, "provider=\"anthropic\""))
}

func TestFallbackCountersAndExport(t *testing.T) {
	c := NewCollector()
	c.IncrementFallback("dynamic", "groq")
	c.IncrementFallback("dynamic", "groq")
	c.IncrementFallback("static", "deepseek")

	snap := c.Snapshot()
	require.Len(t, snap.Fallbacks, 2)
	assert.Equal(t, FallbackKey{Kind: "dynamic", Provider: "groq"}, snap.Fallbacks[0].Key)
	assert.Equal(t, int64(2), snap.Fallbacks[0].Count)
	assert.Equal(t, FallbackKey{Kind: "static", Provider: "deepseek"}, snap.Fallbacks[1].Key)
	assert.Equal(t, int64(1), snap.Fallbacks[1].Count)

	output := ExportPrometheus(snap)
	assert.Contains(t, output, "conductor_fallback_total{kind=\"dynamic\",provider=\"groq\"} 2")
	assert.Contains(t, output, "conductor_fallback_total{kind=\"static\",provider=\"deepseek\"} 1")
}

func TestReadBody(t *testing.T) {
	r := strings.NewReader("hello world")
	body := ReadBody(r)
	assert.Equal(t, "hello world", body)

	assert.Equal(t, "", ReadBody(nil))
}

func TestHistogramEmpty(t *testing.T) {
	h := NewHistogram([]float64{10, 20})
	assert.Equal(t, int64(0), h.Count())
	assert.Equal(t, 0.0, h.Sum())
	assert.Equal(t, 0.0, h.Min())
	assert.Equal(t, 0.0, h.Max())
}

func TestCollectorConcurrentAccess(t *testing.T) {
	c := NewCollector()

	// Run concurrently to test thread safety
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				c.IncrementRequests()
				c.IncrementErrors()
				c.RecordProviderLatency(10)
				c.RecordPromptTokens(1)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	snap := c.Snapshot()
	assert.Equal(t, int64(1000), snap.RequestsTotal)
	assert.Equal(t, int64(1000), snap.ErrorsTotal)
	assert.Equal(t, int64(1000), snap.PromptTokensTotal)
	assert.Equal(t, int64(1000), snap.ProviderLatency.Count)
}

func TestNewHistogramDefaultBuckets(t *testing.T) {
	h := NewHistogram(nil)
	require.NotNil(t, h)
	assert.Len(t, h.Buckets(), 10)
}

func TestStreamLifecycleMetrics(t *testing.T) {
	c := NewCollector()

	c.RecordStreamStarted("openai")
	c.IncrementActiveStreams()
	c.IncrementActiveStreams()
	assert.Equal(t, int64(2), c.ActiveStreams())

	c.RecordStreamChunk(5)
	c.RecordStreamBytes(1000)
	c.RecordStreamOutcome("openai", StreamCompleted, 5, 1000, 120)
	c.DecrementActiveStreams()
	c.DecrementActiveStreams()
	assert.Equal(t, int64(0), c.ActiveStreams())

	snap := c.Snapshot()
	assert.Equal(t, int64(1), snap.StreamStarted)
	assert.Equal(t, int64(1), snap.StreamCompleted)
	assert.Equal(t, int64(0), snap.StreamCancelled)
	assert.Equal(t, int64(0), snap.StreamTimeout)
	assert.Equal(t, int64(0), snap.ActiveStreams)
	assert.Equal(t, int64(5), snap.StreamChunksTotal)
	assert.Equal(t, int64(1000), snap.StreamBytesTotal)
	assert.Equal(t, int64(1), snap.StreamDurationMs.Count)
	assert.Equal(t, float64(120), snap.StreamDurationMs.Sum)

	// Per-provider stats
	ps, ok := snap.StreamStatsByProvider["openai"]
	require.True(t, ok)
	assert.Equal(t, int64(1), ps.Started)
	assert.Equal(t, int64(1), ps.Completed)
	assert.Equal(t, int64(5), ps.Chunks)
	assert.Equal(t, int64(1000), ps.Bytes)
	assert.Equal(t, int64(1), ps.Duration.Count)
	assert.Equal(t, float64(5), ps.ChunksPerStream.Sum)
	assert.Equal(t, float64(1000), ps.BytesPerStream.Sum)
}

func TestStreamOutcomesMapToCounters(t *testing.T) {
	c := NewCollector()
	c.RecordStreamChunk(1)
	c.RecordStreamBytes(10)
	c.RecordStreamOutcome("a", StreamCompleted, 1, 10, 5)
	c.RecordStreamChunk(2)
	c.RecordStreamBytes(20)
	c.RecordStreamOutcome("a", StreamCancelled, 2, 20, 5)
	c.RecordStreamChunk(3)
	c.RecordStreamBytes(30)
	c.RecordStreamOutcome("a", StreamTimeout, 3, 30, 5)
	c.RecordStreamChunk(4)
	c.RecordStreamBytes(40)
	c.RecordStreamOutcome("a", StreamError, 4, 40, 5)

	snap := c.Snapshot()
	assert.Equal(t, int64(1), snap.StreamCompleted)
	assert.Equal(t, int64(1), snap.StreamCancelled)
	assert.Equal(t, int64(1), snap.StreamTimeout)
	assert.Equal(t, int64(1), snap.StreamErrorsTotal)
	assert.Equal(t, int64(4), snap.StreamDurationMs.Count)

	ps := snap.StreamStatsByProvider["a"]
	assert.Equal(t, int64(1), ps.Completed)
	assert.Equal(t, int64(1), ps.Cancelled)
	assert.Equal(t, int64(1), ps.Timeout)
	assert.Equal(t, int64(1), ps.Errors)
}

func TestExportPrometheusStreamMetrics(t *testing.T) {
	c := NewCollector()
	c.RecordStreamStarted("openai")
	c.IncrementActiveStreams()
	c.RecordStreamChunk(3)
	c.RecordStreamBytes(512)
	c.RecordStreamOutcome("openai", StreamCompleted, 3, 512, 200)
	c.DecrementActiveStreams()

	snap := c.Snapshot()
	output := ExportPrometheus(snap)

	assert.Contains(t, output, "conductor_stream_active 0")
	assert.Contains(t, output, "conductor_stream_started_total 1")
	assert.Contains(t, output, "conductor_stream_completed_total 1")
	assert.Contains(t, output, "conductor_stream_cancelled_total 0")
	assert.Contains(t, output, "conductor_stream_timeout_total 0")
	assert.Contains(t, output, "conductor_stream_chunks_total 3")
	assert.Contains(t, output, "conductor_stream_bytes_total 512")
	assert.Contains(t, output, "conductor_stream_duration_ms_sum")
	assert.Contains(t, output, "conductor_stream_duration_ms_count")
	// Per-provider labelled histogram
	assert.Contains(t, output, "provider=\"openai\"")
}

func TestCollectorConcurrentStreamRecording(t *testing.T) {
	c := NewCollector()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.RecordStreamStarted("openai")
				c.IncrementActiveStreams()
				c.RecordStreamChunk(2)
				c.RecordStreamBytes(100)
				c.RecordStreamOutcome("openai", StreamCompleted, 2, 100, 10)
				c.DecrementActiveStreams()
			}
		}()
	}
	wg.Wait()

	snap := c.Snapshot()
	assert.Equal(t, int64(800), snap.StreamStarted)
	assert.Equal(t, int64(800), snap.StreamCompleted)
	assert.Equal(t, int64(0), snap.ActiveStreams)
	assert.Equal(t, int64(1600), snap.StreamChunksTotal)
	ps := snap.StreamStatsByProvider["openai"]
	assert.Equal(t, int64(800), ps.Started)
	assert.Equal(t, int64(800), ps.Completed)
}
