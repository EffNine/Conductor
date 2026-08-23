package metrics

import (
	"bytes"
	"fmt"
	"io"
	"sort"
)

// ExportPrometheus exports all metrics in Prometheus exposition format.
func ExportPrometheus(snap MetricSnapshot) string {
	var b bytes.Buffer

	writeMetadata(&b, snap)
	writeGauges(&b, snap)
	writeCounters(&b, snap)
	writeHistograms(&b, snap)

	return b.String()
}

func writeMetadata(b *bytes.Buffer, snap MetricSnapshot) {
	b.WriteString("# HELP conductor_build_info Conductor gateway build information.\n")
	b.WriteString("# TYPE conductor_build_info gauge\n")
	b.WriteString("conductor_build_info{version=\"dev\"} 1\n\n")

	b.WriteString("# HELP conductor_uptime_seconds Seconds since the gateway started.\n")
	b.WriteString("# TYPE conductor_uptime_seconds gauge\n")
	b.WriteString(fmt.Sprintf("conductor_uptime_seconds %.3f\n\n", snap.UptimeSeconds))
}

func writeGauges(b *bytes.Buffer, snap MetricSnapshot) {
	b.WriteString("# HELP conductor_requests_total Total number of incoming requests.\n")
	b.WriteString("# TYPE conductor_requests_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_requests_total %d\n", snap.RequestsTotal))

	b.WriteString("# HELP conductor_errors_total Total number of errors (non-2xx responses).\n")
	b.WriteString("# TYPE conductor_errors_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_errors_total %d\n", snap.ErrorsTotal))

	b.WriteString("# HELP conductor_streams_total Total number of streaming requests.\n")
	b.WriteString("# TYPE conductor_streams_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_streams_total %d\n", snap.StreamsTotal))

	b.WriteString("# HELP conductor_stream_active Number of streams currently in flight.\n")
	b.WriteString("# TYPE conductor_stream_active gauge\n")
	b.WriteString(fmt.Sprintf("conductor_stream_active %d\n", snap.ActiveStreams))

	b.WriteString("# HELP conductor_stream_errors_total Total number of streaming errors.\n")
	b.WriteString("# TYPE conductor_stream_errors_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_stream_errors_total %d\n", snap.StreamErrorsTotal))

	b.WriteString("# HELP conductor_stream_started_total Total number of streams started.\n")
	b.WriteString("# TYPE conductor_stream_started_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_stream_started_total %d\n", snap.StreamStarted))

	b.WriteString("# HELP conductor_stream_completed_total Total number of streams completed successfully.\n")
	b.WriteString("# TYPE conductor_stream_completed_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_stream_completed_total %d\n", snap.StreamCompleted))

	b.WriteString("# HELP conductor_stream_cancelled_total Total number of streams cancelled (client disconnected or context cancelled).\n")
	b.WriteString("# TYPE conductor_stream_cancelled_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_stream_cancelled_total %d\n", snap.StreamCancelled))

	b.WriteString("# HELP conductor_stream_timeout_total Total number of streams that timed out.\n")
	b.WriteString("# TYPE conductor_stream_timeout_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_stream_timeout_total %d\n", snap.StreamTimeout))

	b.WriteString("# HELP conductor_stream_chunks_total Total number of chunks streamed to clients.\n")
	b.WriteString("# TYPE conductor_stream_chunks_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_stream_chunks_total %d\n", snap.StreamChunksTotal))

	b.WriteString("# HELP conductor_stream_bytes_total Total bytes written to streaming clients.\n")
	b.WriteString("# TYPE conductor_stream_bytes_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_stream_bytes_total %d\n", snap.StreamBytesTotal))

	b.WriteString("# HELP conductor_retries_total Total number of retries across all providers.\n")
	b.WriteString("# TYPE conductor_retries_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_retries_total %d\n", snap.RetriesTotal))

	b.WriteString("# HELP conductor_probes_total Total number of health probes executed.\n")
	b.WriteString("# TYPE conductor_probes_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_probes_total %d\n", snap.ProbesTotal))

	b.WriteString("# HELP conductor_probe_successes Total number of successful health probes.\n")
	b.WriteString("# TYPE conductor_probe_successes counter\n")
	b.WriteString(fmt.Sprintf("conductor_probe_successes %d\n", snap.ProbeSuccess))

	b.WriteString("# HELP conductor_probe_failures Total number of failed health probes.\n")
	b.WriteString("# TYPE conductor_probe_failures counter\n")
	b.WriteString(fmt.Sprintf("conductor_probe_failures %d\n", snap.ProbeFailure))

	b.WriteString("# HELP conductor_breaker_rejections_total Total number of requests rejected by circuit breakers.\n")
	b.WriteString("# TYPE conductor_breaker_rejections_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_breaker_rejections_total %d\n", snap.BreakerRejections))

	b.WriteString("# HELP conductor_breaker_opens_total Total number of times breakers opened.\n")
	b.WriteString("# TYPE conductor_breaker_opens_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_breaker_opens_total %d\n", snap.BreakerOpens))

	if len(snap.Fallbacks) > 0 {
		b.WriteString("\n# HELP conductor_fallback_total Requests served by a non-primary candidate, by fallback kind and serving provider.\n")
		b.WriteString("# TYPE conductor_fallback_total counter\n")
		for _, f := range snap.Fallbacks {
			b.WriteString(fmt.Sprintf("conductor_fallback_total{kind=%q,provider=%q} %d\n",
				f.Key.Kind, f.Key.Provider, f.Count))
		}
	}

	b.WriteString("# HELP conductor_cache_hits_total Total number of cache hits.\n")
	b.WriteString("# TYPE conductor_cache_hits_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_cache_hits_total %d\n", snap.CacheHits))

	b.WriteString("# HELP conductor_cache_misses_total Total number of cache misses.\n")
	b.WriteString("# TYPE conductor_cache_misses_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_cache_misses_total %d\n", snap.CacheMisses))

	b.WriteString("# HELP conductor_cache_stores_total Total number of cache stores.\n")
	b.WriteString("# TYPE conductor_cache_stores_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_cache_stores_total %d\n", snap.CacheStores))

	b.WriteString("# HELP conductor_cache_evictions_total Total number of cache evictions.\n")
	b.WriteString("# TYPE conductor_cache_evictions_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_cache_evictions_total %d\n\n", snap.CacheEvictions))

	b.WriteString("# HELP conductor_provider_registrations_total Total number of provider registrations.\n")
	b.WriteString("# TYPE conductor_provider_registrations_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_provider_registrations_total %d\n", snap.ProviderRegistrations))

	b.WriteString("# HELP conductor_provider_unregistrations_total Total number of provider unregistrations.\n")
	b.WriteString("# TYPE conductor_provider_unregistrations_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_provider_unregistrations_total %d\n", snap.ProviderUnregistrations))

	b.WriteString("# HELP conductor_provider_lookups_total Total number of provider registry lookups.\n")
	b.WriteString("# TYPE conductor_provider_lookups_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_provider_lookups_total %d\n", snap.ProviderLookups))

	b.WriteString("# HELP conductor_provider_lookup_latency_ms Provider registry lookup latency in milliseconds.\n")
	b.WriteString("# TYPE conductor_provider_lookup_latency_ms histogram\n")
	writeHistogram(b, "conductor_provider_lookup_latency_ms",
		"Provider registry lookup latency in milliseconds", snap.ProviderLookupLatency, nil)
}

func writeCounters(b *bytes.Buffer, snap MetricSnapshot) {
	b.WriteString("# HELP conductor_prompt_tokens_total Total prompt tokens consumed.\n")
	b.WriteString("# TYPE conductor_prompt_tokens_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_prompt_tokens_total %d\n", snap.PromptTokensTotal))

	b.WriteString("# HELP conductor_completion_tokens_total Total completion tokens produced.\n")
	b.WriteString("# TYPE conductor_completion_tokens_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_completion_tokens_total %d\n", snap.CompletionTokensTotal))

	b.WriteString("# HELP conductor_total_tokens_total Total tokens consumed.\n")
	b.WriteString("# TYPE conductor_total_tokens_total counter\n")
	b.WriteString(fmt.Sprintf("conductor_total_tokens_total %d\n\n", snap.TotalTokensTotal))
}

func writeHistograms(b *bytes.Buffer, snap MetricSnapshot) {
	writeHistogram(b, "conductor_provider_latency_ms",
		"Provider call latency in milliseconds", snap.ProviderLatency, nil)

	for name, h := range snap.ProviderLatencyByProvider {
		writeHistogram(b, "conductor_provider_latency_ms",
			"Provider call latency in milliseconds", h, map[string]string{"provider": name})
	}

	writeHistogram(b, "conductor_stream_duration_ms",
		"Stream duration in milliseconds", snap.StreamDurationMs, nil)

	writeHistogram(b, "conductor_stream_chunks",
		"Number of chunks per stream", snap.StreamChunks, nil)

	writeHistogram(b, "conductor_stream_bytes",
		"Number of bytes written per stream", snap.StreamBytes, nil)

	for name, s := range snap.StreamStatsByProvider {
		writeHistogram(b, "conductor_stream_duration_ms",
			"Stream duration in milliseconds", s.Duration, map[string]string{"provider": name})
		writeHistogram(b, "conductor_stream_chunks",
			"Number of chunks per stream", s.ChunksPerStream, map[string]string{"provider": name})
		writeHistogram(b, "conductor_stream_bytes",
			"Number of bytes written per stream", s.BytesPerStream, map[string]string{"provider": name})
	}

	writeHistogram(b, "conductor_routing_latency_ms",
		"Routing decision latency in milliseconds", snap.RoutingLatency, nil)

	writeHistogram(b, "conductor_cache_lookup_latency_ms",
		"Cache lookup latency in milliseconds", snap.CacheLookupLatency, nil)

	writeHistogram(b, "conductor_cache_store_latency_ms",
		"Cache store latency in milliseconds", snap.CacheStoreLatency, nil)
}

func writeHistogram(b *bytes.Buffer, metricName string, help string, h MetricHistogram, labels map[string]string) {
	// Base sum and count
	b.WriteString(fmt.Sprintf("# HELP %s %s.\n", metricName, help))
	b.WriteString(fmt.Sprintf("# TYPE %s histogram\n", metricName))

	// Write each bucket
	for i, count := range h.Counts {
		le := "+Inf"
		if i < len(h.Buckets) {
			le = fmt.Sprintf("%.3f", h.Buckets[i])
		}
		labelStr := formatLabels(labels)
		b.WriteString(fmt.Sprintf("%s_bucket{le=\"%s\"%s} %d\n",
			metricName, le, labelStr, count))
	}
	// +Inf bucket
	inflabelStr := formatLabels(labels)
	b.WriteString(fmt.Sprintf("%s_bucket{le=\"+Inf\"%s} %d\n",
		metricName, inflabelStr, h.Count))

	// Sum and count
	sumLabelStr := formatLabels(labels)
	b.WriteString(fmt.Sprintf("%s_sum%s %.3f\n", metricName, sumLabelStr, h.Sum))
	b.WriteString(fmt.Sprintf("%s_count%s %d\n\n", metricName, sumLabelStr, h.Count))
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteByte(',')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, " %s=\"%s\"", k, labels[k])
	}
	return buf.String()
}

// ReadBody reads the full body from r and returns it as a string.
func ReadBody(r io.Reader) string {
	if r == nil {
		return ""
	}
	b, _ := io.ReadAll(r)
	return string(b)
}
