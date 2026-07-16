package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"tzro/internal/compactor"
	"tzro/internal/memory"
	"tzro/internal/stream"
	"tzro/internal/symbols"
)

// RunRecall executes a Recall Node loop (ADR-0038).
// It traverses the execution history of specified upstream nodes to align and synthesize discoveries.
func (e *ExecutionEngine) RunRecall(ctx context.Context, taskID, recallNodeID string, upstreamNodeIDs []string, goal string, engine ProbeInferenceEngine) (string, error) {
	fmt.Fprintf(os.Stderr, "[Recall] Node %s starting for task %s (Upstream: %v)\n", recallNodeID, taskID, upstreamNodeIDs)

	maxSteps := 8
	step := 0

	// 1. Build initial manifest of discoveries (metadata only)
	manifest := ""
	for _, nodeID := range upstreamNodeIDs {
		steps, err := memory.DB.GetThoughtSteps(taskID + "_" + nodeID)
		if err != nil {
			continue
		}
		manifest += fmt.Sprintf("### Node: %s\n", nodeID)
		for _, s := range steps {
			if s.ToolName != "" {
				preview := truncate(s.ToolOutput, 100)
				manifest += fmt.Sprintf("- Step %d: %s(%s) -> %s\n", s.StepIndex, s.ToolName, s.ToolArgs, preview)
			}
		}
	}

	systemPrompt := fmt.Sprintf(`You are a Recall Node (Map Phase). Your goal is to align and synthesize discoveries from previous nodes.
Target Goal: %s

## Discovery Manifest (Tool Outputs from Upstream Nodes)
%s

You can use these tools to examine specific results in detail or record key facts:
- <ACTION>{"tool": "fetch_details", "arguments": {"node_id": "id", "step_index": 0}}</ACTION>
- <ACTION>{"tool": "update_refined_context", "arguments": {"fact": "key fact or signature found"}}</ACTION>

On each step:
1. Reason about which discovery metadata suggests a high-signal result.
2. Fetch details for high-signal steps.
3. Record critical findings using 'update_refined_context'.
4. When you have aligned all necessary information and your 'Refined Discovery Context' is sufficient, output <SYNTHESIZE_READY>.

You have a maximum of %d steps.`, goal, manifest, maxSteps)

	lastResult := "Manifest loaded."
	refinedContext := ""

	for step < maxSteps {
		step++

		// 1. Infer next action
		// Include refinedContext in the prompt if not empty
		currentPrompt := systemPrompt
		if refinedContext != "" {
			currentPrompt += fmt.Sprintf("\n\n## Current Refined Discovery Context:\n%s", refinedContext)
		}

		rawResponse, err := engine.Infer(ctx, currentPrompt, lastResult, "")
		if err != nil {
			return "", fmt.Errorf("recall inference failed at step %d: %w", step, err)
		}

		if strings.Contains(rawResponse, "<SYNTHESIZE_READY>") {
			fmt.Fprintf(os.Stderr, "[Recall] Node %s signaled synthesis readiness at step %d\n", recallNodeID, step)
			break
		}

		// 2. Extract and execute tool call
		action, args := extractAction(rawResponse)
		switch action {
		case "fetch_details":
			nodeID, _ := args["node_id"].(string)
			stepIdx, _ := args["step_index"].(float64)

			stepData, err := memory.DB.GetThoughtStepByProbeAndIndex(taskID+"_"+nodeID, int(stepIdx))
			if err != nil {
				lastResult = fmt.Sprintf("Error fetching details: %v", err)
			} else {
				lastResult = fmt.Sprintf("### Details for %s Step %d\nTool: %s\nOutput:\n%s", nodeID, int(stepIdx), stepData.ToolName, stepData.ToolOutput)
			}
		case "update_refined_context":
			fact, _ := args["fact"].(string)
			if refinedContext != "" {
				refinedContext += "\n"
			}
			refinedContext += "- " + fact
			lastResult = "Refined context updated."

			// ADR-0040: Automatic Recall Compaction
			if len(refinedContext) > 2000 {
				compacted, err := e.compactRefinedContext(ctx, refinedContext, goal, engine)
				if err == nil {
					refinedContext = compacted
					lastResult = "Refined context updated and compacted (limit reached)."
				}
			}
		default:
			lastResult = "No valid ACTION found. Use fetch_details, update_refined_context, or SYNTHESIZE_READY."
		}

		// Publish progress
		e.getPublisher().PublishStream(stream.StreamChunk{
			Source:  "executor",
			TaskID:  taskID,
			NodeID:  recallNodeID,
			Type:    "recall_step",
			Content: fmt.Sprintf("Step %d: %s (Context Size: %d)", step, lastResult, len(refinedContext)),
		})
	}

	// Final Synthesis Pass (Reduce)
	fmt.Fprintf(os.Stderr, "[Recall] Node %s executing final synthesis (Reduce Phase).\n", recallNodeID)

	// Load Symbol Index from upstream probes (ADR-0047)
	var symbolIndex []symbols.Symbol
	for _, nodeID := range upstreamNodeIDs {
		probeID := taskID + "_" + nodeID
		syms, err := memory.DB.GetSymbolIndex(probeID)
		if err == nil {
			symbolIndex = append(symbolIndex, syms...)
		}
	}

	// Build Symbol Index reference block for the synthesis prompt
	symbolRefBlock := ""
	if len(symbolIndex) > 0 {
		var sb strings.Builder
		sb.WriteString("\n\n## Authoritative Symbol Reference (AST-extracted, verified):\n")
		sb.WriteString("Use ONLY these exact names when referring to types, functions, and interfaces:\n")
		for _, sym := range symbolIndex {
			sb.WriteString(fmt.Sprintf("- %s (%s): %s\n", sym.Name, sym.Kind, sym.Signature))
		}
		symbolRefBlock = sb.String()
	}

	synthPrompt := fmt.Sprintf(`You are the Synthesis Engine (Reduce Phase) for a Recall Node.
Goal: %s

## Refined Discovery Context (Verified Facts):
%s%s

Review the gathered facts and produce a comprehensive, structured final answer. If the facts are insufficient, explain what is missing.`, goal, refinedContext, symbolRefBlock)

	synthesis, err := engine.Infer(ctx, synthPrompt, lastResult, "")
	if err != nil {
		return "", err
	}

	// Symbol Anchor Check (ADR-0047): verify synthesis references against Index
	if len(symbolIndex) > 0 {
		anchorResult := symbols.CheckAnchoring(synthesis, symbolIndex, symbols.DefaultAnchorThreshold)
		fmt.Fprintf(os.Stderr, "[Recall] Symbol Anchor Check: %d referenced, %d anchored, %d unanchored (%.0f%% hallucination)\n",
			anchorResult.TotalReferenced, anchorResult.Anchored, len(anchorResult.Unanchored), anchorResult.HallucinationPct*100)

		if anchorResult.NeedsCorrection {
			fmt.Fprintf(os.Stderr, "[Recall] Hallucination threshold exceeded — running targeted correction pass.\n")
			correctionPrompt := symbols.BuildCorrectionPrompt(synthesis, anchorResult.Unanchored, symbolIndex)
			corrected, corrErr := engine.Infer(ctx, "You are a code documentation editor. Fix hallucinated symbol names.", correctionPrompt, "")
			if corrErr == nil && corrected != "" {
				synthesis = corrected
				fmt.Fprintf(os.Stderr, "[Recall] Correction pass complete. Re-checking...\n")

				// Re-check after correction
				recheck := symbols.CheckAnchoring(synthesis, symbolIndex, symbols.DefaultAnchorThreshold)
				fmt.Fprintf(os.Stderr, "[Recall] Post-correction: %d referenced, %d anchored, %d unanchored (%.0f%% hallucination)\n",
					recheck.TotalReferenced, recheck.Anchored, len(recheck.Unanchored), recheck.HallucinationPct*100)
			}
		}
	}

	return synthesis, nil
}

func extractAction(response string) (string, map[string]interface{}) {
	actionRe := regexp.MustCompile("(?s)<ACTION>(.*?)</ACTION>")
	matches := actionRe.FindStringSubmatch(response)
	if len(matches) > 1 {
		var parsed struct {
			Tool      string                 `json:"tool"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(matches[1]), &parsed); err == nil {
			return parsed.Tool, parsed.Arguments
		}
	}
	return "", nil
}

func (e *ExecutionEngine) compactRefinedContext(ctx context.Context, refinedCtx, goal string, engine ProbeInferenceEngine) (string, error) {
	// Use the structured compactor for content-aware fact compaction.
	// Code/data facts are preserved deterministically.
	// Large text-only facts are compressed via LLM.
	compactEngine := &compactor.RouterEngine{}
	result, err := compactor.CompactFacts(ctx, refinedCtx, goal, 2000, compactEngine)
	if err != nil {
		return refinedCtx, err
	}

	fmt.Fprintf(os.Stderr, "[Recall Compactor] %d→%d chars (%d LLM calls)\n",
		result.InputChars, result.OutputChars, result.LLMCalls)

	return result.Output, nil
}
