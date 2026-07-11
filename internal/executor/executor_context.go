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
	"tzro/internal/config"
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
// are included to bound memory pressure.
//
// ADR-0044: When callingNodeType is "synthesis", applies synthesis-specific assembly:
// - Validator and recall node outputs are fetched untruncated (no per-node budget)
// - Deterministic node outputs are capped at 256 chars
// - No global ceiling on total context size
//
// For all other callingNodeType values, applies standard tiered allocation:
// - Dynamic ceiling: min(nodeCount * 4096, 32000)
// - Tiered per-node budgets: recall(8) > validator(6) > action(4) > probe(2) > deterministic(1)
//
// ADR-0043 Mechanism B (superseded by ADR-0044): Per-node output is truncated using
// content-aware TruncateToolOutput. This is non-destructive — full output remains
// in SQLite for terminal synthesis and debugging.
//
// Output format:
//
//	--- nodeID (toolName) [completed] ---
//	<raw_output or cleaned output>
//
// The graph parameter is used to look up tool names for each node ID.
func buildAccumulatedContext(taskID string, graph *compiler.ExecutionGraph, callingNodeType string) string {
	states := memory.DB.GetAllNodeStates(taskID)
	if len(states) == 0 {
		return ""
	}

	// Build maps from the graph to identify node types and relationships
	nodeToolMap := make(map[string]string)
	nodeTypeMap := make(map[string]string)
	if graph != nil {
		for _, n := range graph.Nodes {
			nodeToolMap[n.ID] = n.Action
			nodeTypeMap[n.ID] = n.Type
		}
	}

	// Identify nodes superseded by a completed 'recall' node.
	// We check the graph edges to find which probe nodes feed into which recall nodes.
	// If a recall node is completed, its probe's raw output is 'sludge' and should be excluded.
	supersededProbes := make(map[string]bool)
	if graph != nil {
		// First, find all completed recall nodes in the current state
		completedRecalls := make(map[string]bool)
		for _, state := range states {
			if state.Status == "completed" && nodeTypeMap[state.NodeID] == "recall" {
				completedRecalls[state.NodeID] = true
			}
		}
		// Then, find their upstream sources
		for _, edge := range graph.Edges {
			if completedRecalls[edge.TargetID] {
				supersededProbes[edge.SourceID] = true
			}
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

		// Skip nodes that have been superseded by a completed Recall node.
		// This keeps the context 'clean' by replacing discovery sludge with aligned findings.
		if supersededProbes[state.NodeID] {
			fmt.Fprintf(os.Stderr, "[Executor AccumulatedContext] Skipping node %s as it has been superseded by a completed Recall node\n", state.NodeID)
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

	// ADR-0044: Synthesis-aware context assembly with tiered budgets.
	// Two paths depending on the calling node type.
	isSynthesis := callingNodeType == "synthesis"

	// Compute per-node budgets based on calling node type.
	type budgetEntry struct {
		nodeID string
		output string
		budget int // -1 means untruncated
	}
	budgeted := make([]budgetEntry, len(completed))

	if isSynthesis {
		// Synthesis path (ADR-0044 Mechanism A):
		// - Validator (action nodes with _validator suffix or any action) and recall nodes: untruncated
		// - Deterministic nodes: capped at 256 chars
		// - No global ceiling
		for i, cn := range completed {
			ntype := nodeTypeMap[cn.nodeID]
			switch ntype {
			case "recall":
				budgeted[i] = budgetEntry{cn.nodeID, cn.output, -1} // untruncated
			case "deterministic":
				budgeted[i] = budgetEntry{cn.nodeID, cn.output, 256}
			default:
				// action, probe, synthesis, etc. — untruncated for synthesis caller
				budgeted[i] = budgetEntry{cn.nodeID, cn.output, -1}
			}
		}
	} else {
		// Standard path (ADR-0044 Mechanism B+C):
		// Dynamic ceiling: min(nodeCount * 4096, 32000)
		// Tiered per-node budgets by type weight.
		dynamicCeiling := len(completed) * 4096
		hardCap := 32000
		configCeiling := config.GetAccumulatedContextMaxChars()
		if configCeiling > 0 && configCeiling < hardCap {
			hardCap = configCeiling
		}
		if dynamicCeiling > hardCap {
			dynamicCeiling = hardCap
		}

		// Compute total weight for tiered allocation
		typeWeights := map[string]int{
			"recall":        8,
			"action":        6, // covers validators (action type with _validator suffix)
			"probe":         2,
			"deterministic": 1,
		}
		defaultWeight := 4 // for unknown types (synthesis, sub_dag, etc.)

		totalWeight := 0
		nodeWeights := make([]int, len(completed))
		for i, cn := range completed {
			ntype := nodeTypeMap[cn.nodeID]
			w, ok := typeWeights[ntype]
			if !ok {
				w = defaultWeight
			}
			nodeWeights[i] = w
			totalWeight += w
		}

		if totalWeight == 0 {
			totalWeight = 1 // safety
		}

		for i, cn := range completed {
			perNodeBudget := (nodeWeights[i] * dynamicCeiling) / totalWeight
			if perNodeBudget < 256 {
				perNodeBudget = 256 // absolute floor
			}
			budgeted[i] = budgetEntry{cn.nodeID, cn.output, perNodeBudget}
		}
	}

	for _, be := range budgeted {
		toolName := nodeToolMap[be.nodeID]
		if toolName == "" {
			toolName = "unknown"
		}

		output := be.output
		if be.budget >= 0 && len(output) > be.budget {
			output = TruncateToolOutput(output, be.budget)
			fmt.Fprintf(os.Stderr, "[Executor AccumulatedContext] Truncated node %s output from %d to %d chars (budget: %d per node)\n",
				be.nodeID, len(be.output), len(output), be.budget)
		}

		sb.WriteString(fmt.Sprintf("--- %s (%s) [completed] ---\n", be.nodeID, toolName))
		sb.WriteString(output)
		sb.WriteString("\n\n")
	}

	result := sb.String()
	fmt.Fprintf(os.Stderr, "[Executor AccumulatedContext] Built %d chars of structured context from %d completed nodes (of %d total, %d skipped, synthesis=%v)\n",
		len(result), len(completed), len(completed)+skipped, skipped, isSynthesis)

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
