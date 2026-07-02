package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"tzro/internal/memory"
	"tzro/internal/stream"
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
		steps, err := memory.DB.GetThoughtSteps(nodeID)
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

	systemPrompt := fmt.Sprintf(`You are a Recall Node. Your goal is to align and synthesize discoveries from previous nodes.
Target Goal: %s

## Discovery Manifest (Tool Outputs from Upstream Nodes)
%s

You can use these tools to examine specific results in detail:
- <ACTION>{"tool": "fetch_details", "arguments": {"node_id": "id", "step_index": 0}}</ACTION>

On each step, reason about which discovery you need to examine to fulfill the goal.
When you have aligned all necessary information, output <SYNTHESIZE_READY>.
You have a maximum of %d steps.`, goal, manifest, maxSteps)

	lastResult := "Manifest loaded."
	for step < maxSteps {
		step++
		
		// 1. Infer next action
		rawResponse, err := engine.Infer(ctx, systemPrompt, lastResult, "")
		if err != nil {
			return "", fmt.Errorf("recall inference failed at step %d: %w", step, err)
		}

		if strings.Contains(rawResponse, "<SYNTHESIZE_READY>") {
			fmt.Fprintf(os.Stderr, "[Recall] Node %s signaled synthesis readiness at step %d\n", recallNodeID, step)
			break
		}

		// 2. Extract and execute tool call
		action, args := extractAction(rawResponse)
		if action == "fetch_details" {
			nodeID, _ := args["node_id"].(string)
			stepIdx, _ := args["step_index"].(float64) // JSON numbers are float64
			
			stepData, err := memory.DB.GetThoughtStepByProbeAndIndex(nodeID, int(stepIdx))
			if err != nil {
				lastResult = fmt.Sprintf("Error fetching details: %v", err)
			} else {
				lastResult = fmt.Sprintf("### Details for %s Step %d\nTool: %s\nOutput:\n%s", nodeID, int(stepIdx), stepData.ToolName, stepData.ToolOutput)
			}
		} else {
			lastResult = "No valid ACTION found. Use fetch_details or SYNTHESIZE_READY."
		}

		// Publish progress
		e.getPublisher().PublishStream(stream.StreamChunk{
			Source:  "executor",
			TaskID:  taskID,
			NodeID:  recallNodeID,
			Type:    "recall_step",
			Content: fmt.Sprintf("Step %d: %s", step, lastResult),
		})
	}

	// Final Synthesis Pass
	fmt.Fprintf(os.Stderr, "[Recall] Node %s executing final synthesis.\n", recallNodeID)
	synthPrompt := fmt.Sprintf(`You are the Synthesis Engine for a Recall Node.
Goal: %s
Review all gathered facts and produce a comprehensive, structured final answer.`, goal)
	
	return engine.Infer(ctx, synthPrompt, lastResult, "")
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


