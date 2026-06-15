package inference

import (
	"sync/atomic"
)

var (
	GlobalPromptTokens      int64
	GlobalCompletionTokens  int64
	GlobalInferenceDuration int64 // stored as microseconds
	GlobalInferenceCount    int64
)

// RecordGlobalMetrics records the prompt/completion token count and duration of a model call
func RecordGlobalMetrics(prompt, completion int, durationSec float64) {
	atomic.AddInt64(&GlobalPromptTokens, int64(prompt))
	atomic.AddInt64(&GlobalCompletionTokens, int64(completion))
	atomic.AddInt64(&GlobalInferenceDuration, int64(durationSec*1e6))
	atomic.AddInt64(&GlobalInferenceCount, 1)
}

// GetGlobalAverageTPS returns the global average tokens per second
func GetGlobalAverageTPS() float64 {
	durationUs := atomic.LoadInt64(&GlobalInferenceDuration)
	if durationUs == 0 {
		return 0.0
	}
	durationSec := float64(durationUs) / 1e6
	completion := atomic.LoadInt64(&GlobalCompletionTokens)
	return float64(completion) / durationSec
}
