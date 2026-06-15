package proactivity

import (
	"sync"
	"time"
)

// ResourceUsage holds consumed resources for budget evaluation.
type ResourceUsage struct {
	MaxCPUTime   time.Duration
	MaxTokens    int
	MaxToolCalls int
}

// BudgetTracker tracks resource consumption for background daemons.
type BudgetTracker struct {
	mu            sync.RWMutex
	daemonUsage   map[string]ResourceUsage
	globalUsage   ResourceUsage
	lastResetTime time.Time
	resetInterval time.Duration
}

// NewBudgetTracker creates a new BudgetTracker that resets at the given interval.
func NewBudgetTracker(resetInterval time.Duration) *BudgetTracker {
	return &BudgetTracker{
		daemonUsage:   make(map[string]ResourceUsage),
		lastResetTime: time.Now(),
		resetInterval: resetInterval,
	}
}

// RecordUsage registers consumed resources for a specific daemon.
func (bt *BudgetTracker) RecordUsage(daemonName string, cpu time.Duration, tokens int, toolCalls int) {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	// Auto-reset check
	if time.Since(bt.lastResetTime) >= bt.resetInterval {
		bt.daemonUsage = make(map[string]ResourceUsage)
		bt.globalUsage = ResourceUsage{}
		bt.lastResetTime = time.Now()
	}

	// Update daemon usage
	du := bt.daemonUsage[daemonName]
	du.MaxCPUTime += cpu
	du.MaxTokens += tokens
	du.MaxToolCalls += toolCalls
	bt.daemonUsage[daemonName] = du

	// Update global usage
	bt.globalUsage.MaxCPUTime += cpu
	bt.globalUsage.MaxTokens += tokens
	bt.globalUsage.MaxToolCalls += toolCalls
}

// GetConsumedResources returns consumed resources for a specific daemon.
func (bt *BudgetTracker) GetConsumedResources(daemonName string) ResourceUsage {
	bt.mu.RLock()
	defer bt.mu.RUnlock()

	// Check if reset is overdue
	if time.Since(bt.lastResetTime) >= bt.resetInterval {
		return ResourceUsage{}
	}

	return bt.daemonUsage[daemonName]
}

// GetGlobalConsumedResources returns global background consumed resources.
func (bt *BudgetTracker) GetGlobalConsumedResources() ResourceUsage {
	bt.mu.RLock()
	defer bt.mu.RUnlock()

	// Check if reset is overdue
	if time.Since(bt.lastResetTime) >= bt.resetInterval {
		return ResourceUsage{}
	}

	return bt.globalUsage
}

// Reset clears all accumulated resource usages.
func (bt *BudgetTracker) Reset() {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.daemonUsage = make(map[string]ResourceUsage)
	bt.globalUsage = ResourceUsage{}
	bt.lastResetTime = time.Now()
}
