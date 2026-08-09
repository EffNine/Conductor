# Performance & Production Readiness — Conductor RC1

This document covers benchmark methodology, profiling, memory auditing, and load testing for the Conductor AI gateway.

## Hardware & Environment

All benchmarks were run on:
- **CPU:** Apple M2 (8 cores)
- **Go:** 1.26.5
- **OS:** macOS (darwin/arm64)

Results will vary by hardware. Use the commands below on your target platform to get accurate baselines.

## Benchmark Suite

### Running Benchmarks

```bash
# Run all benchmarks with allocation reporting
go test -bench=. -benchmem ./benchmarks/

# Run a specific benchmark
go test -bench=BenchmarkRouterResolve -benchmem ./benchmarks/

# Run with race detector (slower but catches data races in benchmarks)
go test -race -bench=. -benchmem ./benchmarks/

# Run only fast benchmarks (skip slow ones like cache key with large messages)
go test -bench='^(BenchmarkRouter|BenchmarkRegistry|BenchmarkBreaker|BenchmarkMetrics|BenchmarkLRU)' -benchmem ./benchmarks/
```

### Benchmark Categories

| Category | Benchmarks | What It Measures |
|----------|-----------|-----------------|
| **Routing** | `BenchmarkRouterResolve*` | Router lookup latency (ns/op), allocations |
| **Registry** | `BenchmarkRegistry*` | Provider lookup, iteration, capability matching |
| **Cache** | `BenchmarkCache*`, `BenchmarkBuildCacheKey*` | Cache hit/miss, key construction, store latency |
| **Circuit Breaker** | `BenchmarkBreaker*` | Allow/record/stats overhead |
| **Metrics** | `BenchmarkMetrics*` | Snapshot, counter, histogram overhead |
| **Routing Engine** | `BenchmarkRouterEngine*` | Intelligent routing scoring latency |
| **LRU Cache** | `BenchmarkLRUCache*` | Low-level LRU operations |
| **Discovery** | `BenchmarkDiscovery*` | Provider discovery by capability/model |
| **Concurrency** | `Benchmark*Concurrent*` | Lock contention under parallel access |
| **Allocations** | `Benchmark*Allocations` | Memory allocation profiles |

### Key Benchmark Results (Reference)

```
BenchmarkRouterResolve-8                              ~142 ns/op   160 B/op   2 allocs/op
BenchmarkRegistryGet-8                                ~14 ns/op     0 B/op   0 allocs/op
BenchmarkBreakerAllow-8                               ~11 ns/op     0 B/op   0 allocs/op
BenchmarkCacheGetHit-8                                ~120 ns/op    0 B/op   0 allocs/op
BenchmarkCacheBuildKey-8                              ~1069 ns/op  816 B/op  21 allocs/op
BenchmarkRouterEngineSelectBestProvider-8             ~1947 ns/op 3496 B/op  31 allocs/op
```

### Interpreting Results

- **ns/op**: Nanoseconds per operation. Lower is better.
- **B/op**: Bytes allocated per operation. Zero allocations are ideal for hot paths.
- **allocs/op**: Number of heap allocations per operation. Fewer is better; 0 is optimal.

**Hot path targets** (router resolve, registry get, breaker allow) should stay under 200 ns/op with ≤2 allocations.

## Load Testing

### Running Load Tests

Load tests require a running Conductor instance:

```bash
# Start Conductor locally
make run

# Run load tests against it
LOAD_TEST=1 go test -v -run=^$ -bench=. -benchtime=5s ./loadtest/
```

### Load Test Scenarios

| Scenario | Concurrency | Requests | Purpose |
|----------|------------|----------|---------|
| Light | 10 | 100 | Baseline sanity check |
| Medium | 100 | 1000 | Typical production load |
| Heavy | 500 | 5000 | Stress testing |
| Extreme | 1000 | 10000 | Capacity limits |

### Metrics Collected

- **Success rate**: % of requests returning 200 OK
- **Latency distribution**: P50, P95, P99, min, max
- **Throughput**: Requests per second
- **Goroutine count**: At end of test (leak detection)
- **Error rate**: % of failed requests

### Manual Load Test with wrk

```bash
# Install wrk: brew install wrk
wrk -t12 -c100 -d30s -s scripts/wrk_chat.lua \
    --header "Authorization: Bearer $CONDUCTOR_API_KEY" \
    http://127.0.0.1:8080/v1/chat/completions
```

## Profiling

### CPU Profile

```bash
# Start the server with pprof enabled
go run ./cmd/conductor/main.go &

# In another terminal, capture CPU profile for 30s
curl -s http://localhost:8080/debug/pprof/profile?seconds=30 \
    -H "Authorization: Bearer $CONDUCTOR_API_KEY" \
    -o cpu.prof

# Visualize
go tool pprof cpu.prof

# Or with web UI (requires graphviz)
go tool pprof -http=:8081 cpu.prof
```

### Memory Profile

```bash
# Heap profile (allocations since last GC)
curl -s http://localhost:8080/debug/pprof/heap \
    -H "Authorization: Bearer $CONDUCTOR_API_KEY" \
    -o heap.prof

# In-use objects
curl -s http://localhost:8080/debug/pprof/heap?allocs \
    -H "Authorization: Bearer $CONDUCTOR_API_KEY" \
    -o heap_allocs.prof

# Analyze
go tool pprof heap.prof
```

### Mutex Profile (contention)

```bash
# Enable mutex profiling (5% of locks sampled)
# Set CONDUCTOR_ENABLE_MUTEX_PROFILE=1 before starting
curl -s http://localhost:8080/debug/pprof/mutex \
    -H "Authorization: Bearer $CONDUCTOR_API_KEY" \
    -o mutex.prof

go tool pprof mutex.prof
```

### Block Profile (goroutine blocking)

```bash
# Enable block profiling
# Set CONDUCTOR_ENABLE_BLOCK_PROFILE=1 before starting
curl -s http://localhost:8080/debug/pprof/block \
    -H "Authorization: Bearer $CONDUCTOR_API_KEY" \
    -o block.prof

go tool pprof block.prof
```

### Trace

```bash
# 5-second execution trace
curl -s "http://localhost:8080/debug/pprof/trace?seconds=5" \
    -H "Authorization: Bearer $CONDUCTOR_API_KEY" \
    -o trace.out

# Visualize with Go tools
go tool trace trace.out
```

### Goroutine Dump

```bash
# Get current goroutine stack traces
curl -s http://localhost:8080/debug/pprof/goroutine \
    -H "Authorization: Bearer $CONDUCTOR_API_KEY" \
    -o goroutines.prof

go tool pprof goroutines.prof
```

### Programmatic Profiling

```go
import (
    "runtime/pprof"
    "os"
)

// CPU profile
f, _ := os.Create("cpu.prof")
pprof.StartCPUProfile(f)
defer pprof.StopCPUProfile()

// Memory profile
f, _ := os.Create("heap.prof")
pprof.WriteHeapProfile(f)
```

## Memory Audit

### Checking for Leaks

```bash
# Run benchmarks with race detector and memory profiling
go test -race -memprofile=mem.prof -bench=. ./benchmarks/

# Check goroutine count before/after
go tool pprof -top mem.prof
```

### Sustained Load Memory Test

```bash
# Run a sustained load test and monitor memory
for i in $(seq 1 10); do
    LOAD_TEST=1 go test -run=^$ -bench=BenchmarkMain -benchtime=1s ./benchmarks/ 2>&1 | grep HeapAlloc
    sleep 1
done
```

### Memory Audit Checklist

- [ ] No goroutine count growth after sustained load
- [ ] HeapAlloc stabilizes (doesn't grow unboundedly)
- [ ] NumGC count increases normally (GC is working)
- [ ] No timer leaks (check `runtime.Timer` in pprof)
- [ ] No channel leaks (check blocking goroutines in profile)
- [ ] Context cleanup on stream termination

## Performance Report Template

```
=== Conductor Performance Report ===
Date: YYYY-MM-DD
Hardware: <CPU, cores>
Go version: <version>

--- Benchmark Results ---
<Benchmark>                <ops>    <ns/op>   <B/op>  <allocs/op>
...

--- Load Test Results ---
Concurrency: <N>
Total Requests: <N>
Success Rate: <X.X>%
Avg Latency: <Xms>
P50 Latency: <Xms>
P95 Latency: <Xms>
P99 Latency: <Xms>
Throughput: <X req/s>

--- Memory Audit ---
Initial Goroutines: <N>
Final Goroutines: <N>
Initial Heap MB: <X.XX>
Final Heap MB: <X.XX>
GC Cycles: <N>
Leaked: <yes/no>

--- GC Stats ---
<go tool pprof -text heap.prof>
```

## Optimization Guidelines

### When to Investigate

1. **Hot path > 200 ns/op** with allocations → investigate inlining / escape analysis
2. **Goroutine growth** under load → check for leaked channels/goroutines
3. **High mutex contention** → consider lock-free structures or sharding
4. **Memory growth** without bound → check for unbounded caches/maps

### Known Optimizations Already Applied

- Registry lookups use `sync.RWMutex` with read-heavy optimization (0 allocs)
- Circuit breaker uses `sync.Mutex` with atomic counters for stats (0 allocs)
- Cache uses `container/list` for O(1) LRU operations
- Metrics use lock-free atomics for counters, mutex-protected histograms
- Router uses RWMutex for config reads during resolution

## Makefile Targets

```bash
# Run all benchmarks
make benchmarks

# Run benchmarks with verbose output
make benchmarks-verbose

# Run specific benchmark
make benchmark BENCH=BenchmarkRouterResolve

# Run load test
make load-test

# Run load test with specific concurrency
make load-test CONCURRENCY=100 REQUESTS=1000

# Generate CPU profile during benchmark
make benchmark-cpu

# Generate memory profile during benchmark
make benchmark-mem
```

## Continuous Performance Monitoring

Add performance benchmarks to CI:

```yaml
# .github/workflows/performance.yaml
- name: Run Benchmarks
  run: go test -bench=. -benchmem ./benchmarks/
- name: Check Regression
  run: |
    go test -bench=BenchmarkRouterResolve -benchmem ./benchmarks/ > baseline.txt
    # Compare against stored baseline
```
