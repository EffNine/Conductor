package cache

import (
	"time"
)

// Entry is a single cached value with metadata.
type Entry struct {
	Key         string
	Value       []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
	TTL         time.Duration
	AccessCount int64
	LastAccess  time.Time
}

// IsExpired reports whether the entry has passed its TTL.
func (e *Entry) IsExpired() bool {
	if e.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(e.ExpiresAt)
}

// Config holds cache configuration.
type Config struct {
	// MaxEntries is the maximum number of entries allowed. Default 1024.
	MaxEntries int
	// DefaultTTL is the default time-to-live for entries. Default 5 minutes.
	DefaultTTL time.Duration
	// EnableExpirationCleanup turns on background expiration cleanup.
	EnableExpirationCleanup bool
	// CleanupInterval is how often the background cleanup runs. Default 1 minute.
	CleanupInterval time.Duration
}

// Validate returns an error if config is invalid.
func (c *Config) Validate() error {
	if c.MaxEntries <= 0 {
		c.MaxEntries = 1024
	}
	if c.DefaultTTL <= 0 {
		c.DefaultTTL = 5 * time.Minute
	}
	if c.CleanupInterval <= 0 {
		c.CleanupInterval = 1 * time.Minute
	}
	return nil
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxEntries:              1024,
		DefaultTTL:              5 * time.Minute,
		EnableExpirationCleanup: true,
		CleanupInterval:         1 * time.Minute,
	}
}

// Stats holds cache statistics.
type Stats struct {
	Hits             int64
	Misses           int64
	Evictions        int64
	Expirations      int64
	SetOperations    int64
	GetOperations    int64
	DeleteOperations int64
	ClearOperations  int64
	CurrentEntries   int64
}

// Cache is the interface all cache implementations must satisfy.
type Cache interface {
	// Get retrieves a value by key. Returns nil if not found or expired.
	Get(key string) ([]byte, bool)
	// Set stores a value with the given TTL.
	Set(key string, value []byte, ttl time.Duration)
	// Delete removes a key.
	Delete(key string)
	// Clear removes all entries.
	Clear()
	// Stats returns current cache statistics.
	Stats() Stats
	// Stop halts background maintenance.
	Stop()
}
