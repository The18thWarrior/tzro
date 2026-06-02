package executor

// executor_context.go — Accumulated Context Architecture for GBNF Bridge Prompts.
//
// Replaces {{nodes.X.output.Y}} template interpolation with agentnami-style
// structured context injection. Instead of embedding resolved values inline
// into prose (where the GBNF bridge must re-extract them stochastically),
// upstream node outputs are passed as labeled structured blocks alongside
// the original instruction with {{...}} references as extraction hints.
//
// See: benchmark-analysis-2026-05-30-1300 for failure analysis motivating this change.

import (
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// maxAccumulatedContextNodes limits how many completed upstream nodes are included
// in the accumulated context. Only the N most recently completed nodes are kept.
// This bounds KV-cache memory pressure without truncating individual node outputs,
// preserving full structured data for the GBNF bridge extraction.
//
// Benchmark evidence (2026-05-30 17:10 run): unbounded context caused ~849 MB/case
// RSS growth, triggering Tier-2 sidecar recycle after just 5 cases. Limiting to 3
// nodes targets ≤100 MB/case while retaining the direct ancestors that matter most
// for downstream argument extraction.
const maxAccumulatedContextNodes = 3

// buildAccumulatedContext collects the most recent completed upstream node outputs
// for a task and formats them as labeled structured blocks. This replaces template
// interpolation for LLM-facing prompts, giving the GBNF bridge clean structured
// data to extract from. Only the last [maxAccumulatedContextNodes] completed nodes
// are included to bound memory pressure — individual outputs are never truncated.
//
// Output format:
//
//	--- nodeID (toolName) [completed] ---
//	<raw_output or cleaned output>
//
// The graph parameter is used to look up tool names for each node ID.
func buildAccumulatedContext(taskID string, graph *compiler.ExecutionGraph) string {
	states := memory.DB.GetAllNodeStates(taskID)
	if len(states) == 0 {
		return ""
	}

	// Build a map of nodeID → tool name from the graph
	nodeToolMap := make(map[string]string)
	if graph != nil {
		for _, n := range graph.Nodes {
			nodeToolMap[n.ID] = n.Action
		}
	}

	// Collect completed nodes with usable output
	type completedNode struct {
		nodeID string
		output string
	}
	var completed []completedNode
	for _, state := range states {
		if state.Status != "completed" {
			continue
		}

		// Use RawOutput (clean tool response) when available, fall back to
		// Output with tier prefix stripped
		output := state.RawOutput
		if output == "" {
			output = state.Output
			if idx := strings.Index(output, "] "); idx != -1 {
				output = output[idx+2:]
			}
		}

		if output == "" {
			continue
		}

		completed = append(completed, completedNode{nodeID: state.NodeID, output: output})
	}

	if len(completed) == 0 {
		return ""
	}

	// Keep only the N most recently completed nodes. Earlier nodes are dropped
	// entirely rather than truncated, so the bridge always sees full uncut data
	// for the nodes that remain. This bounds KV-cache allocation per inference.
	skipped := 0
	if len(completed) > maxAccumulatedContextNodes {
		skipped = len(completed) - maxAccumulatedContextNodes
		completed = completed[skipped:]
	}

	var sb strings.Builder
	if skipped > 0 {
		sb.WriteString(fmt.Sprintf("[... %d earlier completed nodes omitted ...]\n\n", skipped))
	}

	for _, cn := range completed {
		toolName := nodeToolMap[cn.nodeID]
		if toolName == "" {
			toolName = "unknown"
		}

		sb.WriteString(fmt.Sprintf("--- %s (%s) [completed] ---\n", cn.nodeID, toolName))
		sb.WriteString(cn.output)
		sb.WriteString("\n\n")
	}

	result := sb.String()
	fmt.Fprintf(os.Stderr, "[Executor AccumulatedContext] Built %d chars of structured context from %d completed nodes (of %d total, %d skipped)\n",
		len(result), len(completed), len(completed)+skipped, skipped)

	return result
}

// buildContextAwareSystemPrompt creates the system prompt for context-aware bridge extraction.
// The instruction is passed through with {{nodes.X.output.Y}} references intact, serving as
// extraction hints that tell the bridge which upstream output fields to grab from context.
func buildContextAwareSystemPrompt(toolName string, originalInstruction string, schema string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf(
		"You are the Local Tactician Node Executor. Extract structured tool parameters for '%s' from the accumulated context below.\n\n",
		toolName,
	))

	if schema != "" {
		sb.WriteString("OUTPUT SCHEMA:\n")
		sb.WriteString(schema)
		sb.WriteString("\n\n")
	}

	sb.WriteString("INSTRUCTION:\n")
	sb.WriteString(originalInstruction)
	sb.WriteString("\n\n")

	// If the instruction contains {{...}} references, add extraction guidance
	if strings.Contains(originalInstruction, "{{nodes.") {
		sb.WriteString("The instruction contains {{nodes.X.output.Y}} references indicating which upstream output fields to extract. ")
		sb.WriteString("Match these references to the corresponding values in the accumulated context blocks. ")
		sb.WriteString("Use the EXACT values from the context — do NOT hallucinate or generate placeholder values.\n")
	}

	sb.WriteString("\nReturn ONLY a valid JSON object matching the schema. Do not include explanatory text.")

	return sb.String()
}

// buildContextAwareUserPrompt creates the user prompt with structured accumulated context.
// The prompt includes: accumulated context blocks, optional RAG context, and the interpolated
// instruction as a fallback reference.
func buildContextAwareUserPrompt(accumulatedContext string, ragContext string, interpolatedInstruction string) string {
	var sb strings.Builder

	if accumulatedContext != "" {
		sb.WriteString("## Accumulated Context from Prior Steps\n\n")
		sb.WriteString(accumulatedContext)
		sb.WriteString("\n")
	}

	if ragContext != "" {
		sb.WriteString("## Additional Context\n\n")
		sb.WriteString(ragContext)
		sb.WriteString("\n")
	}

	// Include the interpolated instruction as a concrete reference showing resolved values.
	// This gives the bridge both structured context (for key-based extraction) AND
	// the resolved flat text (as a verification reference).
	sb.WriteString("## Step Instruction (with resolved values)\n\n")
	sb.WriteString(interpolatedInstruction)

	return sb.String()
}
