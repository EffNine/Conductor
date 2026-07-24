package health

import (
	"context"
	"sync"
	"time"
)

// ProbeResult is one probe or live-update outcome pending atomic catalog apply.
type ProbeResult struct {
	ModelID         string
	Provider        string
	ProviderModelID string
	Success         bool
	LatencyMs       int64
	ErrMsg          string
	StatusCode      int
	// Skip means neutral/inconclusive — do not change reachability.
	Skip bool
	// FromLive marks results from real traffic (still applied via batcher when used).
	FromLive bool
}

// CatalogBatcher collects probe results and applies them as atomic batches so
// /v1/models readers never observe a mid-update partial catalog.
type CatalogBatcher struct {
	resultsCh   chan ProbeResult
	batchWindow time.Duration
	onBatch     func([]ProbeResult)
	stopCh      chan struct{}
	wg          sync.WaitGroup
	once        sync.Once
	mu          sync.Mutex
	running     bool
}

// NewCatalogBatcher creates a batcher. onBatch is invoked with the flushed slice
// (caller must not retain the slice). batchWindow defaults to 100ms.
func NewCatalogBatcher(batchWindow time.Duration, onBatch func([]ProbeResult)) *CatalogBatcher {
	if batchWindow <= 0 {
		batchWindow = 100 * time.Millisecond
	}
	if onBatch == nil {
		onBatch = func([]ProbeResult) {}
	}
	return &CatalogBatcher{
		resultsCh:   make(chan ProbeResult, 256),
		batchWindow: batchWindow,
		onBatch:     onBatch,
		stopCh:      make(chan struct{}),
	}
}

// Start runs the batch loop until Stop.
func (b *CatalogBatcher) Start() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return
	}
	b.running = true
	b.mu.Unlock()
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.run(context.Background())
	}()
}

// Stop flushes any pending results and stops the loop.
func (b *CatalogBatcher) Stop() {
	if b == nil {
		return
	}
	b.once.Do(func() { close(b.stopCh) })
	b.wg.Wait()
	b.mu.Lock()
	b.running = false
	b.mu.Unlock()
}

// Submit enqueues a probe result. When the batcher is not running (e.g. unit
// tests calling ProbeModel directly), results are applied immediately.
// If the channel is full, the result is applied as a single-item batch.
func (b *CatalogBatcher) Submit(result ProbeResult) {
	if b == nil {
		return
	}
	b.mu.Lock()
	running := b.running
	b.mu.Unlock()
	if !running {
		b.onBatch([]ProbeResult{result})
		return
	}
	select {
	case b.resultsCh <- result:
	default:
		// Channel full — apply immediately so we never drop health signals.
		b.onBatch([]ProbeResult{result})
	}
}

func (b *CatalogBatcher) run(ctx context.Context) {
	ticker := time.NewTicker(b.batchWindow)
	defer ticker.Stop()

	batch := make([]ProbeResult, 0, 32)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		b.onBatch(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case <-b.stopCh:
			// Drain remaining
			for {
				select {
				case r := <-b.resultsCh:
					batch = append(batch, r)
				default:
					flush()
					return
				}
			}
		case r := <-b.resultsCh:
			batch = append(batch, r)
		case <-ticker.C:
			flush()
		}
	}
}
