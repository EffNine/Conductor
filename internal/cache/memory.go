package cache

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"
)

// LRUCache is a thread-safe in-memory LRU cache with TTL support.
type LRUCache struct {
	mu      sync.Mutex
	maxSize int
	items   map[string]*list.Element
	lru     *list.List
	stats   Stats
}

type cacheItem struct {
	key         string
	value       []byte
	expiresAt   time.Time
	accessCount int64
	lastAccess  time.Time
}

// NewLRUCache creates a new LRU cache with the given max size.
func NewLRUCache(maxSize int) *LRUCache {
	if maxSize <= 0 {
		maxSize = 1024
	}
	return &LRUCache{
		maxSize: maxSize,
		items:   make(map[string]*list.Element, maxSize),
		lru:     list.New(),
	}
}

// Get retrieves a value by key. Returns nil and false if not found or expired.
func (c *LRUCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats.GetOperations++

	elem, ok := c.items[key]
	if !ok {
		c.stats.Misses++
		return nil, false
	}

	item := elem.Value.(*cacheItem)
	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		// Expired — remove it.
		c.removeElement(elem)
		c.stats.Expirations++
		c.stats.Misses++
		return nil, false
	}

	// Move to front (most recently used).
	c.lru.MoveToFront(elem)
	item.accessCount++
	c.stats.Hits++

	return item.value, true
}

// Set stores a value with the given TTL.
func (c *LRUCache) Set(key string, value []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats.SetOperations++

	expiresAt := time.Time{}
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	// Update existing key.
	if elem, ok := c.items[key]; ok {
		item := elem.Value.(*cacheItem)
		item.value = value
		item.expiresAt = expiresAt
		item.accessCount++
		item.lastAccess = time.Now()
		c.lru.MoveToFront(elem)
		return
	}

	// Evict if at capacity.
	for c.lru.Len() >= c.maxSize {
		c.evictOldest()
	}

	// Insert new item at front.
	item := &cacheItem{
		key:         key,
		value:       value,
		expiresAt:   expiresAt,
		accessCount: 1,
		lastAccess:  time.Now(),
	}
	elem := c.lru.PushFront(item)
	c.items[key] = elem
}

// Delete removes a key from the cache.
func (c *LRUCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats.DeleteOperations++

	elem, ok := c.items[key]
	if !ok {
		return
	}
	c.removeElement(elem)
}

// Clear removes all entries.
func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats.ClearOperations++
	c.items = make(map[string]*list.Element, c.maxSize)
	c.lru.Init()
}

// Stats returns current cache statistics.
func (c *LRUCache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// Len returns the current number of entries.
func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

func (c *LRUCache) removeElement(elem *list.Element) {
	item := c.lru.Remove(elem).(*cacheItem)
	delete(c.items, item.key)
}

func (c *LRUCache) evictOldest() {
	elem := c.lru.Back()
	if elem == nil {
		return
	}
	c.removeElement(elem)
	c.stats.Evictions++
}

// AtomicCache wraps LRUCache with atomic stats for safe concurrent access.
type AtomicCache struct {
	*LRUCache
	hits   atomic.Int64
	misses atomic.Int64
}

// NewAtomicCache creates a cache with atomic statistics tracking.
func NewAtomicCache(maxSize int) *AtomicCache {
	return &AtomicCache{
		LRUCache: NewLRUCache(maxSize),
	}
}

// GetWithAtomic is like Get but updates atomic stats.
func (c *AtomicCache) GetWithAtomic(key string) ([]byte, bool) {
	val, ok := c.LRUCache.Get(key)
	if ok {
		c.hits.Add(1)
	} else {
		c.misses.Add(1)
	}
	return val, ok
}

// GetHits returns the atomic hit count.
func (c *AtomicCache) GetHits() int64 {
	return c.hits.Load()
}

// GetMisses returns the atomic miss count.
func (c *AtomicCache) GetMisses() int64 {
	return c.misses.Load()
}
