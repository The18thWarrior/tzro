package inference

import (
	"context"
	"sync"
)

type contextKey string

const TokenTrackerKey contextKey = "token_tracker"

// TokenUsage holds metrics for token counts.
type TokenUsage struct {
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	TotalTokens      int     `json:"totalTokens"`
	DurationSeconds  float64 `json:"durationSeconds"`
	AvgTokensPerSec  float64 `json:"avgTokensPerSec"`
	MinTokensPerSec  float64 `json:"minTokensPerSec"`
	MaxTokensPerSec  float64 `json:"maxTokensPerSec"`
	InferenceCount   int     `json:"inferenceCount"`
}

// TokenTracker collects token usage metrics during a task context.
type TokenTracker struct {
	mu         sync.Mutex
	localUsage TokenUsage
	cloudUsage TokenUsage
}

// NewTokenTracker initializes a new TokenTracker.
func NewTokenTracker() *TokenTracker {
	return &TokenTracker{}
}

// WithTokenTracker embeds a TokenTracker into a context.
func WithTokenTracker(ctx context.Context, tracker *TokenTracker) context.Context {
	return context.WithValue(ctx, TokenTrackerKey, tracker)
}

// GetTokenTracker retrieves a TokenTracker from the context.
func GetTokenTracker(ctx context.Context) (*TokenTracker, bool) {
	if ctx == nil {
		return nil, false
	}
	tracker, ok := ctx.Value(TokenTrackerKey).(*TokenTracker)
	return tracker, ok
}

// Record local or cloud token usage with speed metrics.
func (t *TokenTracker) Record(isCloud bool, prompt, completion int, durationSeconds, tokensPerSecond float64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	usage := &t.localUsage
	if isCloud {
		usage = &t.cloudUsage
	}

	usage.PromptTokens += prompt
	usage.CompletionTokens += completion
	usage.TotalTokens += (prompt + completion)
	usage.DurationSeconds += durationSeconds

	if tokensPerSecond > 0 {
		usage.InferenceCount++
		if usage.MinTokensPerSec == 0 || tokensPerSecond < usage.MinTokensPerSec {
			usage.MinTokensPerSec = tokensPerSecond
		}
		if tokensPerSecond > usage.MaxTokensPerSec {
			usage.MaxTokensPerSec = tokensPerSecond
		}
		// Recompute rolling average from total completion tokens and total duration
		if usage.DurationSeconds > 0 {
			usage.AvgTokensPerSec = float64(usage.CompletionTokens) / usage.DurationSeconds
		}
	}
}

// GetUsage returns the collected local and cloud usages.
func (t *TokenTracker) GetUsage() (TokenUsage, TokenUsage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.localUsage, t.cloudUsage
}
