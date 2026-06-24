package memory

import (
	"fmt"
	"time"
)

// RecordInferenceSample persists a single inference call's metrics to the
// inference_samples table. This survives daemon restarts, unlike the in-memory
// atomic counters in the inference package.
func (sdb *SqliteDatabase) RecordInferenceSample(promptTokens, completionTokens int, durationSec float64) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	if sdb.db == nil {
		return fmt.Errorf("database not initialized")
	}

	now := time.Now().Unix()
	_, err := sdb.db.Exec(
		`INSERT INTO inference_samples (prompt_tokens, completion_tokens, duration_us, recorded_at)
		 VALUES (?, ?, ?, ?)`,
		promptTokens, completionTokens, int64(durationSec*1e6), now,
	)
	if err != nil {
		return fmt.Errorf("failed to record inference sample: %w", err)
	}

	// Prune old samples beyond 7 days to prevent unbounded table growth.
	cutoff := now - 7*24*3600
	_, _ = sdb.db.Exec(`DELETE FROM inference_samples WHERE recorded_at < ?`, cutoff)

	return nil
}

// GetAverageTPS computes average tokens-per-second over inference samples
// recorded in the given time window (seconds). Returns 0.0 if no samples exist.
func (sdb *SqliteDatabase) GetAverageTPS(windowSeconds int64) float64 {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return 0.0
	}

	cutoff := time.Now().Unix() - windowSeconds
	var totalTokens int64
	var totalDurationUs int64
	err := sdb.db.QueryRow(
		`SELECT COALESCE(SUM(completion_tokens), 0), COALESCE(SUM(duration_us), 0)
		 FROM inference_samples WHERE recorded_at >= ?`, cutoff,
	).Scan(&totalTokens, &totalDurationUs)
	if err != nil || totalDurationUs == 0 {
		return 0.0
	}

	durationSec := float64(totalDurationUs) / 1e6
	return float64(totalTokens) / durationSec
}

// RecordCacheEvent persists a cache hit or miss to the cache_events table.
func (sdb *SqliteDatabase) RecordCacheEvent(hit bool) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	if sdb.db == nil {
		return fmt.Errorf("database not initialized")
	}

	now := time.Now().Unix()
	isHit := 0
	if hit {
		isHit = 1
	}
	_, err := sdb.db.Exec(
		`INSERT INTO cache_events (is_hit, recorded_at) VALUES (?, ?)`,
		isHit, now,
	)
	if err != nil {
		return fmt.Errorf("failed to record cache event: %w", err)
	}

	// Prune old events beyond 7 days.
	cutoff := now - 7*24*3600
	_, _ = sdb.db.Exec(`DELETE FROM cache_events WHERE recorded_at < ?`, cutoff)

	return nil
}

// GetDBCacheHitRate computes cache hit rate over events in the given window (seconds).
// Returns 1.0 (100%) if no events exist.
func (sdb *SqliteDatabase) GetDBCacheHitRate(windowSeconds int64) float64 {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return 1.0
	}

	cutoff := time.Now().Unix() - windowSeconds
	var total int64
	var hits int64
	err := sdb.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(is_hit), 0)
		 FROM cache_events WHERE recorded_at >= ?`, cutoff,
	).Scan(&total, &hits)
	if err != nil || total == 0 {
		return 1.0
	}

	return float64(hits) / float64(total)
}
