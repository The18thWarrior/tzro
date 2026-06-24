package cache

import (
	"sync"
	"sync/atomic"
)

var (
	GlobalCacheHits   int64
	GlobalCacheMisses int64
)

// CacheEventPersistFunc persists cache hit/miss events to durable storage.
type CacheEventPersistFunc func(hit bool)

// CacheHitRateQueryFunc queries cache hit rate from durable storage.
type CacheHitRateQueryFunc func(windowSeconds int64) float64

var (
	cachePersister CacheEventPersistFunc
	cacheQuerier   CacheHitRateQueryFunc
	cacheMu        sync.RWMutex
)

// SetCacheEventPersister registers a callback that persists cache events to SQLite.
func SetCacheEventPersister(fn CacheEventPersistFunc) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cachePersister = fn
}

// SetCacheHitRateQuerier registers a callback that queries durable cache hit rate.
func SetCacheHitRateQuerier(fn CacheHitRateQueryFunc) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheQuerier = fn
}

// RecordCacheHit records a cache hit (in-memory + durable if registered)
func RecordCacheHit() {
	atomic.AddInt64(&GlobalCacheHits, 1)

	cacheMu.RLock()
	fn := cachePersister
	cacheMu.RUnlock()

	if fn != nil {
		go fn(true)
	}
}

// RecordCacheMiss records a cache miss (in-memory + durable if registered)
func RecordCacheMiss() {
	atomic.AddInt64(&GlobalCacheMisses, 1)

	cacheMu.RLock()
	fn := cachePersister
	cacheMu.RUnlock()

	if fn != nil {
		go fn(false)
	}
}

// GetCacheHitRate returns the ratio of cache hits to total queries.
// Prefers durable DB data over in-memory atomics.
func GetCacheHitRate() float64 {
	// Try durable storage first (24h rolling window)
	cacheMu.RLock()
	fn := cacheQuerier
	cacheMu.RUnlock()

	if fn != nil {
		return fn(24 * 3600)
	}

	// Fallback to in-memory atomics
	hits := atomic.LoadInt64(&GlobalCacheHits)
	misses := atomic.LoadInt64(&GlobalCacheMisses)
	total := hits + misses
	if total == 0 {
		return 1.0 // default to 100% if no queries
	}
	return float64(hits) / float64(total)
}
