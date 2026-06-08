package proactivity

import (
	"fmt"
	"time"
)

// PolicyDecision represents the evaluation result of a proposed action.
type PolicyDecision struct {
	Allowed          bool
	ApprovalRequired bool
	Reason           string
}

// SentinelGate enforces safety, permission, preemption, and budgeting policies.
type SentinelGate struct {
	GlobalExecutionBudget Budget
	GlobalIntervalBudget  Budget
}

// NewSentinelGate creates a new SentinelGate with standard global budgets.
func NewSentinelGate() *SentinelGate {
	return &SentinelGate{
		GlobalExecutionBudget: Budget{
			MaxCPUTime:   15 * time.Second,
			MaxTokens:    4000,
			MaxToolCalls: 5,
		},
		GlobalIntervalBudget: Budget{
			MaxCPUTime:   5 * time.Minute,
			MaxTokens:    50000,
			MaxToolCalls: 100,
		},
	}
}

// Evaluate checks if the proposed action is permitted to execute or must be queued/deferred/dropped.
func (g *SentinelGate) Evaluate(action *ProposedAction, tracker *BudgetTracker, daemon Daemon) PolicyDecision {
	// 1. Foreground Preemption Check:
	// Foreground user tasks always have absolute priority.
	if IsForegroundActive() {
		return PolicyDecision{
			Allowed:          false,
			ApprovalRequired: false,
			Reason:           "foreground activity is currently active; background execution deferred",
		}
	}

	// 2. Proactivity Ladder Validation:
	// Level L4 (External Side Effect) ALWAYS requires explicit user approval.
	if action.Level == L4ExternalSideEffect {
		return PolicyDecision{
			Allowed:          true,
			ApprovalRequired: true,
			Reason:           "L4 external side effects always require explicit user approval",
		}
	}

	// Level L3 (Reversible Action) configurable approval: default to requiring approval for safety.
	if action.Level == L3ReversibleAction {
		return PolicyDecision{
			Allowed:          true,
			ApprovalRequired: true, // Default to true for safety in v1
			Reason:           "L3 local reversible actions require user approval by default",
		}
	}

	// Level L2 (Suggest) surfaces recommendations/alert notifications. Enqueue to Attention Queue.
	if action.Level == L2Suggest {
		return PolicyDecision{
			Allowed:          true,
			ApprovalRequired: true,
			Reason:           "L2 suggestions require user review",
		}
	}

	// 3. Daemon Capabilities Check:
	// If daemon is not allowed to request LLM or exceeds daemon max proactivity level.
	if action.RequiresLLM && !daemon.RequiresLLM() {
		return PolicyDecision{
			Allowed:          false,
			ApprovalRequired: false,
			Reason:           fmt.Sprintf("daemon '%s' does not have LLM capabilities but proposed action requested LLM", daemon.Name()),
		}
	}

	if action.Level > daemon.MaxLevel() {
		return PolicyDecision{
			Allowed:          false,
			ApprovalRequired: false,
			Reason:           fmt.Sprintf("action proactivity level %s exceeds daemon '%s' max level %s", action.Level, daemon.Name(), daemon.MaxLevel()),
		}
	}

	// 4. Budget Enforcement:
	// Check single-execution budgets (Option B)
	daemonReq := daemon.ResourceRequirements()

	// CPU Execution limits
	if daemonReq.MaxCPUTime > 0 && action.EstimatedLatency > daemonReq.MaxCPUTime {
		return PolicyDecision{
			Allowed:          false,
			Reason:           fmt.Sprintf("estimated latency %s exceeds daemon execution limit %s", action.EstimatedLatency, daemonReq.MaxCPUTime),
		}
	}
	if g.GlobalExecutionBudget.MaxCPUTime > 0 && action.EstimatedLatency > g.GlobalExecutionBudget.MaxCPUTime {
		return PolicyDecision{
			Allowed:          false,
			Reason:           fmt.Sprintf("estimated latency %s exceeds global execution limit %s", action.EstimatedLatency, g.GlobalExecutionBudget.MaxCPUTime),
		}
	}

	// Check cumulative interval budgets (Option C)
	consumed := tracker.GetConsumedResources(daemon.Name())
	globalConsumed := tracker.GetGlobalConsumedResources()

	// Check Token limits
	if daemonReq.MaxTokens > 0 && consumed.MaxTokens+action.PayloadTokenEstimate() > daemonReq.MaxTokens {
		return PolicyDecision{
			Allowed:          false,
			Reason:           fmt.Sprintf("daemon '%s' token budget exhausted for current interval", daemon.Name()),
		}
	}
	if g.GlobalIntervalBudget.MaxTokens > 0 && globalConsumed.MaxTokens+action.PayloadTokenEstimate() > g.GlobalIntervalBudget.MaxTokens {
		return PolicyDecision{
			Allowed:          false,
			Reason:           "global background token budget exhausted for current interval",
		}
	}

	// Check Tool Call limits
	estimatedCalls := len(action.RequiredCapabilities)
	if daemonReq.MaxToolCalls > 0 && consumed.MaxToolCalls+estimatedCalls > daemonReq.MaxToolCalls {
		return PolicyDecision{
			Allowed:          false,
			Reason:           fmt.Sprintf("daemon '%s' tool call budget exhausted for current interval", daemon.Name()),
		}
	}
	if g.GlobalIntervalBudget.MaxToolCalls > 0 && globalConsumed.MaxToolCalls+estimatedCalls > g.GlobalIntervalBudget.MaxToolCalls {
		return PolicyDecision{
			Allowed:          false,
			Reason:           "global background tool call budget exhausted for current interval",
		}
	}

	// L0 & L1 deterministic actions allowed by default if budget permits.
	return PolicyDecision{
		Allowed:          true,
		ApprovalRequired: false,
		Reason:           "allowed under policy and budget limits",
	}
}

// PayloadTokenEstimate extracts/estimates token cost from proposed action.
func (action *ProposedAction) PayloadTokenEstimate() int {
	if !action.RequiresLLM {
		return 0
	}
	// For v1, return a sensible default estimate if not provided.
	return 1000
}
