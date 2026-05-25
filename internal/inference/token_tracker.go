package inference

import (
	"context"
	"sync"
)

type contextKey string

const TokenTrackerKey contextKey = "token_tracker"

// TokenUsage holds metrics for token counts.
type TokenUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
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

// Record local or cloud token usage.
func (t *TokenTracker) Record(isCloud bool, prompt, completion int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if isCloud {
		t.cloudUsage.PromptTokens += prompt
		t.cloudUsage.CompletionTokens += completion
		t.cloudUsage.TotalTokens += (prompt + completion)
	} else {
		t.localUsage.PromptTokens += prompt
		t.localUsage.CompletionTokens += completion
		t.localUsage.TotalTokens += (prompt + completion)
	}
}

// GetUsage returns the collected local and cloud usages.
func (t *TokenTracker) GetUsage() (TokenUsage, TokenUsage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.localUsage, t.cloudUsage
}
