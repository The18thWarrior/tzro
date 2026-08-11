package executor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/inference"
)

const (
	// scatterMaxTokens is the generation limit for scatter probes.
	// Set to 300 tokens — enough for a focused paragraph, well under
	// the 400-token attention fatigue threshold (ADR-0071).
	scatterMaxTokens = 300
)

// SpawnScatterProbes inserts targeted Probe Nodes into the graph for each
// missing goal item, plus a scatter_assembly node downstream. The assembly
// node depends on all scatter probes and performs deterministic concatenation
// + smoothing + VTE re-run.
//
// Budget cap: min(len(specs), budget.RemainingSpawns/2) — reserves half the
// remaining budget for Edge Thoughts (ADR-0071).
//
// Returns ("", nil, nil) when budget is insufficient (no nodes created).
func SpawnScatterProbes(graph *compiler.ExecutionGraph, recallNodeID string, specs []ScatterSpec, budget *compiler.MutationBudget) (assemblyNodeID string, scatterNodeIDs []string, err error) {
	if budget == nil || len(specs) == 0 {
		return "", nil, nil
	}

	maxProbes := budget.RemainingSpawns / 2
	if maxProbes <= 0 {
		fmt.Fprintf(os.Stderr, "[Scatter] Budget insufficient for scatter (remaining=%d, need at least 2)\n",
			budget.RemainingSpawns)
		return "", nil, nil
	}

	// Cap at budget
	probeCount := len(specs)
	if probeCount > maxProbes {
		probeCount = maxProbes
	}

	// Create scatter probe nodes
	for i := 0; i < probeCount; i++ {
		spec := specs[i]
		nodeID := fmt.Sprintf("scatter_probe_%s_%d", recallNodeID, i)
		probeNode := compiler.GraphNode{
			ID:   nodeID,
			Type: "probe",
			ProbeConfig: &compiler.ProbeConfig{
				Goal:            spec.GoalItem,
				DirectSynthesis: true,
				ContextFile:     spec.ContextFilePath,
				MaxTokens:       scatterMaxTokens,
				StepBudget:      1, // Single-shot, no exploration
			},
			Instructions:        fmt.Sprintf("Generate a focused, concise answer for: %s", spec.GoalItem),
			Status:              "pending",
			ActivationThreshold: 0.0, // No edge thoughts for scatter probes
		}

		// Add node to graph
		graph.Nodes = append(graph.Nodes, probeNode)

		// Wire: recall → scatter_probe
		graph.Edges = append(graph.Edges, compiler.GraphEdge{
			SourceID: recallNodeID,
			TargetID: nodeID,
		})

		scatterNodeIDs = append(scatterNodeIDs, nodeID)

		// Decrement budget
		budget.RemainingSpawns--
	}

	// Create scatter_assembly node
	assemblyNodeID = fmt.Sprintf("scatter_assembly_%s", recallNodeID)
	assemblyNode := compiler.GraphNode{
		ID:           assemblyNodeID,
		Type:         "scatter_assembly",
		Instructions: recallNodeID, // The assembly handler reads this to find the recall node
		Status:       "pending",
	}
	graph.Nodes = append(graph.Nodes, assemblyNode)

	// Wire: each scatter_probe → scatter_assembly
	for _, probeID := range scatterNodeIDs {
		graph.Edges = append(graph.Edges, compiler.GraphEdge{
			SourceID: probeID,
			TargetID: assemblyNodeID,
		})
	}

	fmt.Fprintf(os.Stderr, "[Scatter] Spawned %d scatter probes + assembly node %s (budget remaining: %d)\n",
		probeCount, assemblyNodeID, budget.RemainingSpawns)

	return assemblyNodeID, scatterNodeIDs, nil
}

// assembleScatterOutput deterministically concatenates the original recall
// synthesis with scatter probe outputs, using section-per-item formatting.
// Empty scatter outputs are silently skipped.
func assembleScatterOutput(recallSynthesis string, scatterOutputs map[string]string) string {
	if len(scatterOutputs) == 0 {
		return recallSynthesis
	}

	var b strings.Builder
	b.WriteString(recallSynthesis)

	for goalItem, output := range scatterOutputs {
		output = strings.TrimSpace(output)
		if output == "" {
			continue
		}
		b.WriteString("\n\n## ")
		b.WriteString(goalItem)
		b.WriteString("\n\n")
		b.WriteString(output)
	}

	return b.String()
}

// smoothAssembly runs a single local-model inference to merge the assembled
// sections into a coherent document. This is a lightweight pass — not a full
// Recall Node re-run.
func smoothAssembly(ctx context.Context, assembled string, engine ProbeInferenceEngine) (string, error) {
	systemPrompt := `You are a technical editor. You will receive a document with multiple sections 
that need to be merged into a coherent, well-structured document. 
Preserve all factual content. Remove redundancy. Ensure consistent formatting.
Output ONLY the merged document — no meta-commentary.`

	userPrompt := fmt.Sprintf("Merge the following sections into a coherent document:\n\n%s", assembled)

	ctx = context.WithValue(ctx, inference.MaxTokensKey, 4096)
	result, err := engine.Infer(ctx, systemPrompt, userPrompt, "", TargetWorker)
	if err != nil {
		// On smoothing failure, return the assembled output as-is
		fmt.Fprintf(os.Stderr, "[Scatter] Smoothing pass failed: %v — using raw assembly\n", err)
		return assembled, nil
	}

	return result, nil
}
