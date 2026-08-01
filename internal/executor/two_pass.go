package executor

// two_pass.go — Two-Pass Tool Extraction for Thought Chain steps (ADR-0064).
//
// Every Thought Chain step (Probe and Recall loops) executes two inference passes:
//   - Pass 1 (Worker, unconstrained): Generate free-text reasoning.
//     Done BEFORE calling extractToolAction.
//   - Pass 2 (Router, GBNF-constrained): Extract the structured action.
//     This is what extractToolAction does.
//
// The GBNF pass always runs — it doubles as a validation layer for malformed
// JSON, missing characters, and hallucinated tool names.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"tzro/internal/inference"
)

// TwoPassActionSchema is the GBNF-constrained JSON schema for the extraction
// pass (Pass 2). Intentionally minimal — just action, tool, arguments.
// The reasoning, confidence, and synthesis fields belong to Pass 1.
const TwoPassActionSchema = `{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["tool_call", "synthesize"]
		},
		"tool": { "type": "string" },
		"arguments": { "type": "object" }
	},
	"required": ["action", "tool", "arguments"]
}`

// extractToolAction runs the GBNF-constrained extraction pass (Pass 2) on
// reasoning output from Pass 1. It determines: call a tool, or synthesize?
//
// Input handling:
//   - If reasoning contains complete <ACTION>...</ACTION> tags: targeted extraction
//     (tag content + surrounding context sent to GBNF).
//   - Otherwise: full reasoning output sent to GBNF.
//
// Returns (action, toolName, args, error). action is "tool_call" or "synthesize".
// For "synthesize", toolName and args are empty.
func extractToolAction(
	ctx context.Context,
	engine ProbeInferenceEngine,
	reasoning string,
	allowedTools []string,
	goal ...string,
) (action string, toolName string, args map[string]interface{}, err error) {

	// Build the GBNF extraction prompt
	toolList := strings.Join(allowedTools, ", ")

	// Check for complete <ACTION> tags — use targeted extraction if present
	extractionInput := buildExtractionInput(reasoning)

	// Inject goal context when available so the router can derive
	// meaningful arguments (e.g., search queries) from the task intent.
	var goalCtx string
	if len(goal) > 0 && goal[0] != "" {
		// Truncate goal to keep extraction prompt compact
		g := goal[0]
		if len(g) > 500 {
			g = g[:500]
		}
		goalCtx = fmt.Sprintf(" The task goal is: %s.", g)
	}

	systemPrompt := fmt.Sprintf(
		"You are a precise action extractor.%s Given the reasoning below, determine the action: "+
			"either 'tool_call' (with a tool name from [%s] and its SPECIFIC arguments — "+
			"do NOT leave arguments empty, extract the actual parameters from the reasoning) "+
			"or 'synthesize' (if the reasoning indicates all information has been gathered). "+
			"Output ONLY the JSON object.", goalCtx, toolList)

	messages := []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: extractionInput},
	}

	// GBNF-constrained inference on the router
	gbnfCtx := context.WithValue(ctx, inference.MaxTokensKey, 512)
	result, err := engine.InferMessages(gbnfCtx, messages, TwoPassActionSchema)
	if err != nil {
		return "", "", nil, fmt.Errorf("two-pass extraction failed: %w", err)
	}

	// Parse the GBNF-constrained output
	var parsed struct {
		Action    string                 `json:"action"`
		Tool      string                 `json:"tool"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return "", "", nil, fmt.Errorf("two-pass extraction parse failed: %w", err)
	}

	if parsed.Action == "synthesize" {
		return "synthesize", "", nil, nil
	}

	if parsed.Tool == "" {
		return "", "", nil, fmt.Errorf("two-pass extraction produced empty tool name")
	}

	return parsed.Action, parsed.Tool, parsed.Arguments, nil
}

// actionTagRe matches complete <ACTION>...</ACTION> tag pairs.
var actionTagRe = regexp.MustCompile(`(?s)<ACTION>(.*?)</ACTION>`)

// buildExtractionInput prepares the input for the GBNF extraction pass.
// If the reasoning contains complete <ACTION> tags, uses targeted extraction
// (tag content + surrounding context). Otherwise, sends the full reasoning.
func buildExtractionInput(reasoning string) string {
	matches := actionTagRe.FindStringSubmatch(reasoning)
	if len(matches) > 1 {
		// Targeted extraction: ACTION tag content + surrounding lines for context
		tagContent := strings.TrimSpace(matches[1])

		// Find the tag position and extract surrounding context (up to 500 chars each side)
		idx := strings.Index(reasoning, "<ACTION>")
		prefix := ""
		if idx > 0 {
			start := idx - 500
			if start < 0 {
				start = 0
			}
			prefix = reasoning[start:idx]
		}

		endIdx := strings.Index(reasoning, "</ACTION>") + len("</ACTION>")
		suffix := ""
		if endIdx < len(reasoning) {
			end := endIdx + 500
			if end > len(reasoning) {
				end = len(reasoning)
			}
			suffix = reasoning[endIdx:end]
		}

		return fmt.Sprintf("Context: %s\nAction tag content: %s\nContext: %s", prefix, tagContent, suffix)
	}

	// No ACTION tags — send full reasoning
	return reasoning
}
