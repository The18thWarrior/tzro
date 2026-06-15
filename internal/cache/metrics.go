package cache

import (
	"sync/atomic"
)

var (
	GlobalCacheHits   int64
	GlobalCacheMisses int64
)

// RecordCacheHit records a cache hit
func RecordCacheHit() {
	atomic.AddInt64(&GlobalCacheHits, 1)
}

// RecordCacheMiss records a cache miss
func RecordCacheMiss() {
	atomic.AddInt64(&GlobalCacheMisses, 1)
}

// GetCacheHitRate returns the ratio of cache hits to total queries
func GetCacheHitRate() float64 {
	hits := atomic.LoadInt64(&GlobalCacheHits)
	misses := atomic.LoadInt64(&GlobalCacheMisses)
	total := hits + misses
	if total == 0 {
		return 1.0 // default to 100% if no queries
	}
	return float64(hits) / float64(total)
}
