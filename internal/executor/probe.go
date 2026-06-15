package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

// ProbeInferenceEngine abstracts the inference call for testability.
// In production, this wraps InferenceBackend.CallModel. In tests,
// a mock returns canned GBNF-constrained JSON responses.
type ProbeInferenceEngine interface {
	Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error)
}

// DefaultProbeInference wraps the global inference backend for production use.
type DefaultProbeInference struct{}

func (d *DefaultProbeInference) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	backend := inference.ActiveBackend
	if backend == nil {
		return "", fmt.Errorf("no active inference backend")
	}
	result, err := backend.CallModel(ctx, []inference.InferenceMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}}, jsonSchema)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

// ThoughtChainStep is the GBNF-constrained JSON output schema for each
// Thought Chain inference step. The Local Model must produce output
// conforming to this schema on every call.
type ThoughtChainStep struct {
	Action      string                 `json:"action"`              // "tool_call" | "synthesize"
	Tool        string                 `json:"tool,omitempty"`      // tool name (when action == "tool_call")
	Arguments   map[string]interface{} `json:"arguments,omitempty"` // tool arguments JSON map
	NextThought string                 `json:"nextThought"`         // reasoning for the next step
	Confidence  float64                `json:"confidence"`          // 0.0 - 1.0 convergence signal
	Synthesis   string                 `json:"synthesis,omitempty"` // final output (when action == "synthesize")
}

// UnmarshalJSON implements custom unmarshaling to handle arguments as either a JSON string or a JSON object.
func (t *ThoughtChainStep) UnmarshalJSON(data []byte) error {
	type Alias ThoughtChainStep
	aux := struct {
		Arguments interface{} `json:"arguments"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Arguments != nil {
		switch v := aux.Arguments.(type) {
		case map[string]interface{}:
			t.Arguments = v
		case string:
			if v != "" {
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(v), &parsed); err != nil {
					t.Arguments = map[string]interface{}{"query": v}
				} else {
					t.Arguments = parsed
				}
			}
		}
	}
	return nil
}

// ThoughtChainStepSchema is the JSON schema that constrains Local Model
// output for each Thought Chain step via GBNF grammar.
const ThoughtChainStepSchema = `{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["tool_call", "synthesize"]
		},
		"tool": { "type": "string" },
		"arguments": { "type": "string" },
		"nextThought": { "type": "string" },
		"confidence": { "type": "number" },
		"synthesis": { "type": "string" }
	},
	"required": ["action", "nextThought", "confidence"]
}`

// RunProbe executes a Probe Node's Thought Chain loop.
//
// Each step:
//  1. Build prompt from goal + latest summary + recent thoughts + tool output
//  2. Call Local Model with GBNF-constrained schema → ThoughtChainStep
//  3. If action == "tool_call": call tool, persist step with output
//  4. If action == "synthesize" && confidence >= 0.9: return synthesis
//  5. Every N steps: rolling compaction summary
//  6. At budget exhaustion: forced synthesis
//
// All steps are persisted to SQLite for durability.
func RunProbe(
	ctx context.Context,
	taskID string,
	probeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
) (string, error) {
	// Defaults
	stepBudget := config.StepBudget
	if stepBudget <= 0 {
		stepBudget = 30
	}
	compactEvery := config.CompactEvery
	if compactEvery <= 0 {
		compactEvery = 5
	}

	// Build allowed tools set for validation
	allowedToolSet := make(map[string]bool)
	for _, t := range config.AllowedTools {
		allowedToolSet[t] = true
	}

	// Build dynamic GBNF schema for thought step
	var toolEnum string
	if len(config.AllowedTools) > 0 {
		var toolNames []string
		for _, t := range config.AllowedTools {
			toolNames = append(toolNames, fmt.Sprintf("%q", t))
		}
		toolEnum = fmt.Sprintf(`"type": "string", "enum": [%s]`, strings.Join(toolNames, ", "))
	} else {
		toolEnum = `"type": "string"`
	}

	stepSchema := fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["tool_call", "synthesize"]
			},
			"tool": { %s },
			"arguments": { "type": "string" },
			"nextThought": { "type": "string" },
			"confidence": { "type": "number" },
			"synthesis": { "type": "string" }
		},
		"required": ["action", "nextThought", "confidence"]
	}`, toolEnum)

	var lastToolOutput string

	// Loop detection: track recent tool calls to detect degenerate retries
	type recentCall struct {
		tool string
		args string
	}
	var recentCalls []recentCall
	const maxConsecutiveRepeats = 3

	for step := 1; step <= stepBudget; step++ {
		// 1. Build the prompt context
		systemPrompt := buildProbeSystemPrompt(config.Goal, config.AllowedTools)
		userPrompt, err := buildProbeUserPrompt(probeID, step, lastToolOutput)
		if err != nil {
			return "", fmt.Errorf("failed to build probe prompt at step %d: %w", step, err)
		}

		// 2. Call Local Model with GBNF constraint
		rawResponse, err := engine.Infer(ctx, systemPrompt, userPrompt, stepSchema)
		if err != nil {
			return "", fmt.Errorf("probe inference failed at step %d: %w", step, err)
		}

		// 3. Parse the structured response
		var chainStep ThoughtChainStep
		if err := json.Unmarshal([]byte(rawResponse), &chainStep); err != nil {
			return "", fmt.Errorf("failed to parse probe step %d response: %w", step, err)
		}

		// 4. Execute tool call if requested
		toolOutput := ""
		var toolArgsStr string
		if chainStep.Action == "tool_call" && chainStep.Tool != "" {
			// Sanitize tool name: the 4B model sometimes concatenates reasoning
			// into the tool field (e.g. "list_dir_dir_contents_path_or_file...").
			// Try to recover by prefix-matching against allowed tools.
			if !allowedToolSet[chainStep.Tool] {
				sanitized := sanitizeToolName(chainStep.Tool, allowedToolSet)
				if sanitized != "" {
					fmt.Fprintf(os.Stderr, "[Probe] Sanitized tool name: '%s' -> '%s'\n",
						truncate(chainStep.Tool, 50), sanitized)
					chainStep.Tool = sanitized
				}
			}

			// Validate tool is in allowed set
			if !allowedToolSet[chainStep.Tool] {
				toolOutput = fmt.Sprintf("Error: tool '%s' is not in the allowed tools set", chainStep.Tool)
			} else {
				// Parse arguments and call
				args := chainStep.Arguments
				if args == nil {
					args = map[string]interface{}{}
				}
				// Normalize arguments: remap fallback "query" key to the
				// tool's actual required parameter (e.g. "path" for filesystem tools)
				args = normalizeToolArguments(chainStep.Tool, args)
				// Rescue empty path for filesystem tools by extracting from nextThought
				args = rescueEmptyPathFromThought(chainStep.Tool, args, chainStep.NextThought)

				result, err := tools.Call(ctx, chainStep.Tool, args)
				if err != nil {
					toolOutput = fmt.Sprintf("Error: %v", err)
				} else {
					toolOutput = result
				}
				if bytes, err := json.Marshal(args); err == nil {
					toolArgsStr = string(bytes)
				}
			}

			// Loop detection: check for consecutive identical tool calls
			currentCall := recentCall{tool: chainStep.Tool, args: toolArgsStr}
			repeats := 0
			for i := len(recentCalls) - 1; i >= 0; i-- {
				if recentCalls[i] == currentCall {
					repeats++
				} else {
					break
				}
			}
			recentCalls = append(recentCalls, currentCall)

			if repeats >= maxConsecutiveRepeats {
				fmt.Fprintf(os.Stderr, "[Probe] Loop detected: %s called %d times with identical args. Injecting hint.\n", chainStep.Tool, repeats+1)
				lastToolOutput = fmt.Sprintf(
					"LOOP DETECTED: You have called '%s' with identical arguments %d times in a row and it keeps failing or returning the same result. "+
						"You MUST try something DIFFERENT: use a different tool, use different arguments (e.g. an absolute path instead of relative, or a different directory), "+
						"or if you have gathered enough information, set action to 'synthesize' with confidence >= 0.9 to produce your final answer.",
					chainStep.Tool, repeats+1,
				)
			} else {
				lastToolOutput = toolOutput
			}
		}

		// 5. Persist the thought step
		thoughtStep := memory.ThoughtStep{
			ID:         fmt.Sprintf("%s_step_%d", probeID, step),
			ProbeID:    probeID,
			TaskID:     taskID,
			StepIndex:  step,
			Thought:    chainStep.NextThought,
			ToolName:   chainStep.Tool,
			ToolArgs:   toolArgsStr,
			ToolOutput: toolOutput,
			CreatedAt:  time.Now().Unix(),
		}
		if err := memory.DB.AddThoughtStep(thoughtStep); err != nil {
			fmt.Fprintf(os.Stderr, "[Probe] Warning: failed to persist step %d: %v\n", step, err)
		}

		// 6. Check for convergence
		if chainStep.Action == "synthesize" && chainStep.Confidence >= 0.9 {
			fmt.Fprintf(os.Stderr, "[Probe] Node %s converged at step %d (confidence: %.2f)\n", probeID, step, chainStep.Confidence)
			return chainStep.Synthesis, nil
		}

		// 7. Rolling compaction every N steps
		if step%compactEvery == 0 {
			if err := compactThoughtChain(ctx, probeID, taskID, step, compactEvery, engine); err != nil {
				fmt.Fprintf(os.Stderr, "[Probe] Warning: compaction failed at step %d: %v\n", step, err)
			}
		}
	}

	// Budget exhaustion: force synthesis
	fmt.Fprintf(os.Stderr, "[Probe] Node %s budget exhausted (%d steps). Forcing synthesis.\n", probeID, stepBudget)
	return forceSynthesis(ctx, probeID, taskID, engine)
}

// buildProbeSystemPrompt constructs the system prompt for the probe's Local Model call.
// Includes per-tool parameter schemas so the local model knows exactly what arguments
// each tool requires (fixes empty-arguments bug where model omitted required params).
func buildProbeSystemPrompt(goal string, allowedTools []string) string {
	toolList := ""
	for i, t := range allowedTools {
		if i > 0 {
			toolList += ", "
		}
		toolList += t
	}

	// Build per-tool parameter reference by extracting inner schemas
	toolSchemas := buildToolSchemaReference(allowedTools)

	return fmt.Sprintf(`You are a Probe Node — an autonomous code exploration agent.
Your goal: %s

You have access to these tools: [%s]

## Tool Parameter Reference
%s
On each step, produce a JSON object with:
- "action": either "tool_call" (to use a tool) or "synthesize" (to produce a final answer)
- "tool": the tool name (when action is "tool_call")
- "arguments": a JSON object with the tool's required parameters (when action is "tool_call"). ALWAYS include required parameters.
- "nextThought": your reasoning about what to explore next
- "confidence": 0.0-1.0 indicating how confident you are that you have enough information to answer
- "synthesis": your final answer (when action is "synthesize" and confidence >= 0.9)

IMPORTANT: When using "tool_call", you MUST include the "arguments" field with all required parameters as a JSON object. For example, list_dir requires: {"path": "/some/path"}

Be systematic. Build understanding incrementally. Only synthesize when confident.`, goal, toolList, toolSchemas)
}

// buildToolSchemaReference generates a compact reference block describing each tool's
// parameters. Extracts the inner properties from the GBNF schema envelope.
func buildToolSchemaReference(allowedTools []string) string {
	var sb strings.Builder
	for _, toolName := range allowedTools {
		t := tools.GetTool(toolName)
		if t == nil {
			continue
		}
		schemaStr, err := t.GetSchema()
		if err != nil || schemaStr == "" {
			continue
		}

		// Parse the GBNF schema to extract inner properties
		var schema map[string]interface{}
		if json.Unmarshal([]byte(schemaStr), &schema) != nil {
			continue
		}

		// Navigate: properties -> tool_arguments -> properties
		props, _ := schema["properties"].(map[string]interface{})
		if props == nil {
			continue
		}
		toolArgs, _ := props["tool_arguments"].(map[string]interface{})
		if toolArgs == nil {
			continue
		}
		innerProps, _ := toolArgs["properties"].(map[string]interface{})
		if innerProps == nil {
			continue
		}
		requiredList, _ := toolArgs["required"].([]interface{})

		// Build compact parameter listing
		requiredSet := make(map[string]bool)
		for _, r := range requiredList {
			if s, ok := r.(string); ok {
				requiredSet[s] = true
			}
		}

		sb.WriteString(fmt.Sprintf("### %s\n", toolName))
		for paramName, paramVal := range innerProps {
			paramMap, _ := paramVal.(map[string]interface{})
			paramType := "string"
			if paramMap != nil {
				if t, ok := paramMap["type"].(string); ok {
					paramType = t
				}
			}
			reqMarker := ""
			if requiredSet[paramName] {
				reqMarker = " (REQUIRED)"
			}
			sb.WriteString(fmt.Sprintf("- %s: %s%s\n", paramName, paramType, reqMarker))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// buildProbeUserPrompt builds the user prompt from persisted thought chain state.
func buildProbeUserPrompt(probeID string, stepNum int, lastToolOutput string) (string, error) {
	var prompt string

	// Include latest compaction summary if available
	summary, err := memory.DB.GetLatestSummary(probeID)
	if err == nil && summary.Summary != "" {
		prompt += fmt.Sprintf("## Previous Exploration Summary\n%s\n\n", summary.Summary)
	}

	// Include recent unccompacted steps
	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err == nil && len(steps) > 0 {
		// Only include the most recent steps (after last compaction)
		recentStart := 0
		if len(steps) > 5 {
			recentStart = len(steps) - 5
		}
		prompt += "## Recent Steps\n"
		for _, s := range steps[recentStart:] {
			prompt += fmt.Sprintf("- Step %d: %s", s.StepIndex, s.Thought)
			if s.ToolName != "" {
				prompt += fmt.Sprintf(" [used: %s]", s.ToolName)
			}
			prompt += "\n"
		}
		prompt += "\n"
	}

	// Include last tool output
	if lastToolOutput != "" {
		// Cap tool output to prevent context explosion
		if len(lastToolOutput) > 4000 {
			lastToolOutput = lastToolOutput[:4000] + "\n... (truncated)"
		}
		prompt += fmt.Sprintf("## Last Tool Output\n```\n%s\n```\n\n", lastToolOutput)
	}

	prompt += fmt.Sprintf("Step %d: What should we do next?", stepNum)
	return prompt, nil
}

// compactThoughtChain creates a rolling summary of recent thought chain steps.
func compactThoughtChain(ctx context.Context, probeID, taskID string, currentStep, window int, engine ProbeInferenceEngine) error {
	startStep := currentStep - window + 1
	if startStep < 1 {
		startStep = 1
	}

	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err != nil {
		return err
	}

	// Collect steps in the compaction window
	var windowSteps []memory.ThoughtStep
	for _, s := range steps {
		if s.StepIndex >= startStep && s.StepIndex <= currentStep {
			windowSteps = append(windowSteps, s)
		}
	}

	if len(windowSteps) == 0 {
		return nil
	}

	// Build compaction prompt
	var stepsText string
	for _, s := range windowSteps {
		stepsText += fmt.Sprintf("Step %d: %s", s.StepIndex, s.Thought)
		if s.ToolName != "" {
			stepsText += fmt.Sprintf(" → %s(%s) → %s", s.ToolName, s.ToolArgs, truncate(s.ToolOutput, 200))
		}
		stepsText += "\n"
	}

	systemPrompt := "Compress the following exploration steps into a concise summary preserving all key findings and discoveries. Output only the summary text."
	userPrompt := stepsText

	summaryText, err := engine.Infer(ctx, systemPrompt, userPrompt, "")
	if err != nil {
		return fmt.Errorf("compaction inference failed: %w", err)
	}

	summary := memory.ThoughtSummary{
		ID:        fmt.Sprintf("%s_summary_%d_%d", probeID, startStep, currentStep),
		ProbeID:   probeID,
		TaskID:    taskID,
		StepRange: fmt.Sprintf("%d-%d", startStep, currentStep),
		Summary:   summaryText,
		CreatedAt: time.Now().Unix(),
	}

	return memory.DB.AddThoughtSummary(summary)
}

// forceSynthesis generates a forced synthesis when the step budget is exhausted.
func forceSynthesis(ctx context.Context, probeID, taskID string, engine ProbeInferenceEngine) (string, error) {
	// Gather all state
	summary, _ := memory.DB.GetLatestSummary(probeID)
	steps, _ := memory.DB.GetThoughtSteps(probeID)

	var context string
	if summary.Summary != "" {
		context += "Summary: " + summary.Summary + "\n"
	}
	for _, s := range steps {
		context += fmt.Sprintf("Step %d: %s\n", s.StepIndex, s.Thought)
	}

	systemPrompt := "You have exhausted your exploration budget. Based on everything discovered so far, produce a comprehensive synthesis of your findings. Be thorough and include all relevant details."
	result, err := engine.Infer(ctx, systemPrompt, context, "")
	if err != nil {
		return "Probe budget exhausted. Unable to synthesize findings.", nil
	}
	return result, nil
}

// sanitizeToolName attempts to recover a valid tool name from garbled model output.
// The 4B model sometimes concatenates reasoning into the tool field, producing names
// like "list_dir_dir_contents_path_or_file_name_and_path_if_file_is_specified".
// This function finds the longest allowed tool name that appears as a prefix.
func sanitizeToolName(garbled string, allowedTools map[string]bool) string {
	bestMatch := ""
	for toolName := range allowedTools {
		if strings.HasPrefix(garbled, toolName) && len(toolName) > len(bestMatch) {
			bestMatch = toolName
		}
	}
	return bestMatch
}

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// normalizeToolArguments remaps miskeyed arguments based on the tool's schema.
// When the local model emits a bare string as arguments (e.g. "CONTEXT.md"),
// UnmarshalJSON wraps it as {"query": "CONTEXT.md"}. But filesystem tools
// expect {"path": "CONTEXT.md"}. This function detects the mismatch by
// inspecting the tool's schema and remaps the value to the correct key.
func normalizeToolArguments(toolName string, args map[string]interface{}) map[string]interface{} {
	// Only normalize if there's a "query" key that might be a fallback
	queryVal, hasQuery := args["query"]
	if !hasQuery {
		return args
	}

	// Get the tool's schema to find required parameter names
	t := tools.GetTool(toolName)
	if t == nil {
		return args
	}
	schemaStr, err := t.GetSchema()
	if err != nil || schemaStr == "" {
		return args
	}

	var schema map[string]interface{}
	if json.Unmarshal([]byte(schemaStr), &schema) != nil {
		return args
	}

	// Navigate: properties -> tool_arguments -> required
	props, _ := schema["properties"].(map[string]interface{})
	if props == nil {
		return args
	}
	toolArgs, _ := props["tool_arguments"].(map[string]interface{})
	if toolArgs == nil {
		return args
	}
	requiredList, _ := toolArgs["required"].([]interface{})
	if len(requiredList) == 0 {
		return args
	}

	// Find the first required parameter that isn't "query"
	for _, r := range requiredList {
		reqKey, ok := r.(string)
		if !ok || reqKey == "query" {
			continue
		}
		// If the required key is missing from args, remap "query" to it
		if _, exists := args[reqKey]; !exists {
			args[reqKey] = queryVal
			delete(args, "query")
			fmt.Fprintf(os.Stderr, "[Probe] Normalized argument: remapped 'query' -> '%s' for tool '%s'\n", reqKey, toolName)
			break
		}
	}

	return args
}

// rescueEmptyPathFromThought attempts to extract a file/directory path from the
// model's nextThought text when filesystem tool arguments are missing or empty.
// The 4B local model frequently describes what it wants to read in its reasoning
// (e.g., "Read CONTEXT.md", "explore internal/compiler") but fails to populate
// the arguments JSON correctly. This function recovers those paths.
func rescueEmptyPathFromThought(toolName string, args map[string]interface{}, thought string) map[string]interface{} {
	// Only rescue for filesystem tools
	fsTools := map[string]bool{"read_file": true, "list_dir": true, "search_files": true}
	if !fsTools[toolName] {
		return args
	}

	// Check if path is already populated
	if pathVal, exists := args["path"]; exists {
		if pathStr, ok := pathVal.(string); ok && pathStr != "" {
			// Resolve relative paths to absolute
			if !filepath.IsAbs(pathStr) {
				resolved := config.ResolvePath(pathStr)
				if resolved != pathStr {
					fmt.Fprintf(os.Stderr, "[Probe] Resolved relative path: '%s' -> '%s' for tool '%s'\n", pathStr, resolved, toolName)
					args["path"] = resolved
				}
			}
			return args
		}
	}

	// Try to extract a path from the thought text
	extracted := extractPathFromText(thought)
	if extracted != "" {
		// Resolve relative paths to absolute using TZRO_DIR
		if !filepath.IsAbs(extracted) {
			resolved := config.ResolvePath(extracted)
			if resolved != extracted {
				fmt.Fprintf(os.Stderr, "[Probe] Resolved rescued path: '%s' -> '%s' for tool '%s'\n", extracted, resolved, toolName)
				extracted = resolved
			}
		}
		args["path"] = extracted
		fmt.Fprintf(os.Stderr, "[Probe] Rescued empty path from thought: '%s' for tool '%s'\n", extracted, toolName)
	}

	return args
}

// extractPathFromText uses heuristics to find file/directory paths in free text.
// Looks for: absolute paths, quoted names, relative paths with extensions, known directory names.
func extractPathFromText(text string) string {
	if text == "" {
		return ""
	}

	// Priority 1: Absolute paths (e.g., /Users/jp/Desktop/Repos/tzro/CONTEXT.md)
	absPathRe := regexp.MustCompile(`(/[a-zA-Z0-9._\-]+(?:/[a-zA-Z0-9._\-]+)+)`)
	if matches := absPathRe.FindStringSubmatch(text); len(matches) > 1 {
		return matches[1]
	}

	// Priority 2: Quoted or backtick-delimited names (e.g., 'tzro-mcp', `bootstrap.go`, "main.go")
	// This catches bare names the model mentions in reasoning regardless of extension.
	quotedRe := regexp.MustCompile("['\"`]([a-zA-Z0-9_][a-zA-Z0-9_.\\-]*)['\"`]")
	if matches := quotedRe.FindStringSubmatch(text); len(matches) > 1 {
		candidate := matches[1]
		// Exclude common English words and meta-terms that appear in quotes
		exclusions := map[string]bool{"path": true, "query": true, "error": true, "tool": true, "arguments": true, "file": true, "directory": true}
		if !exclusions[candidate] {
			return candidate
		}
	}

	// Priority 3: Filenames with extensions (e.g., CONTEXT.md, go.mod, main.go)
	fileRe := regexp.MustCompile(`\b([a-zA-Z0-9_\-]+\.[a-zA-Z]{1,10})\b`)
	if matches := fileRe.FindStringSubmatch(text); len(matches) > 1 {
		return matches[1]
	}

	// Priority 4: Known directory patterns (e.g., internal/compiler, cmd/tzro)
	dirRe := regexp.MustCompile(`\b((?:internal|cmd|pkg|plugins|tests|docs)/[a-zA-Z0-9_\-/]+)\b`)
	if matches := dirRe.FindStringSubmatch(text); len(matches) > 1 {
		return matches[1]
	}

	// Priority 5: Bare filenames with hyphens (e.g., tzro-mcp, llama-server)
	// These are common executable/project names the model refers to without quotes.
	bareHyphenRe := regexp.MustCompile(`\b([a-zA-Z][a-zA-Z0-9]*(?:-[a-zA-Z0-9]+)+)\b`)
	if matches := bareHyphenRe.FindStringSubmatch(text); len(matches) > 1 {
		candidate := matches[1]
		// Exclude common non-path hyphenated phrases
		exclusions := map[string]bool{"tool-call": true, "read-file": true, "list-dir": true, "next-step": true}
		if !exclusions[candidate] {
			return candidate
		}
	}

	// Priority 6: Bare known directory names
	bareDirRe := regexp.MustCompile(`\b(internal|cmd|pkg|plugins|tests|docs|bin)\b`)
	if matches := bareDirRe.FindStringSubmatch(text); len(matches) > 1 {
		return matches[1]
	}

	return ""
}
