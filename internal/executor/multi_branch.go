package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/inference"
)

// multiBranchCandidateSchema is the GBNF-constrained JSON schema for K candidate outputs.
// The Local Model generates all K candidates in a single inference call by outputting
// a JSON array of ranked action proposals.
const multiBranchCandidateSchema = `{
	"type": "object",
	"properties": {
		"candidates": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"action": { "type": "string" },
					"toolName": { "type": "string" },
					"args": { "type": "object" },
					"reasoning": { "type": "string" },
					"selfScore": { "type": "number" }
				},
				"required": ["action", "toolName", "reasoning", "selfScore"]
			}
		}
	},
	"required": ["candidates"]
}`

// multiBranchResponse wraps the candidate array for JSON parsing.
type multiBranchResponse struct {
	Candidates []Candidate `json:"candidates"`
}

// GenerateKCandidates produces K ranked candidate actions in a single inference call.
// The Local Model outputs a JSON array of K alternative approaches with self-assessed
// scores, constrained by the GBNF schema. This is the single-slot-safe alternative
// to n=K batching (ADR-0045).
func (i *DefaultEdgeThoughtInference) GenerateKCandidates(
	ctx context.Context,
	taskID string,
	sourceNode *compiler.GraphNode,
	targetNode *compiler.GraphNode,
	sourceOutput string,
	k int,
) ([]Candidate, error) {
	systemPrompt := fmt.Sprintf(`You are a strategic action planner for a DAG executor.
Given the source node's output and the target node's objective, generate exactly %d
different candidate approaches to achieve the target node's goal. Each candidate
should use a different strategy or tool configuration. Rank them by how likely they
are to succeed, with the best approach first.

Respond with a JSON object containing a "candidates" array.`, k)

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
- Primary Tool: %s

Generate %d candidate approaches.`,
		sourceNode.ID, sourceNode.Type, sourceNode.Instructions,
		sourceOutput,
		targetNode.ID, targetNode.Type, targetNode.Instructions,
		targetNode.Action,
		k,
	)

	req := inference.StructuredInferenceRequest{
		Messages: []inference.InferenceMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		JSONSchema: multiBranchCandidateSchema,
		TaskID:     taskID,
	}

	result, err := inference.ExecuteRouterStructured(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("multi-branch inference failed: %w", err)
	}

	var resp multiBranchResponse
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		fmt.Fprintf(os.Stderr, "[MultiBranch] parse failure for task %s: %v — returning empty candidates\n", taskID, err)
		return nil, fmt.Errorf("multi-branch parse failure: %w", err)
	}

	// Clamp self-scores
	for j := range resp.Candidates {
		resp.Candidates[j].SelfScore = clampFloat(resp.Candidates[j].SelfScore, 0.0, 1.0)
	}

	fmt.Fprintf(os.Stderr, "[MultiBranch] Generated %d candidates for task %s\n", len(resp.Candidates), taskID)

	return resp.Candidates, nil
}

// selectBestCandidate returns the candidate with the highest Score,
// skipping pruned candidates (Score < 0).
func selectBestCandidate(candidates []Candidate) Candidate {
	bestIdx := -1
	bestScore := -2.0 // below any possible pruned score

	for i, c := range candidates {
		if c.Score > bestScore {
			bestScore = c.Score
			bestIdx = i
		}
	}

	if bestIdx >= 0 {
		return candidates[bestIdx]
	}

	// All candidates pruned — return empty
	return Candidate{}
}

// evaluateMultiBranch runs the complete multi-branch evaluation pipeline
// for a node (ADR-0045). Returns the winning candidate or nil if multi-branch
// should not run (spawned nodes, MCTSBranches=0, inference unavailable).
//
// Pipeline:
//  1. Guard: shouldUseMultiBranch check
//  2. Generate K candidates via single inference call
//  3. Classify each candidate's tool via Speculation Fence
//  4. Score non-pruned candidates via HeuristicValueFunction
//  5. Select best candidate
func evaluateMultiBranch(
	ctx context.Context,
	node *compiler.GraphNode,
	goalPrompt string,
	sourceOutput string,
	speculationCeil int,
) (*Candidate, error) {
	// Guard: only non-spawned nodes with MCTSBranches > 0
	if !shouldUseMultiBranch(node) {
		return nil, nil
	}

	k := node.MCTSBranches
	fmt.Fprintf(os.Stderr, "[MultiBranch] Evaluating %d candidates for node %s\n", k, node.ID)

	// Step 1: Generate K candidates via inference (or fallback)
	var candidates []Candidate

	// Try inference-based generation
	sidecarStatus, _, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
	sidecarActive := sidecarStatus == "Active" || sidecarStatus == "Adopted"

	if sidecarActive {
		// Use EdgeThoughtGen for real candidate generation
		gen := &DefaultEdgeThoughtInference{}
		// Build a minimal source node for the inference call
		sourceNode := &compiler.GraphNode{
			ID:           "source",
			Type:         "action",
			Instructions: goalPrompt,
		}
		var err error
		candidates, err = gen.GenerateKCandidates(ctx, "multi_branch", sourceNode, node, sourceOutput, k)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[MultiBranch] Candidate generation failed: %v — falling back to single-shot\n", err)
			return nil, nil // fall back to single-shot execution
		}
	} else {
		// No sidecar — generate heuristic fallback candidates based on node action
		candidates = []Candidate{
			{
				Action:    node.Action,
				ToolName:  node.Action,
				Args:      map[string]interface{}{},
				Reasoning: "Primary tool action (default)",
				SelfScore: 0.7,
			},
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Step 2: Classify + Score each candidate
	vf := &HeuristicValueFunction{}
	for i, c := range candidates {
		mode := ClassifySpeculation(c.ToolName, speculationCeil)
		switch mode {
		case SpecBlocked:
			candidates[i].Score = -1 // pruned
			fmt.Fprintf(os.Stderr, "[MultiBranch] Candidate %d (%s) PRUNED — tool %s above L%d\n",
				i, c.Action, c.ToolName, speculationCeil)

		case SpecImagined:
			output, err := ImagineToolOutput(ctx, c.ToolName, c.Args)
			if err != nil {
				candidates[i].Score = -1
				continue
			}
			candidates[i].Output = output
			score, _ := vf.Score(ctx, c, output, goalPrompt)
			candidates[i].Score = score

		case SpecReal:
			// In the ready queue context, real tools would execute in shadow state.
			// Here we score based on the candidate's self-assessment + goal alignment.
			candidates[i].Output = fmt.Sprintf("(pending real execution for %s)", c.ToolName)
			score, _ := vf.Score(ctx, c, candidates[i].Output, goalPrompt)
			candidates[i].Score = score
		}
	}

	// Step 3: Select best
	best := selectBestCandidate(candidates)
	if best.Score <= 0 {
		fmt.Fprintf(os.Stderr, "[MultiBranch] All candidates pruned for node %s — falling back to single-shot\n", node.ID)
		return nil, nil
	}

	fmt.Fprintf(os.Stderr, "[MultiBranch] Winner for node %s: %s (tool=%s, score=%.3f)\n",
		node.ID, best.Action, best.ToolName, best.Score)
	return &best, nil
}

