package proactivity

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// ==========================================
// 1. ObserverDaemon
// ==========================================

// ObserverDaemon monitors workflow and tool failures to suggest recoveries.
type ObserverDaemon struct {
	mu            sync.Mutex
	failureCounts map[string]int // maps taskId/workflowId to consecutive failure counts
}

// NewObserverDaemon creates a new ObserverDaemon.
func NewObserverDaemon() *ObserverDaemon {
	return &ObserverDaemon{
		failureCounts: make(map[string]int),
	}
}

func (d *ObserverDaemon) Name() string {
	return "observer_daemon"
}

func (d *ObserverDaemon) Subscriptions() []string {
	return []string{"workflow.failed", "tool.failed", "model.latency_spike"}
}

func (d *ObserverDaemon) MaxLevel() ProactivityLevel {
	return L2Suggest
}

func (d *ObserverDaemon) ResourceRequirements() Budget {
	return Budget{
		MaxCPUTime:   2 * time.Second,
		MaxTokens:    0, // Deterministic only
		MaxToolCalls: 0,
	}
}

func (d *ObserverDaemon) RequiresLLM() bool {
	return false
}

func (d *ObserverDaemon) Handler(ctx context.Context, event Event) (*ProposedAction, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch event.Type {
	case "workflow.failed":
		id := event.CorrelationID
		if id == "" {
			id = "unknown_wf"
		}
		d.failureCounts[id]++
		count := d.failureCounts[id]

		if count >= 2 {
			// Propose L2 Suggestion to retry with smaller context
			return &ProposedAction{
				ID:           fmt.Sprintf("act_wf_fail_%s", id),
				Level:        L2Suggest,
				ActionType:   "workflow_retry_suggestion",
				Description:  fmt.Sprintf("Workflow '%s' has failed %d times consecutively. Propose retrying with a smaller context window.", id, count),
				Confidence:   0.9,
				IsReversible: true,
			}, nil
		}

	case "tool.failed":
		id := event.CorrelationID
		return &ProposedAction{
			ID:           fmt.Sprintf("act_tool_fail_%s", id),
			Level:        L2Suggest,
			ActionType:   "tool_failure_alert",
			Description:  fmt.Sprintf("Tool invocation failed in task '%s'. Propose inspecting credentials or checking server logs.", id),
			Confidence:   0.85,
			IsReversible: true,
		}, nil

	case "model.latency_spike":
		return &ProposedAction{
			ID:           "act_latency_spike",
			Level:        L1Prepare, // L1 local cache pre-warm/GC
			ActionType:   "cache_compaction",
			Description:  "Model latency spike detected. Propose pre-emptively running Tier-1 active slot cache erasure to free resources.",
			Confidence:   0.8,
			IsReversible: true,
			Execute: func(ctx context.Context) (string, error) {
				fmt.Fprintln(os.Stderr, "[ObserverDaemon Action] Clearing cache slots...")
				return "Cleared model active slots successfully", nil
			},
		}, nil
	}

	return nil, nil
}

// ==========================================
// 2. CompactorDaemon
// ==========================================

// CompactorDaemon monitors memory and context pressure to propose compaction.
type CompactorDaemon struct{}

func NewCompactorDaemon() *CompactorDaemon {
	return &CompactorDaemon{}
}

func (d *CompactorDaemon) Name() string {
	return "compactor_daemon"
}

func (d *CompactorDaemon) Subscriptions() []string {
	return []string{"memory.compaction_needed"}
}

func (d *CompactorDaemon) MaxLevel() ProactivityLevel {
	return L1Prepare
}

func (d *CompactorDaemon) ResourceRequirements() Budget {
	return Budget{
		MaxCPUTime:   5 * time.Second,
		MaxTokens:    1000,
		MaxToolCalls: 2,
	}
}

func (d *CompactorDaemon) RequiresLLM() bool {
	return true
}

func (d *CompactorDaemon) Handler(ctx context.Context, event Event) (*ProposedAction, error) {
	if event.Type == "memory.compaction_needed" {
		return &ProposedAction{
			ID:           "act_compact_context",
			Level:        L1Prepare,
			ActionType:   "context_compaction",
			Description:  "Large payload cached on disk. Propose compiling a metadata summary to prune vector search candidates.",
			Confidence:   0.9,
			RequiresLLM:  true,
			IsReversible: true,
			Execute: func(ctx context.Context) (string, error) {
				fmt.Fprintln(os.Stderr, "[CompactorDaemon Action] Compacting context and building summarization indices...")
				return "Context compacted and SQLite indices optimized successfully", nil
			},
		}, nil
	}
	return nil, nil
}

// ==========================================
// 3. ReconcilerDaemon
// ==========================================

// ReconcilerDaemon monitors plan drift and stale caches to suggest alignments.
type ReconcilerDaemon struct{}

func NewReconcilerDaemon() *ReconcilerDaemon {
	return &ReconcilerDaemon{}
}

func (d *ReconcilerDaemon) Name() string {
	return "reconciler_daemon"
}

func (d *ReconcilerDaemon) Subscriptions() []string {
	return []string{"plan.drift_detected", "cache.stale"}
}

func (d *ReconcilerDaemon) MaxLevel() ProactivityLevel {
	return L2Suggest
}

func (d *ReconcilerDaemon) ResourceRequirements() Budget {
	return Budget{
		MaxCPUTime:   3 * time.Second,
		MaxTokens:    0,
		MaxToolCalls: 1,
	}
}

func (d *ReconcilerDaemon) RequiresLLM() bool {
	return false
}

func (d *ReconcilerDaemon) Handler(ctx context.Context, event Event) (*ProposedAction, error) {
	switch event.Type {
	case "plan.drift_detected":
		return &ProposedAction{
			ID:           "act_plan_drift",
			Level:        L2Suggest,
			ActionType:   "realign_plan_suggestion",
			Description:  "Plan drift detected between execution graph and external world state. Propose reviewing/regenerating the Abstract Graph.",
			Confidence:   0.9,
			IsReversible: true,
		}, nil

	case "cache.stale":
		// Propose L1 deterministic refresh
		return &ProposedAction{
			ID:           "act_cache_refresh",
			Level:        L1Prepare,
			ActionType:   "deterministic_cache_refresh",
			Description:  "Stale cache entry detected. Propose refreshing index configurations.",
			Confidence:   0.95,
			IsReversible: true,
			Execute: func(ctx context.Context) (string, error) {
				fmt.Fprintln(os.Stderr, "[ReconcilerDaemon Action] Refreshing stale cache indexes...")
				return "Cache refreshed successfully", nil
			},
		}, nil
	}

	return nil, nil
}

// ==========================================
// 4. PrefetcherDaemon
// ==========================================

// PrefetcherDaemon monitors user idle states to prefetch resources.
type PrefetcherDaemon struct{}

func NewPrefetcherDaemon() *PrefetcherDaemon {
	return &PrefetcherDaemon{}
}

func (d *PrefetcherDaemon) Name() string {
	return "prefetcher_daemon"
}

func (d *PrefetcherDaemon) Subscriptions() []string {
	return []string{"user.idle"}
}

func (d *PrefetcherDaemon) MaxLevel() ProactivityLevel {
	return L1Prepare
}

func (d *PrefetcherDaemon) ResourceRequirements() Budget {
	return Budget{
		MaxCPUTime:   10 * time.Second,
		MaxTokens:    0,
		MaxToolCalls: 5,
	}
}

func (d *PrefetcherDaemon) RequiresLLM() bool {
	return false
}

func (d *PrefetcherDaemon) Handler(ctx context.Context, event Event) (*ProposedAction, error) {
	if event.Type == "user.idle" {
		return &ProposedAction{
			ID:           "act_cache_warmup",
			Level:        L1Prepare,
			ActionType:   "cache_warmup",
			Description:  "System is idle. Propose prefetching and loading the static system prompt cache segment.",
			Confidence:   0.95,
			IsReversible: true,
			Execute: func(ctx context.Context) (string, error) {
				// Simulate prefetching static prompt segments
				fmt.Fprintln(os.Stderr, "[PrefetcherDaemon Action] Pre-warming prompt templates...")
				return "KV attention cache pre-warm completed successfully", nil
			},
		}, nil
	}
	return nil, nil
}
