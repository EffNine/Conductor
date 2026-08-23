package health

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCatalogBatcherFlushAppliesPendingResults pins the Flush contract: after
// Flush returns, every result submitted before it has been applied via
// onBatch. The batch window is set far beyond the test duration so only Flush
// can trigger the apply — this is what lets probe passes mark providers
// filter-ready without racing the batch window (cold-start catalog flicker).
func TestCatalogBatcherFlushAppliesPendingResults(t *testing.T) {
	var mu sync.Mutex
	applied := 0
	b := NewCatalogBatcher(time.Hour, func(results []ProbeResult) {
		mu.Lock()
		applied += len(results)
		mu.Unlock()
	})
	b.Start()
	t.Cleanup(b.Stop)

	const n = 7
	for i := 0; i < n; i++ {
		b.Submit(ProbeResult{
			ModelID:  fmt.Sprintf("p/model-%d", i),
			Provider: "p",
			Success:  true,
		})
	}

	b.Flush()

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, n, applied, "Flush must synchronously drain all pending results")
}

// TestCatalogBatcherFlushWithoutStartIsNoop covers the not-running path:
// results submitted while the batcher is stopped are applied immediately by
// Submit itself, so a later Flush must be a harmless no-op.
func TestCatalogBatcherFlushWithoutStartIsNoop(t *testing.T) {
	var mu sync.Mutex
	applied := 0
	b := NewCatalogBatcher(50*time.Millisecond, func(results []ProbeResult) {
		mu.Lock()
		applied += len(results)
		mu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		b.Flush()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Flush on non-running batcher must return immediately")
	}
}
