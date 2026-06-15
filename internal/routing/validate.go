package routing

import (
	"context"
	"fmt"

	"tzro/internal/compiler"
	"tzro/internal/telemetry"
)

// PlanFunc is a function type for plan backends (local or cloud).
type PlanFunc func(ctx context.Context) (*compiler.ExecutionGraph, error)

// ValidateGraph runs the local plan validation pipeline:
//  1. Structural check — graph is non-nil with at least one node
//  2. Cycle detection — Kahn topological sort succeeds
//  3. Tool schema conformance — every action node references a registered tool
//
// The toolExists function should return true if the tool name is registered.
func ValidateGraph(graph *compiler.ExecutionGraph, toolExists func(string) bool) error {
	// Step 1: Structural check
	if graph == nil || len(graph.Nodes) == 0 {
		return fmt.Errorf("validation failed: empty or nil graph")
	}

	// Step 2: Cycle detection via Kahn topological sort
	if _, err := compiler.CompileAndSort(graph); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Step 3: Tool schema conformance
	// Probe, synthesis, and deterministic nodes are exempt from tool checks
	for _, node := range graph.Nodes {
		switch node.Type {
		case "probe", "synthesis", "deterministic":
			continue
		}
		if node.Action != "" && !toolExists(node.Action) {
			return fmt.Errorf("validation failed: node %q references unknown tool %q", node.ID, node.Action)
		}
	}

	return nil
}

// PlanWithEscalation attempts local planning, validates the result, and escalates
// to cloud planning if validation fails and the routing decision allows it.
// Publishes telemetry events for validation failures.
// Enforces a 1-retry cap: local → cloud. No further retries.
func PlanWithEscalation(ctx context.Context, localPlan, cloudPlan PlanFunc, decision RoutingDecision, toolExists func(string) bool) (*compiler.ExecutionGraph, error) {
	// Attempt local planning
	graph, err := localPlan(ctx)
	if err != nil {
		if !decision.AllowCloudFallback {
			return nil, fmt.Errorf("local planning failed and cloud fallback blocked by privacy policy: %w", err)
		}
		telemetry.Default.PublishEvent("plan_local_error", "", "", fmt.Sprintf("Local planning error: %v. Escalating to cloud.", err))
		return cloudPlan(ctx)
	}

	// Validate the locally-produced graph
	if valErr := ValidateGraph(graph, toolExists); valErr != nil {
		telemetry.Default.PublishEvent("plan_validation_failed", graph.TaskID, "",
			fmt.Sprintf("Local plan failed validation: %v. Escalating to cloud.", valErr))

		if !decision.AllowCloudFallback {
			return nil, fmt.Errorf("local plan invalid and cloud fallback blocked by privacy policy: %w", valErr)
		}

		// 1-retry cap: escalate to cloud
		return cloudPlan(ctx)
	}

	return graph, nil
}
