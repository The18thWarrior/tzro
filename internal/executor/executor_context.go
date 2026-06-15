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
	"tzro/internal/inference"
	"tzro/internal/memory"
)

// maxAccumulatedContextNodes limits how many completed upstream nodes are included
// in the accumulated context. Only the N most recently completed nodes are kept.
// This bounds KV-cache memory pressure without truncating individual node outputs,
// preserving full structured data for the GBNF bridge extraction.
//
// Originally set to 3 to control RSS growth (~849 MB/case unbounded). Increased
// to 6 after observing 80% PARAM FAIL rate from downstream validators losing
// upstream outputs. With output compaction in place, 6 nodes stays within safe
// memory bounds while ensuring validators see enough context for parameter extraction.
const maxAccumulatedContextNodes = 6

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

	// Inject the original user goal prompt so validators can extract entity
	// values (names, IDs, codes) that the planner may have omitted from
	// individual node instructions. Without this, the first validator in a
	// DAG has no source for parameter values not in its node instructions.
	if graph != nil && graph.GoalPrompt != "" {
		sb.WriteString("--- Original User Request ---\n")
		sb.WriteString(graph.GoalPrompt)
		sb.WriteString("\n\n")
	}

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

// buildStaticBaseInstruction returns the shared, invariant system prompt used by all
// GBNF bridge/exec nodes in a task. This text is identical across every node execution,
// enabling llama-server's --cache-reuse to share the KV cache segment for this prefix.
func buildStaticBaseInstruction(expectXML bool) string {
	base := "You are the Local Tactician Node Executor for the tzro durable execution engine. " +
		"Your role is to extract structured tool parameters from the accumulated context of prior workflow steps. "

	if expectXML {
		base += "You MUST return ONLY a valid XML structure matching the requested format. "
	} else {
		base += "You MUST return ONLY a valid JSON object matching the provided schema. "
	}

	base += "Do NOT hallucinate or generate placeholder values — use EXACT values from the context. " +
		"If the instruction contains {{nodes.X.output.Y}} references, match them to corresponding values in the accumulated context blocks."
	
	return base
}

// buildSegmentedMessages constructs a 4-message conversation structure designed to maximize
// KV cache prefix sharing across nodes in the same task. The layout is:
//
//  1. {system, staticBase}           — invariant across all nodes; cached
//  2. {user, accumulatedCtx}         — shared across nodes at the same topological level
//  3. {assistant, ack}               — synthetic turn boundary (omitted if no accumulated ctx)
//  4. {user, schema + instruction}   — per-node volatile content
//
// This segmentation ensures the first 1-2 messages' KV entries are reusable from the
// --cache-reuse 2048 window, avoiding redundant computation on repeated prompt prefixes.
func buildSegmentedMessages(staticBase string, accumulatedCtx string, schema string, instruction string, expectXML bool) []inference.InferenceMessage {
	var msgs []inference.InferenceMessage

	// Segment 1: Static invariant system prompt (cacheable)
	msgs = append(msgs, inference.InferenceMessage{
		Role:    "system",
		Content: staticBase,
	})

	// Segment 2-3: Accumulated context as a user→assistant exchange (cacheable per-level)
	if accumulatedCtx != "" {
		msgs = append(msgs, inference.InferenceMessage{
			Role:    "user",
			Content: "## Accumulated Context from Prior Steps\n\n" + accumulatedCtx,
		})
		msgs = append(msgs, inference.InferenceMessage{
			Role:    "assistant",
			Content: "I have reviewed the accumulated context. Ready to extract parameters.",
		})
	}

	// Segment 4: Per-node volatile content (schema + instruction)
	var sb strings.Builder
	if schema != "" {
		sb.WriteString("OUTPUT SCHEMA:\n")
		sb.WriteString(schema)
		sb.WriteString("\n\n")
	}
	sb.WriteString("INSTRUCTION:\n")
	sb.WriteString(instruction)
	
	if expectXML {
		sb.WriteString("\n\nReturn ONLY a valid XML structure.")
	} else {
		sb.WriteString("\n\nReturn ONLY a valid JSON object matching the schema.")
	}

	msgs = append(msgs, inference.InferenceMessage{
		Role:    "user",
		Content: sb.String(),
	})

	return msgs
}
