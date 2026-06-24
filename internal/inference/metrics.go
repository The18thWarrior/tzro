package inference

import (
	"sync"
	"sync/atomic"
)

var (
	GlobalPromptTokens      int64
	GlobalCompletionTokens  int64
	GlobalInferenceDuration int64 // stored as microseconds
	GlobalInferenceCount    int64
)

// MetricsPersistFunc is the signature for a callback that durably persists
// inference metrics to SQLite. Set via SetMetricsPersister at startup.
type MetricsPersistFunc func(promptTokens, completionTokens int, durationSec float64)

// MetricsQueryFunc is the signature for a callback that reads average TPS
// from durable storage (SQLite) over a rolling window. Set via SetMetricsQuerier.
type MetricsQueryFunc func(windowSeconds int64) float64

var (
	metricsPersister MetricsPersistFunc
	metricsQuerier   MetricsQueryFunc
	metricsMu        sync.RWMutex
)

// SetMetricsPersister registers a callback that persists inference samples
// to durable storage (SQLite). Called once at daemon startup after DB init.
func SetMetricsPersister(fn MetricsPersistFunc) {
	metricsMu.Lock()
	defer metricsMu.Unlock()
	metricsPersister = fn
}

// SetMetricsQuerier registers a callback that queries durable TPS from the DB.
// Called once at daemon startup after DB init.
func SetMetricsQuerier(fn MetricsQueryFunc) {
	metricsMu.Lock()
	defer metricsMu.Unlock()
	metricsQuerier = fn
}

// RecordGlobalMetrics records the prompt/completion token count and duration of a model call.
// It updates both in-memory atomics (for hot-path within a process) and persists to SQLite
// (for durability across restarts) if a persister has been registered.
func RecordGlobalMetrics(prompt, completion int, durationSec float64) {
	atomic.AddInt64(&GlobalPromptTokens, int64(prompt))
	atomic.AddInt64(&GlobalCompletionTokens, int64(completion))
	atomic.AddInt64(&GlobalInferenceDuration, int64(durationSec*1e6))
	atomic.AddInt64(&GlobalInferenceCount, 1)

	metricsMu.RLock()
	fn := metricsPersister
	metricsMu.RUnlock()

	if fn != nil {
		// Fire-and-forget — DB write failures are non-fatal for inference
		go fn(prompt, completion, durationSec)
	}
}

// GetGlobalAverageTPS returns the average tokens per second.
// It prefers durable DB metrics (survives restarts) over in-memory atomics.
// Falls back to in-memory counters if no DB querier is registered or DB returns 0.
func GetGlobalAverageTPS() float64 {
	// Try durable storage first (24h rolling window)
	metricsMu.RLock()
	fn := metricsQuerier
	metricsMu.RUnlock()

	if fn != nil {
		if tps := fn(24 * 3600); tps > 0 {
			return tps
		}
	}

	// Fallback to in-memory atomics (current process session only)
	durationUs := atomic.LoadInt64(&GlobalInferenceDuration)
	if durationUs == 0 {
		return 0.0
	}
	durationSec := float64(durationUs) / 1e6
	completion := atomic.LoadInt64(&GlobalCompletionTokens)
	return float64(completion) / durationSec
}
