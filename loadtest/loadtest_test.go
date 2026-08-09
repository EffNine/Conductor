package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// LoadTestConfig holds the parameters for a load test run.
type LoadTestConfig struct {
	TargetURL     string
	APIKey        string
	Concurrency   int
	TotalRequests int
	RequestFunc   func() ([]byte, error)
	Timeout       time.Duration
}

// LoadTestResult holds the aggregate results from a load test run.
type LoadTestResult struct {
	TotalRequests   int
	Successful      int
	Failed          int
	TotalDuration   time.Duration
	AvgLatency      time.Duration
	MinLatency      time.Duration
	MaxLatency      time.Duration
	P50Latency      time.Duration
	P95Latency      time.Duration
	P99Latency      time.Duration
	RequestsPerSec  float64
	ErrorRate       float64
	GoroutinesAtEnd int
}

func runLoadTest(t testing.TB, cfg LoadTestConfig) (*LoadTestResult, error) {
	if cfg.TargetURL == "" {
		cfg.TargetURL = "http://127.0.0.1:8080"
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 100
	}
	if cfg.TotalRequests == 0 {
		cfg.TotalRequests = 1000
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("CONDUCTOR_API_KEY")
		if cfg.APIKey == "" {
			cfg.APIKey = "test-key"
		}
	}

	var (
		successes atomic.Int64
		failures  atomic.Int64
		totalMs   atomic.Int64
		minMs     atomic.Int64
		maxMs     atomic.Int64
	)
	minMs.Store(int64(1 << 62))
	maxMs.Store(0)

	startTime := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)

	latencies := make([]int64, 0, cfg.TotalRequests)
	var latMu sync.Mutex

	for i := 0; i < cfg.TotalRequests; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(reqIdx int) {
			defer wg.Done()
			defer func() { <-sem }()

			body, err := cfg.RequestFunc()
			if err != nil {
				failures.Add(1)
				return
			}

			reqStart := time.Now()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, cfg.TargetURL+"/v1/chat/completions", bytes.NewReader(body))
			if err != nil {
				failures.Add(1)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

			client := &http.Client{Timeout: cfg.Timeout}
			resp, err := client.Do(req)
			if err != nil {
				failures.Add(1)
				return
			}
			defer resp.Body.Close()

			_, _ = io.Copy(io.Discard, resp.Body)
			latency := time.Since(reqStart)
			latMs := latency.Milliseconds()

			if resp.StatusCode == http.StatusOK {
				successes.Add(1)
			} else {
				failures.Add(1)
			}

			totalMs.Add(latMs)
			for {
				cur := minMs.Load()
				if latMs < cur {
					if minMs.CompareAndSwap(cur, latMs) {
						break
					}
					continue
				}
				break
			}
			for {
				cur := maxMs.Load()
				if latMs > cur {
					if maxMs.CompareAndSwap(cur, latMs) {
						break
					}
					continue
				}
				break
			}

			latMu.Lock()
			latencies = append(latencies, latMs)
			latMu.Unlock()
		}(i)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	result := &LoadTestResult{
		TotalRequests:  cfg.TotalRequests,
		Successful:     int(successes.Load()),
		Failed:         int(failures.Load()),
		TotalDuration:  totalDuration,
		AvgLatency:     time.Duration(totalMs.Load()/int64(cfg.TotalRequests)) * time.Millisecond,
		MinLatency:     time.Duration(minMs.Load()) * time.Millisecond,
		MaxLatency:     time.Duration(maxMs.Load()) * time.Millisecond,
		RequestsPerSec: float64(cfg.TotalRequests) / totalDuration.Seconds(),
		ErrorRate:      float64(failures.Load()) / float64(cfg.TotalRequests),
	}

	if len(latencies) > 0 {
		result.P50Latency = time.Duration(percentile(latencies, 50)) * time.Millisecond
		result.P95Latency = time.Duration(percentile(latencies, 95)) * time.Millisecond
		result.P99Latency = time.Duration(percentile(latencies, 99)) * time.Millisecond
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	result.GoroutinesAtEnd = runtime.NumGoroutine()

	return result, nil
}

func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * float64(p) / 100.0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Report prints a formatted summary of load test results.
func Report(r *LoadTestResult) {
	fmt.Println("========================================")
	fmt.Println("LOAD TEST RESULTS")
	fmt.Println("========================================")
	fmt.Printf("Total Requests:   %d\n", r.TotalRequests)
	fmt.Printf("Successful:       %d (%.1f%%)\n", r.Successful, float64(r.Successful)/float64(r.TotalRequests)*100)
	fmt.Printf("Failed:           %d (%.1f%%)\n", r.Failed, r.ErrorRate*100)
	fmt.Printf("Total Duration:   %s\n", r.TotalDuration.Round(time.Millisecond))
	fmt.Printf("Requests/sec:     %.2f\n", r.RequestsPerSec)
	fmt.Printf("Avg Latency:      %s\n", r.AvgLatency.Round(time.Millisecond))
	fmt.Printf("Min Latency:      %s\n", r.MinLatency.Round(time.Millisecond))
	fmt.Printf("Max Latency:      %s\n", r.MaxLatency.Round(time.Millisecond))
	fmt.Printf("P50 Latency:      %s\n", r.P50Latency.Round(time.Millisecond))
	fmt.Printf("P95 Latency:      %s\n", r.P95Latency.Round(time.Millisecond))
	fmt.Printf("P99 Latency:      %s\n", r.P99Latency.Round(time.Millisecond))
	fmt.Printf("Goroutines at End: %d\n", r.GoroutinesAtEnd)
	fmt.Println("========================================")
}

// ---- Benchmark Package Helpers ----

// MakeChatRequest builds a JSON body for a chat completion request.
func MakeChatRequest(model, content string) ([]byte, error) {
	req := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": content},
		},
		"max_tokens": 64,
	}
	return json.Marshal(req)
}

// MakeStreamChatRequest builds a JSON body for a streaming chat completion request.
func MakeStreamChatRequest(model, content string) ([]byte, error) {
	req := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": content},
		},
		"stream":     true,
		"max_tokens": 64,
	}
	return json.Marshal(req)
}

// RunHTTPBenchmark runs a benchmark against a live server.
func RunHTTPBenchmark(t *testing.T, cfg LoadTestConfig) {
	if cfg.TargetURL == "" {
		cfg.TargetURL = "http://127.0.0.1:8080"
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("CONDUCTOR_API_KEY")
		if cfg.APIKey == "" {
			cfg.APIKey = "test-key"
		}
	}

	result, err := runLoadTest(t, cfg)
	if err != nil {
		t.Fatalf("load test failed: %v", err)
	}
	Report(result)
}

func BenchmarkHTTPChatCompletion(b *testing.B) {
	if os.Getenv("LOAD_TEST") != "1" {
		b.Skip("skip HTTP load test; set LOAD_TEST=1 to run")
	}
	cfg := LoadTestConfig{
		Concurrency:   100,
		TotalRequests: b.N,
		RequestFunc: func() ([]byte, error) {
			return MakeChatRequest("gpt-4o", "Hello, world!")
		},
	}
	_, err := runLoadTest(b, cfg)
	if err != nil {
		b.Fatal(err)
	}
}
