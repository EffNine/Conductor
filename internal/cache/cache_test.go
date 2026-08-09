package cache_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLRUCacheBasicSetGet(t *testing.T) {
	c := cache.NewLRUCache(10)
	c.Set("key1", []byte("value1"), 5*time.Minute)

	val, ok := c.Get("key1")
	require.True(t, ok)
	assert.Equal(t, []byte("value1"), val)
}

func TestLRUCacheMiss(t *testing.T) {
	c := cache.NewLRUCache(10)
	_, ok := c.Get("missing")
	assert.False(t, ok)
}

func TestLRUCacheOverwrite(t *testing.T) {
	c := cache.NewLRUCache(10)
	c.Set("key1", []byte("value1"), 5*time.Minute)
	c.Set("key1", []byte("value2"), 5*time.Minute)

	val, ok := c.Get("key1")
	require.True(t, ok)
	assert.Equal(t, []byte("value2"), val)
}

func TestLRUCacheDelete(t *testing.T) {
	c := cache.NewLRUCache(10)
	c.Set("key1", []byte("value1"), 5*time.Minute)
	c.Delete("key1")

	_, ok := c.Get("key1")
	assert.False(t, ok)
}

func TestLRUCacheClear(t *testing.T) {
	c := cache.NewLRUCache(10)
	c.Set("key1", []byte("value1"), 5*time.Minute)
	c.Set("key2", []byte("value2"), 5*time.Minute)
	c.Clear()

	_, ok := c.Get("key1")
	assert.False(t, ok)
	_, ok = c.Get("key2")
	assert.False(t, ok)
	assert.Equal(t, 0, c.Len())
}

func TestLRUCacheLRUEviction(t *testing.T) {
	c := cache.NewLRUCache(3)
	c.Set("a", []byte("1"), 5*time.Minute)
	c.Set("b", []byte("2"), 5*time.Minute)
	c.Set("c", []byte("3"), 5*time.Minute)

	// Access "a" to make it recently used.
	c.Get("a")

	// Insert "d" — should evict "b" (least recently used).
	c.Set("d", []byte("4"), 5*time.Minute)

	_, ok := c.Get("b")
	assert.False(t, ok, "b should have been evicted")

	val, ok := c.Get("a")
	require.True(t, ok)
	assert.Equal(t, []byte("1"), val)

	val, ok = c.Get("d")
	require.True(t, ok)
	assert.Equal(t, []byte("4"), val)
}

func TestLRUCacheTTLExpiration(t *testing.T) {
	c := cache.NewLRUCache(10)
	c.Set("key1", []byte("value1"), 100*time.Millisecond)

	// Should be available immediately.
	_, ok := c.Get("key1")
	assert.True(t, ok)

	// Wait for expiration.
	time.Sleep(150 * time.Millisecond)

	_, ok = c.Get("key1")
	assert.False(t, ok, "entry should have expired")
}

func TestLRUCacheNoTTL(t *testing.T) {
	c := cache.NewLRUCache(10)
	c.Set("key1", []byte("value1"), 0)

	// No TTL — should persist until evicted.
	val, ok := c.Get("key1")
	require.True(t, ok)
	assert.Equal(t, []byte("value1"), val)
}

func TestLRUCacheStats(t *testing.T) {
	c := cache.NewLRUCache(10)
	c.Set("key1", []byte("value1"), 5*time.Minute)
	c.Get("key1")
	c.Get("missing")
	c.Delete("key1")
	c.Clear()

	s := c.Stats()
	assert.Equal(t, int64(1), s.Hits)
	assert.Equal(t, int64(1), s.Misses)
	assert.Equal(t, int64(1), s.SetOperations)
	assert.Equal(t, int64(2), s.GetOperations)
	assert.Equal(t, int64(1), s.DeleteOperations)
	assert.Equal(t, int64(1), s.ClearOperations)
}

func TestLRUCacheMaxSize(t *testing.T) {
	c := cache.NewLRUCache(2)
	c.Set("a", []byte("1"), 5*time.Minute)
	c.Set("b", []byte("2"), 5*time.Minute)
	c.Set("c", []byte("3"), 5*time.Minute)

	// Only 2 entries should remain.
	assert.Equal(t, 2, c.Len())
}

func TestLRUCacheConcurrentAccess(t *testing.T) {
	c := cache.NewLRUCache(100)
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				key := string(rune('a' + id)) + strconv.Itoa(j)
				c.Set(key, []byte("value"), 5*time.Minute)
				c.Get(key)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Should not panic and stats should be consistent.
	s := c.Stats()
	assert.Equal(t, int64(1000), s.SetOperations)
	assert.Equal(t, int64(1000), s.GetOperations)
}

func TestLRUCacheLargeValues(t *testing.T) {
	c := cache.NewLRUCache(10)
	largeValue := make([]byte, 1024*1024) // 1MB
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	c.Set("large", largeValue, 5*time.Minute)
	val, ok := c.Get("large")
	require.True(t, ok)
	assert.Equal(t, largeValue, val)
}

func TestHashBuilder(t *testing.T) {
	hb := cache.DefaultHashBuilder()

	// Same input should produce same hash.
	hash1 := hb.BuildHash("gpt-4o", []interface{}{
		map[string]interface{}{"role": "user", "content": "hello"},
	}, map[string]interface{}{"temperature": 0.7})
	hash2 := hb.BuildHash("gpt-4o", []interface{}{
		map[string]interface{}{"role": "user", "content": "hello"},
	}, map[string]interface{}{"temperature": 0.7})
	assert.Equal(t, hash1, hash2)

	// Different input should produce different hash.
	hash3 := hb.BuildHash("gpt-4o", []interface{}{
		map[string]interface{}{"role": "user", "content": "different"},
	}, map[string]interface{}{"temperature": 0.7})
	assert.NotEqual(t, hash1, hash3)

	// Different model should produce different hash.
	hash4 := hb.BuildHash("gpt-4o-mini", []interface{}{
		map[string]interface{}{"role": "user", "content": "hello"},
	}, map[string]interface{}{"temperature": 0.7})
	assert.NotEqual(t, hash1, hash4)
}

func TestBuildContentHash(t *testing.T) {
	data1 := []byte("hello world")
	data2 := []byte("hello world")
	data3 := []byte("different")

	hash1 := cache.BuildContentHash(data1)
	hash2 := cache.BuildContentHash(data2)
	hash3 := cache.BuildContentHash(data3)

	assert.Equal(t, hash1, hash2)
	assert.NotEqual(t, hash1, hash3)
}

func TestBuildCompositeHash(t *testing.T) {
	hash1 := cache.BuildCompositeHash("model", "gpt-4o")
	hash2 := cache.BuildCompositeHash("model", "gpt-4o")
	hash3 := cache.BuildCompositeHash("model", "gpt-3.5-turbo")

	assert.Equal(t, hash1, hash2)
	assert.NotEqual(t, hash1, hash3)
}

func TestCacheKey(t *testing.T) {
	key1 := cache.CacheKey("gpt-4o", []interface{}{
		map[string]interface{}{"role": "user", "content": "hello"},
	}, map[string]interface{}{"temperature": 0.7})
	key2 := cache.CacheKey("gpt-4o", []interface{}{
		map[string]interface{}{"role": "user", "content": "hello"},
	}, map[string]interface{}{"temperature": 0.7})

	assert.Equal(t, key1, key2)
	assert.Len(t, key1, 16) // 8 hex bytes = 16 chars
}

func TestResponseCacheKey(t *testing.T) {
	key := cache.ResponseCacheKey("gpt-4o", nil, nil)
	assert.True(t, len(key) > 0)
	assert.True(t, len(key) > 4 && key[:4] == "resp")
}

func TestEmbeddingCacheKey(t *testing.T) {
	key := cache.EmbeddingCacheKey("text-embedding-3-small", "hello world")
	assert.True(t, len(key) > 0)
	assert.Contains(t, key, "emb:")
}

func TestParseHashKey(t *testing.T) {
	hash := "0123456789abcdef"
	val, err := cache.ParseHashKey(hash)
	require.NoError(t, err)
	assert.Equal(t, uint32(0x01234567), val)
}

func TestEncodeDecodeUint32(t *testing.T) {
	val := uint32(12345)
	encoded := cache.EncodeUint32(val)
	decoded, err := cache.DecodeUint32(encoded)
	require.NoError(t, err)
	assert.Equal(t, val, decoded)
}

func TestEncodeDecodeInt64(t *testing.T) {
	val := int64(-12345)
	encoded := cache.EncodeInt64(val)
	decoded, err := cache.DecodeInt64(encoded)
	require.NoError(t, err)
	assert.Equal(t, val, decoded)
}

func TestEncodeDecodeTime(t *testing.T) {
	now := time.Now()
	encoded := cache.EncodeTime(now)
	decoded := cache.DecodeTime(encoded)
	assert.Equal(t, now.UnixNano(), decoded.UnixNano())
}

func TestStatsCollector(t *testing.T) {
	s := cache.NewStatsCollector()
	s.RecordHit(1 * time.Millisecond)
	s.RecordHit(2 * time.Millisecond)
	s.RecordMiss(1 * time.Millisecond)

	assert.InDelta(t, 2.0/3.0, s.GetHitRate(), 0.001)
	assert.Greater(t, s.GetAverageLatency().Nanoseconds(), int64(0))
	assert.Greater(t, s.GetMaxLatency().Nanoseconds(), int64(0))
	assert.Greater(t, s.GetMinLatency().Nanoseconds(), int64(0))
}

func TestStatsCollectorReset(t *testing.T) {
	s := cache.NewStatsCollector()
	s.RecordHit(1 * time.Millisecond)
	s.Reset()

	assert.Equal(t, int64(0), s.Snapshot().Hits)
	assert.Equal(t, int64(0), s.Snapshot().Misses)
}

func TestDefaultConfig(t *testing.T) {
	cfg := cache.DefaultConfig()
	assert.Equal(t, 1024, cfg.MaxEntries)
	assert.Equal(t, 5*time.Minute, cfg.DefaultTTL)
	assert.True(t, cfg.EnableExpirationCleanup)
	assert.Equal(t, 1*time.Minute, cfg.CleanupInterval)
}

func TestConfigValidation(t *testing.T) {
	cfg := cache.Config{}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, 1024, cfg.MaxEntries)
	assert.Equal(t, 5*time.Minute, cfg.DefaultTTL)
}

func TestEntryIsExpired(t *testing.T) {
	entry := &cache.Entry{
		ExpiresAt: time.Now().Add(-1 * time.Second),
	}
	assert.True(t, entry.IsExpired())

	entry2 := &cache.Entry{
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	assert.False(t, entry2.IsExpired())

	// Zero expiry means no expiration.
	entry3 := &cache.Entry{}
	assert.False(t, entry3.IsExpired())
}

func TestLRUEvictor(t *testing.T) {
	// Verify the evictor interface exists and has the expected name.
	e := cache.NewEvictor(cache.EvictionLRU)
	assert.Equal(t, "lru", e.Name())
}

func TestEvictionPolicies(t *testing.T) {
	assert.Equal(t, "lru", cache.NewEvictor(cache.EvictionLRU).Name())
	assert.Equal(t, "lfu", cache.NewEvictor(cache.EvictionLFU).Name())
	assert.Equal(t, "fifo", cache.NewEvictor(cache.EvictionFIFO).Name())
}

func TestAtomicCache(t *testing.T) {
	c := cache.NewAtomicCache(10)
	c.Set("key1", []byte("value1"), 5*time.Minute)

	val, ok := c.GetWithAtomic("key1")
	require.True(t, ok)
	assert.Equal(t, []byte("value1"), val)

	val, ok = c.GetWithAtomic("missing")
	assert.False(t, ok)

	assert.Equal(t, int64(1), c.GetHits())
	assert.Equal(t, int64(1), c.GetMisses())
}

func TestLRUCacheEmptyGet(t *testing.T) {
	c := cache.NewLRUCache(10)
	_, ok := c.Get("")
	assert.False(t, ok)
}

func TestLRUCacheZeroMaxSize(t *testing.T) {
	c := cache.NewLRUCache(0)
	// Should default to 1024.
	c.Set("key1", []byte("value1"), 5*time.Minute)
	assert.Equal(t, 1, c.Len())
}
