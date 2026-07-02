package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

// DefaultEdgeThoughtInference implements the EdgeThoughtInference interface
// by calling the global structured inference model with a constrained JSON schema.
type DefaultEdgeThoughtInference struct{}

// edgeThoughtSchema is the GBNF-constrained JSON schema for edge thought outputs.
const edgeThoughtSchema = `{
	"type": "object",
	"properties": {
		"thought": {
			"type": "string"
		},
		"goalConfidence": {
			"type": "number"
		},
		"goalAchieved": {
			"type": "boolean"
		}
	},
	"required": ["thought", "goalConfidence", "goalAchieved"]
}`

// edgeThoughtResponse is an unexported struct used to parse the inference response.
type edgeThoughtResponse struct {
	Thought        string  `json:"thought"`
	GoalConfidence float64 `json:"goalConfidence"`
	GoalAchieved   bool    `json:"goalAchieved"`
}

// GenerateEdgeThought evaluates whether a source node's output provides sufficient
// context for a target node to achieve the overall task goal.
func (i *DefaultEdgeThoughtInference) GenerateEdgeThought(
	ctx context.Context,
	taskID string,
	sourceNode *compiler.GraphNode,
	targetNode *compiler.GraphNode,
	sourceOutput string,
	stepIndex int,
) (*memory.EdgeThought, error) {
	// Truncate source output if too long.
	if len(sourceOutput) > 4000 {
		sourceOutput = sourceOutput[:4000] + "\n... [truncated]"
	}

	// Build system prompt.
	systemPrompt := `You are an Edge Thought evaluator for a DAG execution engine. 
Your job is to assess whether a completed node's output provides sufficient 
context for the next node to achieve the overall task goal. 

### SKEPTICISM GUARD (CRITICAL):
- If the task goal requires READING, EXTRACTING, or SYNTHESIZING content, and the output ONLY contains lists of files/paths without their content, set goalConfidence < 0.5 and goalAchieved = false.
- Do NOT assume a goal is achieved because a tool "finished." A tool finishing with 0 results often means MORE work is needed (e.g., search elsewhere).
- Be extremely strict about "goalAchieved": 
    - If the overall goal requires writing a file, creating a report, or saving data, goalAchieved MUST be false until you see a successful 'write_file' or 'save' operation in the output.
    - Having the synthesized text in memory (e.g., from a 'probe' or 'recall' node) is NOT enough to set goalAchieved = true if a downstream 'write_file' node exists.
    - RECALL NODE (ADR-0038): If the source is a 'recall' node, its output is a prioritized synthesis of prior discoveries. If the confidence is high, trust that it has aligned all necessary information for the next node.
- Be extremely strict about "goalAchieved": only set to true if the final intended outcome (e.g., a written file with actual data) is verified in the output.

Be calibrated: 
- 0.0 = no useful context / placeholder output
- 0.5 = partial context (e.g., file list obtained, but content not read)
- 0.8+ = sufficient context (e.g., all relevant file contents are in memory)
- 1.0 = complete context / goal fully realized

Output ONLY valid JSON matching the schema.`

	// Build user prompt.
	userPrompt := fmt.Sprintf(`Source Node: 
	- ID: %s
	- Type: %s
	- Instructions: %s

Source Output:
%s

Target Node:
- ID: %s
- Type: %s
- Instructions: %s

Assess the edge between these two nodes based on the source output's 
relevance to the target node's goal. Return JSON matching the schema.`,
		sourceNode.ID, sourceNode.Type, sourceNode.Instructions,
		sourceOutput,
		targetNode.ID, targetNode.Type, targetNode.Instructions,
	)

	// Construct the inference request.
	req := inference.StructuredInferenceRequest{
		Messages: []inference.InferenceMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		JSONSchema: edgeThoughtSchema,
		TaskID:     taskID,
	}

	// Execute inference.
	result, err := inference.GlobalLocalModel.ExecuteStructured(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("inference failed: %w", err)
	}

	// Parse response into edgeThoughtResponse.
	var resp edgeThoughtResponse
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		fmt.Fprintf(os.Stderr, "[EdgeThought] parse failure for task %s: %v — defaulting to confidence=0.3, goalAchieved=false\n", taskID, err)
		return &memory.EdgeThought{
			TaskID:         taskID,
			SourceNode:     sourceNode.ID,
			TargetNode:     targetNode.ID,
			Thought:        "",
			GoalConfidence: 0.3,
			GoalAchieved:   false,
			StepIndex:      stepIndex,
		}, nil
	}

	// Clamp confidence to [0.0, 1.0].
	resp.GoalConfidence = clampFloat(resp.GoalConfidence, 0.0, 1.0)

	// Construct and return the EdgeThought.
	edgeThought := &memory.EdgeThought{
		TaskID:         taskID,
		SourceNode:     sourceNode.ID,
		TargetNode:     targetNode.ID,
		Thought:        resp.Thought,
		GoalConfidence: resp.GoalConfidence,
		GoalAchieved:   resp.GoalAchieved,
		StepIndex:      stepIndex,
	}

	// Log result to stderr.
	fmt.Fprintf(os.Stderr, "[EdgeThought] source->target: confidence=%.2f, goalAchieved=%v\n", resp.GoalConfidence, resp.GoalAchieved)

	return edgeThought, nil
}

// clampFloat constrains a float to the given [min, max] range.
func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
