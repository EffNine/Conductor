package cache

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/metrics"
	"go.uber.org/zap"
)

// Engine is the response cache integrated into the request pipeline.
type Engine struct {
	mu       sync.RWMutex
	cache    *LRUCache
	config   CacheConfig
	metrics  *metrics.Collector
	logger   *zap.Logger
	enabled  atomic.Bool
	stopCh   chan struct{}
	stopped  chan struct{}
}

// CacheConfig holds the runtime configuration for the response cache.
type CacheConfig struct {
	Enabled    bool
	TTL        time.Duration
	MaxEntries int
	Policy     EvictionPolicy
}

// NewEngine creates a new response cache engine.
func NewEngine(cfg config.CacheConfig, m *metrics.Collector, logger *zap.Logger) *Engine {
	e := &Engine{
		cache:   NewLRUCache(cfg.MaxEntries),
		config:  CacheConfig{Enabled: cfg.Enabled, TTL: cfg.TTL, MaxEntries: cfg.MaxEntries, Policy: parseEvictionPolicy(cfg.EvictionPolicy)},
		metrics: m,
		logger:  logger,
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}
	e.enabled.Store(cfg.Enabled)
	return e
}

func parseEvictionPolicy(s string) EvictionPolicy {
	switch s {
	case "lfu":
		return EvictionLFU
	case "fifo":
		return EvictionFIFO
	default:
		return EvictionLRU
	}
}

// Enable turns the cache on.
func (e *Engine) Enable() {
	e.enabled.Store(true)
}

// Disable turns the cache off.
func (e *Engine) Disable() {
	e.enabled.Store(false)
}

// IsEnabled reports whether the cache is active.
func (e *Engine) IsEnabled() bool {
	return e.enabled.Load()
}

// Get performs a cache lookup and records metrics.
func (e *Engine) Get(key string) ([]byte, bool) {
	if !e.enabled.Load() {
		return nil, false
	}
	start := time.Now()
	val, ok := e.cache.Get(key)
	latency := time.Since(start).Milliseconds()
	if e.metrics != nil {
		e.metrics.RecordCacheLookupLatency(latency)
		if ok {
			e.metrics.IncrementCacheHits()
		} else {
			e.metrics.IncrementCacheMisses()
		}
	}
	return val, ok
}

// Set stores a value in the cache and records metrics.
func (e *Engine) Set(key string, value []byte, ttl time.Duration) {
	if !e.enabled.Load() {
		return
	}
	start := time.Now()
	e.cache.Set(key, value, ttl)
	latency := time.Since(start).Milliseconds()
	if e.metrics != nil {
		e.metrics.RecordCacheStoreLatency(latency)
		e.metrics.IncrementCacheStores()
	}
}

// Stats returns current cache statistics.
func (e *Engine) Stats() Stats {
	return e.cache.Stats()
}

// Len returns the current number of entries.
func (e *Engine) Len() int {
	return e.cache.Len()
}

// Clear removes all entries.
func (e *Engine) Clear() {
	e.cache.Clear()
}

// KeyFromRequest builds a cache key for a chat completion request.
func KeyFromRequest(model string, messages []interface{}, params map[string]interface{}) string {
	return ResponseCacheKey(model, messages, params)
}

// RequestToParams converts a ChatCompletionRequest into the map used by the hash builder.
func RequestToParams(req interface{}) (map[string]interface{}, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var params map[string]interface{}
	if err := json.Unmarshal(data, &params); err != nil {
		return nil, err
	}
	return params, nil
}

// CacheResponse serializes a response and stores it.
func (e *Engine) CacheResponse(key string, resp interface{}) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	e.Set(key, data, e.config.TTL)
	return nil
}

// BuildCacheKey builds a cache key from a chat completion request.
func BuildCacheKey(model string, messages []interface{}, params map[string]interface{}) string {
	return ResponseCacheKey(model, messages, params)
}

// AverageLookupLatency returns the average cache lookup latency in milliseconds.
func (e *Engine) AverageLookupLatency() time.Duration {
	if e.metrics == nil {
		return 0
	}
	snap := e.metrics.Snapshot()
	if snap.CacheLookupLatency.Count == 0 {
		return 0
	}
	return time.Duration(int64(snap.CacheLookupLatency.Sum / float64(snap.CacheLookupLatency.Count)))
}
