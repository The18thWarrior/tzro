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
	cfgpkg "tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

// ProbeInferenceEngine abstracts the inference call for testability.
// In production, this wraps InferenceBackend.CallModel. In tests,
// a mock returns canned GBNF-constrained JSON responses.
type ProbeInferenceEngine interface {
	Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error)
	// InferMessages sends a pre-segmented message array to the model.
	// This enables KV cache prefix sharing: the system prompt + tool schemas
	// (segment 1) stay identical across probe steps, so the llama-server's
	// --cache-reuse window can skip re-processing those tokens on every step.
	// Returns (content, error). Callers that don't implement this get a
	// default fallback that extracts system+user from the messages slice.
	InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (string, error)
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

// InferMessages sends a pre-segmented message array to maximize KV cache prefix reuse.
func (d *DefaultProbeInference) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (string, error) {
	backend := inference.ActiveBackend
	if backend == nil {
		return "", fmt.Errorf("no active inference backend")
	}
	result, err := backend.CallModel(ctx, messages, jsonSchema)
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
// downstreamBindingKeys lists property names that downstream nodes need from
// this Probe's output (e.g., ["handler_file_path", "handler_name"]). When
// non-empty, the synthesis schema is extended with these keys as required
// string fields so the Response Resolver can extract them deterministically.
//
// All steps are persisted to SQLite for durability.
func RunProbe(
	ctx context.Context,
	taskID string,
	probeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
	downstreamBindingKeys []string,
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

	var lastToolOutput string
	type recentCall struct {
		tool string
		args string
	}
	var recentCalls []recentCall
	const maxConsecutiveRepeats = 3

	// Consecutive error tracking: when 3+ consecutive tool calls return errors
	// (regardless of which tool/args), lower the minimum step budget to allow
	// immediate synthesis instead of burning through the budget on failing calls.
	var consecutiveErrors int
	const maxConsecutiveErrors = 3

	// Futility detection: if ALL of the first N steps return errors with zero
	// successful calls, abort the probe immediately. This prevents burning the
	// entire step budget (15-20 steps × ~10s each) when the probe can't even
	// get started (e.g., wrong directory, no files found, malformed tool calls).
	// Dynamic: scales with step budget so large-budget probes get more recovery
	// attempts (e.g., stepBudget 30 → threshold 7, stepBudget 10 → threshold 5).
	futilityThreshold := stepBudget / 4
	if futilityThreshold < 5 {
		futilityThreshold = 5
	}

	// Diagnostic tracking for futility abort: records tool name and error
	// for each failed step so the abort log shows WHY calls failed.
	type failedDetail struct {
		step   int
		tool   string
		errMsg string
	}
	var failedToolDetails []failedDetail

	// Successful tool call counter: tracks unique successful tool invocations
	// (calls that returned actual content, not errors). Used to adaptively
	// lower the minimum step budget when the probe has made substantial progress.
	var successfulToolCalls int

	// Output fingerprint tracking: detects diminishing information gain during
	// exploration. When 3 consecutive successful tool outputs match existing
	// fingerprints (first 200 chars), the probe lowers minStepBudget to allow
	// synthesis instead of grinding through redundant exploration steps.
	outputFingerprints := make(map[string]bool)
	var consecutiveDuplicateOutputs int
	const maxConsecutiveDuplicateOutputs = 3

	// minStepBudget is the minimum number of steps a probe must take before
	// synthesis is allowed. Prevents premature termination when the model
	// signals readiness after too few exploration steps.
	// Adaptive: uses the lesser of 8 and stepBudget/2, so small test budgets
	// aren't blocked but production budgets (20-30) get a meaningful floor.
	minStepBudget := 8
	if stepBudget/2 < minStepBudget {
		minStepBudget = stepBudget / 2
	}
	if minStepBudget < 1 {
		minStepBudget = 1
	}

	// Pass 1: High-Entropy Tool Loop
	//
	// KV Cache Prefix Sharing (ADR-0021 extension for probes):
	// The system prompt (goal + tool schemas) is identical across all steps.
	// By hoisting it outside the loop, we ensure the llama-server's
	// --cache-reuse 2048 window matches the system message tokens on every
	// step, avoiding ~500-1000 tokens of redundant KV computation per step.
	// Over 20 steps at ~10s each, this can save 3-5s per step (60-100s total).
	systemPrompt := buildProbeSystemPrompt(config.Goal, config.AllowedTools, config.TaskContext)

	for step := 1; step <= stepBudget; step++ {
		userPrompt, err := buildProbeUserPrompt(probeID, step, lastToolOutput)
		if err != nil {
			return "", fmt.Errorf("failed to build probe prompt at step %d: %w", step, err)
		}

		// Build segmented messages to maximize KV cache prefix reuse:
		//   Segment 1 (system): static goal + tool schemas — identical every step
		//   Segment 2 (user→assistant): accumulated context — grows but prefix is stable
		//   Segment 3 (user): per-step volatile query — changes every step
		probeMessages := buildProbeSegmentedMessages(systemPrompt, userPrompt, probeID)

		// Call Local Model WITHOUT constraint. Probe steps are routing decisions
		// (which file/tool to use next) — thinking mode is NOT enabled here
		// because the per-step overhead (10-30s) multiplies across 15-20 steps,
		// adding 150-500s without proportional quality gain.
		//
		// ADR-0043 Mechanism A: Cap generation per step to prevent runaway output
		// (observed: 16K tokens in a single step collapsed all subsequent calls
		// to 0.1 t/s). Synthesis calls remain uncapped.
		stepCtx := context.WithValue(ctx, inference.MaxTokensKey, cfgpkg.GetProbeStepMaxTokens())
		rawResponse, err := engine.InferMessages(stepCtx, probeMessages, "")
		if err != nil {
			return "", fmt.Errorf("probe inference failed at step %d: %w", step, err)
		}

		// Strip <think>...</think> blocks before tag extraction. The thinking
		// content is reasoning noise — we preserve it in NextThought for logging
		// but must not let it interfere with <SYNTHESIZE_READY> or <ACTION> detection.
		cleanedResponse := inference.StripThinkTags(rawResponse)

		var isSynthesisReady bool
		toolOutput := ""
		var toolArgsStr string
		var toolName string
		var chainStep ThoughtChainStep
		chainStep.NextThought = rawResponse // preserve full response including thinking for logs

		if strings.Contains(cleanedResponse, "<SYNTHESIZE_READY>") {
			// Adaptive minimum: allow early synthesis if the probe has made
			// substantial successful progress (successfulToolCalls >= minStepBudget - 2).
			// This prevents forcing counter-productive extra exploration when
			// the model has already gathered enough data.
			adaptiveMinMet := successfulToolCalls >= minStepBudget-2 && successfulToolCalls > 0

			if step < minStepBudget && !adaptiveMinMet {
				fmt.Fprintf(os.Stderr, "[Probe] Node %s signaled synthesis at step %d but minimum is %d (successful calls: %d) — continuing exploration\n", probeID, step, minStepBudget, successfulToolCalls)
				// Treat as a no-op thought step; continue the loop
				chainStep.Action = "tool_call"
				chainStep.NextThought = rawResponse
				lastToolOutput = fmt.Sprintf("Synthesis signal ignored: minimum step budget is %d, currently at step %d. Continue exploring.", minStepBudget, step)
				// Persist the thought step before continuing
				thoughtStep := memory.ThoughtStep{
					ID:         fmt.Sprintf("%s_step_%d", probeID, step),
					ProbeID:    probeID,
					TaskID:     taskID,
					StepIndex:  step,
					Thought:    chainStep.NextThought,
					ToolOutput: lastToolOutput,
					CreatedAt:  time.Now().Unix(),
				}
				if err := memory.DB.AddThoughtStep(thoughtStep); err != nil {
					fmt.Fprintf(os.Stderr, "[Probe Error] Failed to add thought step: %v\n", err)
				}
				continue
			}
			if adaptiveMinMet && step < minStepBudget {
				fmt.Fprintf(os.Stderr, "[Probe] Node %s signaled synthesis at step %d — adaptive minimum met (%d successful calls ≥ %d threshold)\n", probeID, step, successfulToolCalls, minStepBudget-2)
			}
			fmt.Fprintf(os.Stderr, "[Probe] Node %s signaled synthesis readiness at step %d\n", probeID, step)
			isSynthesisReady = true
			chainStep.Action = "synthesize"
		} else {
			// Extract <ACTION> tag from cleaned response (think-tags already stripped)
			actionRe := regexp.MustCompile("(?s)<ACTION>(.*?)</ACTION>")
			matches := actionRe.FindStringSubmatch(cleanedResponse)
			if len(matches) > 1 {
				var parsed struct {
					Tool      string                 `json:"tool"`
					Arguments map[string]interface{} `json:"arguments"`
				}
				if err := json.Unmarshal([]byte(matches[1]), &parsed); err == nil {
					toolName = parsed.Tool
					chainStep.Action = "tool_call"
					chainStep.Tool = parsed.Tool
					chainStep.Arguments = parsed.Arguments
				} else {
					toolOutput = fmt.Sprintf("Error: Failed to parse ACTION JSON: %v", err)
				}
			}
		}

		if chainStep.Action == "tool_call" && toolName != "" {
			if !allowedToolSet[toolName] {
				sanitized := sanitizeToolName(toolName, allowedToolSet)
				if sanitized != "" {
					fmt.Fprintf(os.Stderr, "[Probe] Sanitized tool name: '%s' -> '%s'\n", truncate(toolName, 50), sanitized)
					toolName = sanitized
					chainStep.Tool = sanitized
				}
			}

			if !allowedToolSet[toolName] {
				toolOutput = fmt.Sprintf("Error: tool '%s' is not in the allowed tools set", toolName)
				failedToolDetails = append(failedToolDetails, failedDetail{step: step, tool: toolName, errMsg: toolOutput})
			} else {
				args := chainStep.Arguments
				if args == nil {
					args = map[string]interface{}{}
				}
				args = normalizeToolArguments(toolName, args)
				args = rescueEmptyPathFromThought(toolName, args, chainStep.NextThought)

				result, err := tools.Call(ctx, toolName, args)
				if err != nil {
					toolOutput = fmt.Sprintf("Error: %v", err)
					consecutiveErrors++
					failedToolDetails = append(failedToolDetails, failedDetail{step: step, tool: toolName, errMsg: toolOutput})
				} else {
					toolOutput = result
					// Detect tool-level errors: tools return JSON with "success":false
					// for validation failures, nonexistent paths, etc. (no Go error).
					if isToolError(result) {
						consecutiveErrors++
						failedToolDetails = append(failedToolDetails, failedDetail{step: step, tool: toolName, errMsg: truncate(result, 200)})
					} else {
						consecutiveErrors = 0 // reset on success
						successfulToolCalls++

						// Output fingerprint convergence check (Fix B):
						// Track first 200 chars of each successful output. When
						// 3 consecutive outputs match existing fingerprints,
						// the probe is reading redundant content — unlock synthesis.
						fingerprint := strings.TrimSpace(result)
						if len(fingerprint) > 200 {
							fingerprint = fingerprint[:200]
						}
						if outputFingerprints[fingerprint] {
							consecutiveDuplicateOutputs++
						} else {
							consecutiveDuplicateOutputs = 0
							outputFingerprints[fingerprint] = true
						}

						// When enough exploration has occurred and outputs are repeating,
						// lower minStepBudget to allow synthesis on the next step.
						if consecutiveDuplicateOutputs >= maxConsecutiveDuplicateOutputs &&
							step >= minStepBudget &&
							successfulToolCalls >= compactEvery*2 {
							fmt.Fprintf(os.Stderr, "[Probe] Node %s: %d consecutive duplicate outputs detected at step %d. Lowering min step budget to allow synthesis.\n",
								probeID, consecutiveDuplicateOutputs, step)
							minStepBudget = step // Allow synthesis on the next step
						}
					}
				}
				if bytes, err := json.Marshal(args); err == nil {
					toolArgsStr = string(bytes)
				}
			}

			// Consecutive error detection: if 3+ tool calls in a row return errors,
			// lower the minimum step budget so the probe can synthesize immediately
			// with whatever it has gathered instead of burning through the budget.
			if consecutiveErrors >= maxConsecutiveErrors {
				fmt.Fprintf(os.Stderr, "[Probe] Node %s hit %d consecutive tool errors at step %d. Lowering min step budget to allow synthesis.\n", probeID, consecutiveErrors, step)
				minStepBudget = step // allow synthesis on the very next step
				lastToolOutput = fmt.Sprintf(
					"WARNING: %d consecutive tool calls have failed. You should synthesize your findings using what you have gathered so far. Output <SYNTHESIZE_READY> to produce your final answer.",
					consecutiveErrors,
				)

				// Futility detection: if we're still within the first N steps
				// and have ZERO successful calls, abort immediately rather than
				// burning through the remaining budget. This saves ~150s
				// (15 steps × 10s) when the probe can't get started at all.
				if step <= futilityThreshold && successfulToolCalls == 0 {
					fmt.Fprintf(os.Stderr, "[Probe] FUTILITY ABORT: Node %s has %d/%d initial steps ALL failed with zero successful calls. Aborting probe loop.\n", probeID, step, futilityThreshold)
					for _, d := range failedToolDetails {
						fmt.Fprintf(os.Stderr, "[Probe]   step %d: tool=%s error=%s\n", d.step, d.tool, truncate(d.errMsg, 150))
					}
					// Persist the final step before breaking
					thoughtStep := memory.ThoughtStep{
						ID:         fmt.Sprintf("%s_step_%d", probeID, step),
						ProbeID:    probeID,
						TaskID:     taskID,
						StepIndex:  step,
						Thought:    chainStep.NextThought,
						ToolName:   toolName,
						ToolArgs:   toolArgsStr,
						ToolOutput: "FUTILITY ABORT: All initial tool calls failed. Proceeding to synthesis with available context.",
						CreatedAt:  time.Now().Unix(),
					}
					if err := memory.DB.AddThoughtStep(thoughtStep); err != nil {
						fmt.Fprintf(os.Stderr, "[Probe Error] Failed to add thought step: %v\n", err)
					}
					break // Exit the probe loop → fall through to synthesis
				}

				// Persist the step with the warning
				thoughtStep := memory.ThoughtStep{
					ID:         fmt.Sprintf("%s_step_%d", probeID, step),
					ProbeID:    probeID,
					TaskID:     taskID,
					StepIndex:  step,
					Thought:    chainStep.NextThought,
					ToolName:   toolName,
					ToolArgs:   toolArgsStr,
					ToolOutput: lastToolOutput,
					CreatedAt:  time.Now().Unix(),
				}
				if err := memory.DB.AddThoughtStep(thoughtStep); err != nil {
					fmt.Fprintf(os.Stderr, "[Probe Error] Failed to add thought step: %v\n", err)
				}
				continue
			}

			currentCall := recentCall{tool: toolName, args: toolArgsStr}
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
				fmt.Fprintf(os.Stderr, "[Probe] Loop detected: %s called %d times. Injecting hint.\n", toolName, repeats+1)
				lastToolOutput = fmt.Sprintf("LOOP DETECTED: You have called '%s' with identical arguments %d times. You MUST try something DIFFERENT, or output <SYNTHESIZE_READY>.", toolName, repeats+1)
			} else {
				lastToolOutput = toolOutput
			}
		} else if isSynthesisReady {
			lastToolOutput = "Synthesis readiness signaled."
		} else {
			lastToolOutput = "No valid <ACTION> tag found. You must either output <ACTION>...</ACTION> or <SYNTHESIZE_READY>."
		}

		thoughtStep := memory.ThoughtStep{
			ID:         fmt.Sprintf("%s_step_%d", probeID, step),
			ProbeID:    probeID,
			TaskID:     taskID,
			StepIndex:  step,
			Thought:    chainStep.NextThought,
			ToolName:   toolName,
			ToolArgs:   toolArgsStr,
			ToolOutput: toolOutput,
			CreatedAt:  time.Now().Unix(),
		}
		if err := memory.DB.AddThoughtStep(thoughtStep); err != nil {
			fmt.Fprintf(os.Stderr, "[Probe Error] Failed to add thought step: %v\n", err)
		}

		if isSynthesisReady {
			break
		}

		if step%compactEvery == 0 {
			_ = compactThoughtChain(ctx, probeID, taskID, step, compactEvery, config.CompactionLevel, engine)
		}
	}

	// Pass 2: Structured Synthesis
	fmt.Fprintf(os.Stderr, "[Probe] Node %s executing Pass 2 Synthesis.\n", probeID)
	return runSynthesisPass(ctx, probeID, taskID, config.Goal, engine, downstreamBindingKeys)
}

func runSynthesisPass(ctx context.Context, probeID, taskID, goal string, engine ProbeInferenceEngine, bindingKeys []string) (string, error) {
	summary, _ := memory.DB.GetLatestSummary(probeID)
	steps, _ := memory.DB.GetThoughtSteps(probeID)
	fmt.Fprintf(os.Stderr, "[Probe] Synthesis Pass: probeID=%s, steps=%d, summaryLen=%d\n", probeID, len(steps), len(summary.Summary))

	var contextStr string
	if summary.Summary != "" {
		contextStr += "Summary: " + summary.Summary + "\n"
	}

	// Build synthesis steps for intelligent truncation.
	// TruncateSynthesisContext applies content-aware truncation (code bracket-depth
	// elision, tabular sampling, text middle-out) starting from the oldest tool
	// results, preserving the most recent results intact.
	var synthSteps []SynthesisStep
	for _, s := range steps {
		synthSteps = append(synthSteps, SynthesisStep{
			StepIndex:  s.StepIndex,
			Thought:    s.Thought,
			ToolOutput: s.ToolOutput,
		})
	}
	contextStr += TruncateSynthesisContext(synthSteps)

	// Build the synthesis schema dynamically. When downstream nodes declare
	// dynamic bindings referencing this probe's output (e.g., "probe_id.output.handler_file_path"),
	// we extend the schema with those keys as required string fields. This ensures
	// the GBNF grammar forces the local model to produce structured key-value pairs
	// that the Response Resolver can extract deterministically (Tier 1: recursive_key)
	// instead of falling through to the lossy semantic fallback.
	synthSchema, extractionHint := buildSynthesisSchema(bindingKeys)

	systemPrompt := fmt.Sprintf(`You are the Synthesis Engine for a Probe Node.
Your goal was: %s

You have completed your exploration. Review the findings and produce a comprehensive, structured final answer.%s`, goal, extractionHint)

	result, err := engine.Infer(ctx, systemPrompt, contextStr, synthSchema)
	if err != nil {
		return "Synthesis inference failed: " + err.Error(), nil
	}

	// Return the full JSON result so the Response Resolver can parse binding keys
	// directly from the JSON structure via recursive_key search (Tier 1).
	// Previously we extracted only the "synthesis" string field, which discarded
	// all structured binding keys and forced downstream resolution through the
	// lossy semantic fallback (Tier 3).
	if len(bindingKeys) > 0 {
		// Validate the JSON is parseable before returning it raw
		var check map[string]interface{}
		if json.Unmarshal([]byte(result), &check) == nil {
			return result, nil
		}
	}

	// No binding keys or JSON parse failed — extract the synthesis field
	var parsed struct {
		Synthesis string `json:"synthesis"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return result, nil // fallback to raw string if parsing fails
	}
	return parsed.Synthesis, nil
}

// buildSynthesisSchema constructs the GBNF-constrained JSON schema for probe synthesis.
// When bindingKeys is non-empty, the schema is extended with those keys as required
// string fields. Returns the schema string and an extraction hint for the system prompt.
func buildSynthesisSchema(bindingKeys []string) (string, string) {
	if len(bindingKeys) == 0 {
		schema := `{
		"type": "object",
		"properties": {
			"synthesis": { "type": "string" }
		},
		"required": ["synthesis"]
	}`
		return schema, ""
	}

	// Build dynamic schema with binding keys
	properties := `"synthesis": { "type": "string" }`
	required := `"synthesis"`
	var keyList string
	for i, key := range bindingKeys {
		properties += fmt.Sprintf(`, "%s": { "type": "string" }`, key)
		required += fmt.Sprintf(`, "%s"`, key)
		if i > 0 {
			keyList += ", "
		}
		keyList += key
	}

	schema := fmt.Sprintf(`{
		"type": "object",
		"properties": { %s },
		"required": [ %s ]
	}`, properties, required)

	hint := fmt.Sprintf(`

In addition to the "synthesis" field, you MUST also extract and return these specific values as separate JSON fields: [%s].
For each field, extract the most relevant value discovered during exploration. If a value was not found, use an empty string.`, keyList)

	return schema, hint
}

// buildProbeSystemPrompt constructs the system prompt for the probe's Local Model call.
// Includes per-tool parameter schemas so the local model knows exactly what arguments
// each tool requires (fixes empty-arguments bug where model omitted required params).
// taskContext, when non-empty, is pinned above the exploration goal so task requirements
// (e.g., target language, specific APIs) override workspace conventions.
func buildProbeSystemPrompt(goal string, allowedTools []string, taskContext string) string {
	toolList := ""
	for i, t := range allowedTools {
		if i > 0 {
			toolList += ", "
		}
		toolList += t
	}

	toolSchemas := buildToolSchemaReference(allowedTools)

	var taskContextSection string
	if taskContext != "" {
		taskContextSection = fmt.Sprintf(`
## Task Specification (PRIORITY — follow these requirements over workspace conventions)
%s

`, taskContext)
	}

	return fmt.Sprintf(`You are a Probe Node — an autonomous code exploration agent.
%sYour goal: %s

You have access to these tools: [%s]

## Tool Parameter Reference
%s
On each step, reason about what to explore next. 
If you need to use a tool, output an XML tag: <ACTION>{"tool": "tool_name", "arguments": {"param": "value"}}</ACTION>.
If you have gathered enough information and are ready to synthesize a final answer, output <SYNTHESIZE_READY>.

IMPORTANT: Do NOT output markdown JSON blocks for the action, use the raw <ACTION> tag.

Be systematic. Build understanding incrementally.
Exploration strategy: list_dir for structure, search_files for patterns (like grep), read_file for content.
Prefer search_files over browsing directories when looking for types, interfaces, or functions.
If a path fails with "does not exist", DO NOT call list_dir or read_file on that path again. You MUST use search_files to locate the correct file instead of guessing directory names.
Do not assume documentation files describe implementation — verify by reading source code.`, taskContextSection, goal, toolList, toolSchemas)
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

// buildProbeUserPrompt builds the per-step volatile user prompt.
// The accumulated context (compaction summary + recent steps) is now handled
// by buildProbeSegmentedMessages as segment 2-3 of the segmented message format,
// so this function only contains the per-step volatile content: last tool output
// and the step query. This separation enables KV cache prefix sharing.
func buildProbeUserPrompt(probeID string, stepNum int, lastToolOutput string) (string, error) {
	var prompt string

	// Include last tool output
	if lastToolOutput != "" {
		prompt += fmt.Sprintf("## Last Tool Output\n```\n%s\n```\n\n", lastToolOutput)
	}

	prompt += fmt.Sprintf("Step %d: What should we do next?", stepNum)
	return prompt, nil
}

// buildProbeSegmentedMessages constructs a multi-segment message array optimized
// for KV cache prefix sharing across probe steps. The layout mirrors the executor's
// buildSegmentedMessages (ADR-0021) but adapted for probe exploration:
//
//  1. {system, staticSystemPrompt}   — goal + tool schemas; IDENTICAL every step
//  2. {user, accumulatedContext}      — compaction summary + recent steps; stable prefix
//  3. {assistant, ack}               — synthetic turn boundary (only if context exists)
//  4. {user, perStepQuery}           — last tool output + step prompt; changes every step
//
// With --cache-reuse 2048, the llama-server reuses the KV cache for any prefix
// that matches between consecutive requests. Since segment 1 is identical and
// segment 2-3 grows incrementally, most of the prefix is reusable, avoiding
// ~500-1000 tokens of redundant computation per step.
func buildProbeSegmentedMessages(systemPrompt, userPrompt, probeID string) []inference.InferenceMessage {
	var msgs []inference.InferenceMessage

	// Segment 1: Static system prompt (goal + tool schemas) — identical every step
	msgs = append(msgs, inference.InferenceMessage{
		Role:    "system",
		Content: systemPrompt,
	})

	// Segments 2-3: Accumulated context from compaction + recent steps
	// This grows over time but the prefix (earlier compaction summaries) is stable
	var accumulatedCtx strings.Builder
	summary, err := memory.DB.GetLatestSummary(probeID)
	if err == nil && summary.Summary != "" {
		accumulatedCtx.WriteString("## Previous Exploration Summary\n")
		accumulatedCtx.WriteString(summary.Summary)
		accumulatedCtx.WriteString("\n\n")
	}
	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err == nil && len(steps) > 0 {
		recentStart := 0
		if len(steps) > 5 {
			recentStart = len(steps) - 5
		}
		accumulatedCtx.WriteString("## Recent Steps\n")
		for _, s := range steps[recentStart:] {
			accumulatedCtx.WriteString(fmt.Sprintf("- Step %d: %s", s.StepIndex, s.Thought))
			if s.ToolName != "" {
				accumulatedCtx.WriteString(fmt.Sprintf(" [used: %s]", s.ToolName))
			}
			accumulatedCtx.WriteString("\n")
		}
		accumulatedCtx.WriteString("\n")
	}

	if accumulatedCtx.Len() > 0 {
		msgs = append(msgs, inference.InferenceMessage{
			Role:    "user",
			Content: accumulatedCtx.String(),
		})
		msgs = append(msgs, inference.InferenceMessage{
			Role:    "assistant",
			Content: "I have reviewed the exploration context. Ready for the next step.",
		})
	}

	// Segment 4: Per-step volatile content (last tool output + step query)
	msgs = append(msgs, inference.InferenceMessage{
		Role:    "user",
		Content: userPrompt,
	})

	return msgs
}
// compactThoughtChain creates a rolling summary of recent thought chain steps.
// The compactionLevel parameter controls how aggressively tool outputs are
// truncated in the compaction prompt. With CompactAggressive, outputs are
// truncated to 200 chars (legacy behavior). With CompactModerate/CompactPreserve,
// full tool outputs are included.
func compactThoughtChain(ctx context.Context, probeID, taskID string, currentStep, window int, compactionLevel compiler.CompactionLevel, engine ProbeInferenceEngine) error {
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
			if compactionLevel == compiler.CompactAggressive {
				// Legacy behavior: truncate tool output to 200 chars for aggressive compaction
				stepsText += fmt.Sprintf(" → %s(%s) → %s", s.ToolName, s.ToolArgs, truncate(s.ToolOutput, 200))
			} else {
				// Moderate/Preserve: include full tool output so synthesis has actual data
				stepsText += fmt.Sprintf(" → %s(%s) → %s", s.ToolName, s.ToolArgs, s.ToolOutput)
			}
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
	// Include tool outputs with intelligent truncation (same as runSynthesisPass)
	var synthSteps []SynthesisStep
	for _, s := range steps {
		synthSteps = append(synthSteps, SynthesisStep{
			StepIndex:  s.StepIndex,
			Thought:    s.Thought,
			ToolOutput: s.ToolOutput,
		})
	}
	context += TruncateSynthesisContext(synthSteps)

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

// isToolError checks if a tool result string indicates a tool-level error.
// Tools return JSON with "success":false for validation failures, nonexistent
// paths, etc. The Go error return from tools.Call is nil in these cases.
func isToolError(result string) bool {
	// Check for the JSON success field pattern
	if strings.Contains(result, `"success":false`) {
		return true
	}
	// Also catch the "Error: ..." prefix used for disallowed tools and parse failures
	if strings.HasPrefix(result, "Error:") {
		return true
	}
	return false
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
				resolved := cfgpkg.ResolvePath(pathStr)
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
			resolved := cfgpkg.ResolvePath(extracted)
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
