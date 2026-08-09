package cache

// EvictionPolicy defines the strategy for removing entries when the cache is full.
type EvictionPolicy int

const (
	// EvictionLRU removes the least recently used entry.
	EvictionLRU EvictionPolicy = iota
	// EvictionLFU removes the least frequently used entry.
	EvictionLFU
	// EvictionFIFO removes the first inserted entry.
	EvictionFIFO
)

// Evictor defines the interface for cache eviction strategies.
type Evictor interface {
	// Evict is called when the cache needs to make room. Returns the key to remove.
	Evict(items map[string]*cacheEntry) string
	// Name returns the name of the eviction policy.
	Name() string
}

// cacheEntry holds a cache entry for eviction purposes.
type cacheEntry struct {
	key         string
	value       []byte
	expiresAt   int64 // unix nanoseconds, 0 = no expiry
	accessCount int64
	insertOrder int64
}

// LRUEvictor implements LRU eviction.
type LRUEvictor struct{}

func (e *LRUEvictor) Evict(items map[string]*cacheEntry) string {
	var oldestKey string
	var oldestTime int64 = -1
	for key, entry := range items {
		if entry.expiresAt > 0 && entry.expiresAt < oldestTime {
			oldestTime = entry.expiresAt
			oldestKey = key
			continue
		}
		if entry.accessCount > 0 && entry.accessCount < 1000000 {
			// Prefer entries with fewer accesses.
			if oldestKey == "" || entry.accessCount < items[oldestKey].accessCount {
				oldestKey = key
			}
		}
	}
	return oldestKey
}

func (e *LRUEvictor) Name() string { return "lru" }

// FIFOEvictor implements FIFO eviction.
type FIFOEvictor struct{}

func (e *FIFOEvictor) Evict(items map[string]*cacheEntry) string {
	var oldestKey string
	var oldestOrder int64 = -1
	for key, entry := range items {
		if oldestKey == "" || entry.insertOrder < oldestOrder {
			oldestOrder = entry.insertOrder
			oldestKey = key
		}
	}
	return oldestKey
}

func (e *FIFOEvictor) Name() string { return "fifo" }

// LFUEvictor implements LFU eviction.
type LFUEvictor struct{}

func (e *LFUEvictor) Evict(items map[string]*cacheEntry) string {
	var leastKey string
	var leastCount int64 = -1
	for key, entry := range items {
		if leastKey == "" || entry.accessCount < leastCount {
			leastCount = entry.accessCount
			leastKey = key
		}
	}
	return leastKey
}

func (e *LFUEvictor) Name() string { return "lfu" }

// NewEvictor creates an Evictor for the given policy name.
func NewEvictor(policy EvictionPolicy) Evictor {
	switch policy {
	case EvictionLFU:
		return &LFUEvictor{}
	case EvictionFIFO:
		return &FIFOEvictor{}
	default:
		return &LRUEvictor{}
	}
}
