package loadtest

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// PrintGoroutineProfile prints the current goroutine count for memory audit purposes.
func PrintGoroutineProfile() {
	fmt.Printf("Goroutines: %d\n", runtime.NumGoroutine())
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	fmt.Printf("HeapAlloc MB: %.2f\n", float64(m.HeapAlloc)/1024/1024)
	fmt.Printf("Sys MB:       %.2f\n", float64(m.Sys)/1024/1024)
	fmt.Printf("NumGC:        %d\n", m.NumGC)
}

// MemoryAuditResult holds memory audit data.
type MemoryAuditResult struct {
	InitialGoroutines int
	FinalGoroutines   int
	InitialHeapMB     float64
	FinalHeapMB       float64
	NumGC             uint32
	GoroutineLeaked   bool
}

// RunMemoryAudit runs a memory audit before and after a workload.
func RunMemoryAudit(name string, workload func(), iterations int) *MemoryAuditResult {
	result := &MemoryAuditResult{}
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	result.InitialGoroutines = runtime.NumGoroutine()
	result.InitialHeapMB = float64(ms.HeapAlloc) / 1024 / 1024

	for i := 0; i < iterations; i++ {
		workload()
	}

	runtime.GC()
	runtime.ReadMemStats(&ms)
	result.FinalGoroutines = runtime.NumGoroutine()
	result.FinalHeapMB = float64(ms.HeapAlloc) / 1024 / 1024
	result.NumGC = ms.NumGC
	if result.FinalGoroutines > result.InitialGoroutines+5 {
		result.GoroutineLeaked = true
	}

	fmt.Printf("=== Memory Audit: %s ===\n", name)
	fmt.Printf("  Goroutines: %d -> %d (delta=%d)\n", result.InitialGoroutines, result.FinalGoroutines, result.FinalGoroutines-result.InitialGoroutines)
	fmt.Printf("  Heap MB:    %.2f -> %.2f\n", result.InitialHeapMB, result.FinalHeapMB)
	fmt.Printf("  NumGC:      %d\n", result.NumGC)
	fmt.Printf("  Leaked:     %v\n", result.GoroutineLeaked)
	return result
}

func BenchmarkMemoryAudit(b *testing.B) {
	result := RunMemoryAudit("benchmark_workload", func() {
		for i := 0; i < 100; i++ {
			_, _ = MakeChatRequest("gpt-4o", "hello")
		}
	}, b.N)
	if result.GoroutineLeaked {
		b.Error("goroutine leak detected")
	}
	_ = result
}

// BenchmarkGoroutineLeakDetection runs a stress test to detect goroutine leaks.
func BenchmarkGoroutineLeakDetection(b *testing.B) {
	initial := runtime.NumGoroutine()
	for i := 0; i < b.N; i++ {
		var ms runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms)
		_ = ms.HeapAlloc
	}
	// Allow goroutines to settle
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	final := runtime.NumGoroutine()
	if final > initial+10 {
		b.Logf("potential goroutine leak: %d -> %d", initial, final)
	}
}
