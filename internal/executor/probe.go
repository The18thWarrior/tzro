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
	"tzro/internal/compactor"
	"tzro/internal/compiler"
	cfgpkg "tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/telemetry"
	"tzro/internal/tools"
)

// ModelTarget controls which inference sidecar a probe step routes to.
type ModelTarget int

const (
	TargetAuto   ModelTarget = iota // schema → router, no schema → worker
	TargetWorker                    // explicitly use 4B worker
	TargetRouter                    // explicitly use 1B router
)

// ProbeInferenceEngine abstracts the inference call for testability.
// In production, this wraps InferenceBackend.CallModel. In tests,
// a mock returns canned GBNF-constrained JSON responses.
type ProbeInferenceEngine interface {
	Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error)
	// InferMessages sends a pre-segmented message array to the model.
	// This enables KV cache prefix sharing: the system prompt + tool schemas
	// (segment 1) stay identical across probe steps, so the llama-server's
	// --cache-reuse window can skip re-processing those tokens on every step.
	// Returns (content, error).
	InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error)
}

// ProbeInference unifies worker/router routing into a single implementation.
// TargetAuto: schema → router (1B), no schema → worker (4B).
// TargetWorker: always worker (4B). Used for synthesis, compaction, recall.
// TargetRouter: always router (1B). Used for fast structured extraction.
type ProbeInference struct{}

func (p *ProbeInference) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error) {
	messages := []inference.InferenceMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}}
	return p.InferMessages(ctx, messages, jsonSchema, target)
}

func (p *ProbeInference) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	var result *inference.InferenceResult
	var err error
	switch target {
	case TargetWorker:
		result, err = inference.CallWorker(ctx, messages, jsonSchema)
	case TargetRouter:
		result, err = inference.CallRouter(ctx, messages, jsonSchema)
	default: // TargetAuto
		if jsonSchema == "" {
			result, err = inference.CallWorker(ctx, messages, jsonSchema)
		} else {
			result, err = inference.CallRouter(ctx, messages, jsonSchema)
		}
	}
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

// SynthesisValidationSchema is the GBNF constraint for the Pass 3
// Synthesis Validation Gate. The Worker model evaluates whether the
// Router's synthesis signal is premature and can request more steps.
const SynthesisValidationSchema = `{"type":"object","properties":{"ready":{"type":"boolean"},"reason":{"type":"string"},"additionalSteps":{"type":"integer"}},"required":["ready"]}`

// computeUnusedTools returns tool names from allowedTools that are NOT
// present in usedToolSet. Used by the Synthesis Validation Gate to
// inform the Worker about unexplored capabilities.
func computeUnusedTools(allowedTools []string, usedToolSet map[string]bool) []string {
	var unused []string
	for _, t := range allowedTools {
		if !usedToolSet[t] {
			unused = append(unused, t)
		}
	}
	return unused
}


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
// All steps are persisted to SQLite for durability.
func RunProbe(
	ctx context.Context,
	taskID string,
	probeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
	synthesisEngine ProbeInferenceEngine,
	downstreamBindingKeys []string,
) (string, error) {
	// Direct Synthesis mode (Grilling Decision #3): bypass Thought Chain exploration
	// and run single-shot inference against a pre-compiled context file.
	if config.DirectSynthesis {
		var contextContent string
		if config.ContextFile != "" {
			// File-based Direct Synthesis (original path)
			fmt.Fprintf(os.Stderr, "[Probe] Direct Synthesis mode: reading %s\n", config.ContextFile)
			content, err := os.ReadFile(config.ContextFile)
			if err != nil {
				return "", fmt.Errorf("failed to read context file for Direct Synthesis: %w", err)
			}
			contextContent = string(content)
		} else if config.Goal != "" {
			// ADR-0054: Self-contained Direct Synthesis — prompt IS the context
			fmt.Fprintf(os.Stderr, "[Probe] Direct Synthesis mode: self-contained (inline context)\n")
			contextContent = config.Goal
		} else {
			return "", fmt.Errorf("DirectSynthesis requires either ContextFile or Goal to be set in ProbeConfig")
		}
		systemPrompt := fmt.Sprintf("You are a precise technical writer and systems architect. Your goal: %s\n\nRead the pre-compiled context below and produce a comprehensive, accurate response.", config.Goal)
		userPrompt := fmt.Sprintf("Pre-compiled context:\n%s", contextContent)
		maxTokens := 4096
		if config.MaxTokens > 0 {
			maxTokens = config.MaxTokens
		}
		ctxWithLimit := context.WithValue(ctx, inference.MaxTokensKey, maxTokens)
		result, err := synthesisEngine.Infer(ctxWithLimit, systemPrompt, userPrompt, "", TargetWorker)
		if err != nil {
			return "", fmt.Errorf("Direct Synthesis failed: %w", err)
		}
		return result, nil
	}

	// Defaults
	stepBudget := config.StepBudget
	if stepBudget <= 0 {
		stepBudget = 30
	}
	// compactEvery controls rolling compaction frequency AND the synthesis detail
	// window (Grilling Decision #6). Sourced from ProbeConfig when set;
	// defaults to 3 (a systems constraint for 16K context windows).
	compactEvery := config.CompactEvery
	if compactEvery <= 0 {
		compactEvery = 3
	}

	// ADR-0060: Inject GenerationGuard for all probe inference calls.
	// Probes are the primary source of degenerate repetition (observed: 8,910
	// lines, 131K tokens in update_add_method). The guard aborts streaming
	// generation when it detects character-level or block-level repetition.
	ctx = context.WithValue(ctx, inference.GenerationGuardKey, inference.NewRepetitionGuard())

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
	var emptyQueryCount int // tracks empty web_search query extractions for seeding logic
	const maxConsecutiveErrors = 3

	// P0 (URL Pre-Extraction): When web_search succeeds, deterministically
	// extract all URLs from the JSON result. When web_browse is called with
	// an empty URL, auto-populate from this list instead of injecting a text
	// hint the 4B model ignores (benchmark run 8: 40 empty-URL rejections).
	var discoveredURLs []string
	visitedURLs := make(map[string]bool)
	var emptyURLCount int // tracks consecutive empty web_browse URL extractions

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

	// Pass 3 Synthesis Validation Gate state (ADR-0066):
	// usedToolSet tracks which tool names have been called during exploration.
	// synthesisRejections counts how many times the Worker has overridden the
	// Router's premature synthesis signal. Capped at maxSynthesisRejections.
	usedToolSet := make(map[string]bool)
	edgeToolFirstSeen := make(map[string]bool) // tracks first occurrence per tool for priority scoring
	var synthesisRejections int
	const maxSynthesisRejections = 2

	// Phase gate for analyze/explore nodes (ADR-0053): synthesis requires at
	// least minAnalyticalCalls successful data-gathering calls. For analyze
	// nodes this means compound query tools (count_by, group_by, filter_where,
	// top_n, sql_cached_data). For exploration probes this includes read_file,
	// ensuring the probe actually reads source content rather than just listing
	// directories. A single call is insufficient — the probe must gather
	// substantive data before synthesis is allowed.
	const minAnalyticalCalls = 2
	var analyticalCallCount int
	analyticalTools := map[string]bool{
		"sql_cached_data": true,
		"count_by":        true,
		"group_by":        true,
		"filter_where":    true,
		"top_n":           true,
		"read_file":       true,
	}
	isAnalyze := isAnalyzeConfig(config.AllowedTools)
	// Phase gate only applies to actual analyze nodes (SourceHint="cache"),
	// NOT to regular probes that happen to have cache tools injected at runtime.
	// Without this check, probes that encounter upstream cached data get blocked
	// by the sql_cached_data requirement even though they're not data analysis tasks.
	phaseGateApplies := shouldPhaseGateApply(&config)
	isExtractionGoal := phaseGateApplies && goalImpliesExtraction(config.Goal)

	// Analytical Evidence (ADR-0053): structured raw data from successful
	// sql_cached_data calls, materialized into the task result alongside synthesis.
	// Captured at tool dispatch time — each item contains the SQL query, capped
	// result rows (≤5), and total row count.
	type evidenceItem struct {
		SQL       string                   `json:"sql"`
		Rows      []map[string]interface{} `json:"rows"`
		TotalRows int                      `json:"totalRows"`
		Capped    bool                     `json:"capped"`
	}
	var analyticalEvidence []evidenceItem
	// FM-5: Track the base table total row count from introspect_cache results
	// for Aggregate Row-Count Validation.
	var knownTotalRows int

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
	// Analyze nodes converge faster than probes — 2-3 SQL queries typically
	// produce the complete answer. Use a lower floor to prevent runaway.
	if isAnalyzeConfig(config.AllowedTools) {
		minStepBudget = stepBudget / 4
		if minStepBudget < 2 {
			minStepBudget = 2
		}
	}

	// Pre-load target directory files if PreloadPaths is set.
	// Strategy: Write pre-loaded content to a temp file in the first PreloadPath
	// directory, then add a directive in the goal telling the probe to read it.
	//
	// Why NOT lastToolOutput: Rolling compaction at step 3 destroys the pre-loaded
	// content (observed: 32K chars → 375 char summary for T3 ADRs).
	// Why NOT TaskContext: Bloats system prompt on every step, overwhelming the router.
	// Why temp file: Content enters via read_file (one tool call), survives in the
	// thought chain naturally, and the synthesis detail window preserves recent reads.
	var preloadedContent string
	var preloadCleanup func()
	if len(config.PreloadPaths) > 0 {
		maxChars := config.PreloadMaxChars
		if maxChars <= 0 {
			maxChars = defaultPreloadMaxChars
		}

		// Fix 3: Compute full content first (no budget) to detect truncation.
		// If the full content exceeds maxChars, auto-promote to DirectSynthesis
		// instead of truncating — this prevents probes from missing files in
		// large directories (e.g., 55+ ADR files exceeding 32K budget).
		//
		// ADR-0058: Hard-cap DirectSynthesis promotion at 200K chars.
		// Content above this threshold overwhelms the 4B model's single-shot
		// synthesis capacity (observed: 2.1M chars → 910 char vacuous summary).
		// Fall through to Thought Chain with truncated preload instead.
		const maxDirectSynthesisChars = 200_000
		fullContent := preloadDirectoryContext(config.PreloadPaths, 10*1024*1024) // 10MB ceiling
		if len(fullContent) > 0 && len(fullContent) <= maxDirectSynthesisChars && !config.DirectSynthesis {
			// Content fits DirectSynthesis — always promote (even if within preload budget).
			// Guard: len > 0 prevents promotion with empty context when preloadDirectoryContext
			// returns nothing (e.g., directories with only CSV/non-code files).
			contextFile := filepath.Join(config.PreloadPaths[0], ".preload_context_full.md")
			if err := os.WriteFile(contextFile, []byte(fullContent), 0644); err == nil {
				config.DirectSynthesis = true
				config.ContextFile = contextFile
				preloadCleanup = func() {
					os.Remove(contextFile)
				}
				fmt.Fprintf(os.Stderr, "[Probe] Preload content (%d chars) exceeds budget (%d) — auto-promoting to DirectSynthesis via %s\n",
					len(fullContent), maxChars, contextFile)
			}
		} else if len(fullContent) > maxDirectSynthesisChars && !config.DirectSynthesis {
			// Content exceeds DirectSynthesis cap — fall through to Thought Chain
			// with truncated preload. The Thought Chain + Compactor handles large
			// content via rolling summarization; DirectSynthesis does not.
			fmt.Fprintf(os.Stderr, "[Probe] Preload content (%d chars) exceeds DirectSynthesis cap (%d) — using Thought Chain with truncated preload\n",
				len(fullContent), maxDirectSynthesisChars)
			preloadedContent = fullContent[:maxChars]
			preloadFile := filepath.Join(config.PreloadPaths[0], ".preload_context.md")
			if err := os.WriteFile(preloadFile, []byte(preloadedContent), 0644); err == nil {
				config.Goal = fmt.Sprintf("%s\n\nIMPORTANT: Start by reading the pre-compiled source context file at '%s' — it contains source files from the target directories. Read it FIRST before any other exploration.", config.Goal, preloadFile)
				preloadCleanup = func() {
					os.Remove(preloadFile)
				}
				fmt.Fprintf(os.Stderr, "[Probe] Pre-loaded %d chars (truncated from %d) into %s\n", len(preloadedContent), len(fullContent), preloadFile)
			}

		} else if fullContent != "" {
			// Content fits within budget — use normal preload flow
			preloadedContent = fullContent
			if len(preloadedContent) > maxChars {
				preloadedContent = preloadedContent[:maxChars]
			}
			// Write to temp file in the first preload directory
			preloadFile := filepath.Join(config.PreloadPaths[0], ".preload_context.md")
			if err := os.WriteFile(preloadFile, []byte(preloadedContent), 0644); err == nil {
				// Add directive to goal telling probe to read the preload file first
				config.Goal = fmt.Sprintf("%s\n\nIMPORTANT: Start by reading the pre-compiled source context file at '%s' — it contains all source files from the target directories concatenated together. Read it FIRST before any other exploration.", config.Goal, preloadFile)
				preloadCleanup = func() {
					os.Remove(preloadFile)
				}
				fmt.Fprintf(os.Stderr, "[Probe] Pre-loaded %d chars from %d directories into %s\n", len(preloadedContent), len(config.PreloadPaths), preloadFile)
			}
		}
	}
	if preloadCleanup != nil {
		defer preloadCleanup()
	}

	// ADR-0058: Initialize Exploration Queue from PreloadPaths for deterministic
	// loop-breaking. When a duplicate read_file is detected, redirect to the next
	// unvisited file instead of injecting a text hint the model ignores.
	var explorationQueue *ExplorationQueue
	if len(config.PreloadPaths) > 0 {
		queueFiles := collectPreloadFiles(config.PreloadPaths)
		if len(queueFiles) > 0 {
			explorationQueue = NewExplorationQueue(queueFiles)
			fmt.Fprintf(os.Stderr, "[Probe] Exploration Queue initialized with %d files for %s\n", len(queueFiles), probeID)
		}
	}

	// Pass 1: High-Entropy Tool Loop
	//
	// ADR-0059: Fixed-Context-Window with Edge Entry Accumulation
	//
	// Each step receives exactly two messages: the static system prompt
	// (goal + upstream context + tool schemas) and a user message
	// (breadcrumbs + last tool output + step query). The system message
	// is byte-identical across all steps, enabling perfect KV cache prefix
	// reuse with zero prefill overhead after step 1.
	//
	// Accumulated findings are captured as EdgeEntry structs (tool name,
	// args, deterministically truncated result snippet) and compiled for
	// synthesis at loop termination. No in-loop compaction calls.
	//
	// Select system prompt based on node type.
	// Analyze nodes (identified by cache tools in allowedTools) get a data analysis
	// prompt; probe nodes get the codebase exploration prompt.

	// Cap upstream context before baking into system prompt (ADR-0059).
	// Analyze nodes use a tighter cap (12K) because the cacheId is extracted
	// deterministically — the raw upstream text is supplementary context.
	// Probe nodes keep 24K for richer codebase exploration context.
	var cappedUpstreamCtx string
	if config.UpstreamContext != "" {
		isAnalyze := isAnalyzeConfig(config.AllowedTools)
		maxUpstreamContextChars := 24576 // ~6K tokens (probes)
		if isAnalyze {
			maxUpstreamContextChars = 12288 // ~3K tokens (analyze — cacheId extracted separately)
		}
		cappedUpstreamCtx = config.UpstreamContext
		if len(cappedUpstreamCtx) > maxUpstreamContextChars {
			cappedUpstreamCtx = compactor.CompactContent(cappedUpstreamCtx, maxUpstreamContextChars)
			fmt.Fprintf(os.Stderr, "[Probe] Capped upstream context from %d to %d chars for probe %s\n", len(config.UpstreamContext), maxUpstreamContextChars, probeID)
		}
	}

	var systemPrompt string
	if isAnalyzeConfig(config.AllowedTools) {
		// Fix 9: Deterministic cacheId extraction — extract real cacheIds from
		// upstream context using regex so the model doesn't have to find them
		// in the text blob (which causes fabrication like 'probe_leads_csv').
		cacheIdRe := regexp.MustCompile(`cache_\d{10,}`)
		extractedCacheIds := cacheIdRe.FindAllString(config.UpstreamContext, -1)
		// Deduplicate
		seen := make(map[string]bool)
		var uniqueCacheIds []string
		for _, id := range extractedCacheIds {
			if !seen[id] {
				seen[id] = true
				uniqueCacheIds = append(uniqueCacheIds, id)
			}
		}
		if len(uniqueCacheIds) > 0 {
			fmt.Fprintf(os.Stderr, "[Probe] Deterministic cacheId extraction for %s: %v\n", probeID, uniqueCacheIds)
		}
		systemPrompt = buildAnalyzeSystemPrompt(config.Goal, config.AllowedTools, config.TaskContext, uniqueCacheIds, cappedUpstreamCtx, isExtractionGoal)
	} else {
		systemPrompt = buildProbeSystemPrompt(config.Goal, config.AllowedTools, config.TaskContext, cappedUpstreamCtx)
	}

	// ADR-0059: Edge Entry accumulator. Each successful tool call appends
	// an entry with tool-type-aware truncation. Compiled for synthesis.
	var edgeEntries []EdgeEntry

	// Context budget for lastToolOutput capping.
	// Use exact tokenization when available, fallback to char heuristic.
	// The router context size is memory-gated (64K on ≥16GB, 16K on <16GB).
	routerCtxSize := inference.GlobalRouterModel.GetActiveContextSize()

	// Tokenize the system prompt once (it's static across all steps).
	systemPromptTokens, _ := inference.GlobalRouterModel.TokenizeContent(systemPrompt)
	// Reserve 10% of context for generation output + safety margin.
	maxPromptTokens := int(float64(routerCtxSize) * 0.90)
	availableForUser := maxPromptTokens - systemPromptTokens
	if availableForUser < 1024 {
		availableForUser = 1024 // Minimum viable user message budget
	}
	// Convert token budget to approximate char budget for pre-capping.
	// Use 4:1 as a conservative estimate (actual ratio may be lower for CSV data).
	contextBudgetChars := availableForUser * 4


	for step := 1; step <= stepBudget; step++ {
		// ADR-0059: Build fixed-context-window prompt per step.
		// Two messages: [system, user(breadcrumbs + lastToolOutput + query)]
		// System prompt is byte-identical every step → perfect KV cache hit.
		var stepContent strings.Builder

		// Inject breadcrumbs for routing memory (ADR-0059)
		breadcrumbs := BuildBreadcrumbs(edgeEntries, stepBudget, isAnalyze)
		if breadcrumbs != "" {
			stepContent.WriteString(breadcrumbs)
			stepContent.WriteString("\n")
		}

		if lastToolOutput != "" {
			// Cap lastToolOutput to remaining context budget (ADR-0059)
			cappedOutput := lastToolOutput
			maxOutput := contextBudgetChars - len(breadcrumbs) - 200
			if maxOutput < 1000 {
				maxOutput = 1000
			}
			if len(cappedOutput) > maxOutput {
				cappedOutput = compactor.CompactContent(cappedOutput, maxOutput)
			}
			stepContent.WriteString(fmt.Sprintf("## Last Tool Output\n```\n%s\n```\n\n", cappedOutput))
		}
		stepContent.WriteString(fmt.Sprintf("Step %d: What should we do next?", step))

		// ADR-0059: Fixed two-message array per step.
		stepMessages := []inference.InferenceMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: stepContent.String()},
		}

		// Pre-flight token check: verify the assembled prompt fits within n_ctx.
		// If it would exceed 90% of the context window, re-truncate lastToolOutput.
		userTokens, _ := inference.GlobalRouterModel.TokenizeContent(stepContent.String())
		totalPromptTokens := systemPromptTokens + userTokens
		if totalPromptTokens > maxPromptTokens {
			// Re-truncate: shrink the user message to fit
			excessTokens := totalPromptTokens - maxPromptTokens
			excessChars := excessTokens * 4 // Conservative char estimate
			newMaxOutput := len(stepContent.String()) - excessChars - 200
			if newMaxOutput < 500 {
				newMaxOutput = 500
			}
			// Rebuild step content with tighter output cap
			var rebuiltContent strings.Builder
			if breadcrumbs != "" {
				rebuiltContent.WriteString(breadcrumbs)
				rebuiltContent.WriteString("\n")
			}
			if lastToolOutput != "" {
				cappedOutput := compactor.CompactContent(lastToolOutput, newMaxOutput)
				rebuiltContent.WriteString(fmt.Sprintf("## Last Tool Output\n```\n%s\n```\n\n", cappedOutput))
			}
			rebuiltContent.WriteString(fmt.Sprintf("Step %d: What should we do next?", step))
			stepMessages[1].Content = rebuiltContent.String()
			fmt.Fprintf(os.Stderr, "[Probe] Pre-flight truncation: %d tokens exceeded %d limit, re-capped output for step %d\n", totalPromptTokens, maxPromptTokens, step)
		}

		// Call Local Model WITHOUT constraint. Probe steps are routing decisions
		// (which file/tool to use next) — thinking mode is NOT enabled here
		// because the per-step overhead (10-30s) multiplies across 15-20 steps,
		// adding 150-500s without proportional quality gain.
		//
		// ADR-0043 Mechanism A: Cap generation per step to prevent runaway output
		// (observed: 16K tokens in a single step collapsed all subsequent calls
		// to 0.1 t/s). Synthesis calls remain uncapped.
		stepCtx := context.WithValue(ctx, inference.MaxTokensKey, cfgpkg.GetProbeStepMaxTokens())
		rawResponse, err := engine.InferMessages(stepCtx, stepMessages, "", TargetAuto)
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

		// ADR-0064: Two-Pass Tool Extraction (Pass 2).
		// Pass 1 (unconstrained reasoning) was the InferMessages call above.
		// Pass 2 (GBNF-constrained extraction) determines the action.
		//
		// Grammar-level synthesis gate: when the probe hasn't met its minimum
		// step budget, remove "synthesize" from the GBNF enum entirely. The 1B
		// Router physically cannot output "synthesize" — it must extract a
		// tool_call (which auto-seed will populate if args are empty).
		// This fixes the ADR-0065 regression: the Router over-classified Worker
		// reasoning as "synthesize", collapsing edge entries from 63 (R-9) to 0 (R-12).
		adaptiveMinMet := successfulToolCalls >= minStepBudget-2 && successfulToolCalls > 0
		forceTool := step < minStepBudget && !adaptiveMinMet
		extractedAction, extractedTool, extractedArgs, extractErr := extractToolAction(
			ctx, engine, cleanedResponse, config.AllowedTools, forceTool, config.Goal,
		)

		if extractErr != nil {
			// GBNF extraction failed — burn the step
			fmt.Fprintf(os.Stderr, "[Probe] Two-pass extraction failed at step %d: %v\n", step, extractErr)
			toolOutput = fmt.Sprintf("Action extraction failed: %v. Include a clear <ACTION> tag or signal synthesis readiness.", extractErr)
		} else if extractedAction == "synthesize" {
			// Adaptive minimum: allow early synthesis if the probe has made
			// substantial successful progress (successfulToolCalls >= minStepBudget - 2).
			// Note: adaptiveMinMet is computed above (line 655) for forceTool gating.

			// Phase gate (ADR-0053): analyze nodes must have at least
			// minAnalyticalCalls successful sql_cached_data calls before
			// synthesis is allowed.
			cacheFutile := phaseGateApplies && analyticalCallCount == 0 && consecutiveErrors >= maxConsecutiveErrors
			phaseGateBlocked := phaseGateApplies && analyticalCallCount < minAnalyticalCalls && !cacheFutile
			if cacheFutile {
				fmt.Fprintf(os.Stderr, "[Probe] Node %s: cache-futility bypass — %d consecutive errors, 0 successful analytical calls. Allowing synthesis.\n", probeID, consecutiveErrors)
			}

			if step < minStepBudget && !adaptiveMinMet {
				fmt.Fprintf(os.Stderr, "[Probe] Node %s signaled synthesis at step %d but minimum is %d (successful calls: %d) — continuing exploration\n", probeID, step, minStepBudget, successfulToolCalls)
				chainStep.Action = "tool_call"
				lastToolOutput = fmt.Sprintf("Synthesis signal ignored: minimum step budget is %d, currently at step %d. Continue exploring.", minStepBudget, step)
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
			if phaseGateBlocked {
				fmt.Fprintf(os.Stderr, "[Probe] Node %s signaled synthesis at step %d but phase gate blocked — only %d/%d required data query calls. Continuing exploration.\n", probeID, step, analyticalCallCount, minAnalyticalCalls)
				chainStep.Action = "tool_call"
				var phaseGateFeedback string
				if isExtractionGoal {
					phaseGateFeedback = fmt.Sprintf("Synthesis signal ignored: you have only completed %d of %d required data queries. Your goal asks for specific records/fields — run data queries (filter_where, count_by, group_by, top_n) that retrieve the actual columns mentioned in the goal. Do NOT just run COUNT(*) — retrieve the actual data rows the goal asks for.", analyticalCallCount, minAnalyticalCalls)
				} else if containsTool(config.AllowedTools, "read_file") {
					phaseGateFeedback = fmt.Sprintf("Synthesis signal ignored: you have only completed %d of %d required data queries. You must read source files (read_file) or run data queries before synthesizing. Do not just list directories — read the actual file contents.", analyticalCallCount, minAnalyticalCalls)
				} else {
					phaseGateFeedback = fmt.Sprintf("Synthesis signal ignored: you have only completed %d of %d required data queries. Run more data queries (count_by, group_by, filter_where, top_n) with aggregate functions before synthesizing. Use introspect_cache first if you need to see the schema.", analyticalCallCount, minAnalyticalCalls)
				}
				lastToolOutput = phaseGateFeedback
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

			// Pass 3: Synthesis Validation Gate (ADR-0066)
			// The 1B Router signals synthesis prematurely because it can't
			// judge information completeness. Send the context to the 4B Worker
			// with a GBNF-constrained validation schema to get a second opinion.
			if synthesisRejections < maxSynthesisRejections {
				unusedTools := computeUnusedTools(config.AllowedTools, usedToolSet)
				validationSuffix := fmt.Sprintf(
					"\n\nSYNTHESIS VALIDATION: Router signaled synthesis at step %d/%d. "+
						"Successful calls: %d. Tools never used: [%s]. "+
						"Is the exploration complete enough for a comprehensive answer? "+
						"Output {\"ready\": true} to proceed or {\"ready\": false, \"reason\": \"...\", \"additionalSteps\": N} to continue.",
					step, stepBudget, successfulToolCalls, strings.Join(unusedTools, ", "),
				)
				// Use TargetWorker: the 4B model judges completeness better than the 1B router
				validationCtx := context.WithValue(ctx, inference.MaxTokensKey, 256)
				valResult, valErr := engine.Infer(validationCtx, systemPrompt, stepContent.String()+validationSuffix, SynthesisValidationSchema, TargetWorker)
				if valErr == nil {
					var valParsed struct {
						Ready           bool   `json:"ready"`
						Reason          string `json:"reason"`
						AdditionalSteps int    `json:"additionalSteps"`
					}
					if parseErr := json.Unmarshal([]byte(valResult), &valParsed); parseErr == nil && !valParsed.Ready {
						synthesisRejections++
						additionalSteps := valParsed.AdditionalSteps
						if additionalSteps <= 0 || additionalSteps > 4 {
							additionalSteps = 3 // safe default
						}
						minStepBudget = step + additionalSteps
						fmt.Fprintf(os.Stderr, "[Probe] Synthesis Validation Gate REJECTED at step %d (rejection %d/%d): %s. Extended budget by %d steps.\n",
							step, synthesisRejections, maxSynthesisRejections, valParsed.Reason, additionalSteps)
						lastToolOutput = fmt.Sprintf("Synthesis rejected by validation gate: %s. Extended budget by %d steps. Continue exploring.", valParsed.Reason, additionalSteps)
						thoughtStep := memory.ThoughtStep{
							ID:        fmt.Sprintf("%s_step_%d", probeID, step),
							ProbeID:   probeID,
							TaskID:    taskID,
							StepIndex: step,
							Thought:   chainStep.NextThought,
							ToolOutput: lastToolOutput,
							CreatedAt: time.Now().Unix(),
						}
						if err := memory.DB.AddThoughtStep(thoughtStep); err != nil {
							fmt.Fprintf(os.Stderr, "[Probe Error] Failed to add thought step: %v\n", err)
						}
						continue
					}
				} else {
					fmt.Fprintf(os.Stderr, "[Probe] Synthesis Validation Gate inference failed: %v — proceeding with synthesis\n", valErr)
				}
			}

			fmt.Fprintf(os.Stderr, "[Probe] Node %s signaled synthesis readiness at step %d\n", probeID, step)
			isSynthesisReady = true
			chainStep.Action = "synthesize"
		} else if extractedAction == "tool_call" && extractedTool != "" {
			toolName = extractedTool
			chainStep.Action = "tool_call"
			chainStep.Tool = extractedTool
			chainStep.Arguments = extractedArgs

			// Fix 2 (ADR-0064 regression): Post-extraction argument validation.
			// The GBNF schema requires arguments but the router may emit empty
			// values (e.g., {"query": ""} or {}). Reject and inject corrective
			// feedback with the goal context so the model can generate proper args.
			if extractedTool == "web_search" {
				q, _ := extractedArgs["query"].(string)
				if q == "" {
					// Argument seeding: on the first empty-query occurrence,
					// auto-seed from the probe goal instead of burning the step.
					// On subsequent empties, reject with corrective feedback.
					emptyQueryCount++
					if emptyQueryCount <= 2 {
						// Auto-seed: extract a search query from the goal text
						fallbackQuery := extractSearchQueryFromGoal(config.Goal)
						fmt.Fprintf(os.Stderr, "[Probe] Auto-seeded web_search query from goal at step %d: %s\n", step, truncate(fallbackQuery, 80))
						if chainStep.Arguments == nil {
							chainStep.Arguments = make(map[string]interface{})
						}
						chainStep.Arguments["query"] = fallbackQuery
					} else {
						fmt.Fprintf(os.Stderr, "[Probe] Rejected web_search with empty query at step %d (attempt %d) — injecting goal-derived hint\n", step, emptyQueryCount)
						toolOutput = fmt.Sprintf("REJECTED: web_search requires a non-empty 'query' argument. "+
							"Your task goal is: %s. Generate a specific search query based on this goal.",
							truncate(config.Goal, 300))
						toolName = ""
						chainStep.Action = ""
					}
				}
			}
			if extractedTool == "web_browse" {
				u, _ := extractedArgs["url"].(string)
				if u == "" {
					// P0: Auto-populate URL from pre-extracted web_search results
					// instead of injecting a text hint the model ignores.
					autoURL := ""
					for _, candidate := range discoveredURLs {
						if !visitedURLs[candidate] {
							autoURL = candidate
							break
						}
					}
					if autoURL != "" {
						emptyURLCount++
						fmt.Fprintf(os.Stderr, "[Probe] Auto-populated web_browse URL from discovered list at step %d: %s\n", step, truncate(autoURL, 80))
						if chainStep.Arguments == nil {
							chainStep.Arguments = make(map[string]interface{})
						}
						chainStep.Arguments["url"] = autoURL
						visitedURLs[autoURL] = true
					} else {
						emptyURLCount++
						fmt.Fprintf(os.Stderr, "[Probe] Rejected web_browse with empty url at step %d (no unvisited URLs remain) — injecting corrective hint\n", step)
						var visitedList string
						for url := range visitedURLs {
							if visitedList != "" {
								visitedList += ", "
							}
							visitedList += url
						}
						if visitedList != "" {
							toolOutput = fmt.Sprintf("REJECTED: web_browse requires a non-empty 'url' argument. "+
								"All discovered URLs have been visited: [%s]. "+
								"Use web_search with a different query to find new URLs, or synthesize your findings.",
								truncate(visitedList, 300))
						} else {
							toolOutput = fmt.Sprintf("REJECTED: web_browse requires a non-empty 'url' argument. "+
								"Use web_search first to find relevant URLs, then call web_browse with a specific URL.")
						}
						toolName = ""
						chainStep.Action = ""
					}
				}
			}

			// Fix 3 (ADR-0064 regression): Restore SQL auto-extraction for analyze nodes.
			// If the extraction returned sql_cached_data/introspect_cache with empty args,
			// attempt to extract SQL from the Pass 1 reasoning text (regex-based, zero cost).
			if isAnalyze && (extractedTool == "sql_cached_data" || extractedTool == "introspect_cache") {
				if extractedTool == "sql_cached_data" {
					sqlArg, _ := extractedArgs["sql"].(string)
					if sqlArg == "" {
						if autoSQL, cacheTable := extractSQLFromText(cleanedResponse); autoSQL != "" {
							fmt.Fprintf(os.Stderr, "[Probe] SQL auto-extraction enriched empty args: %s (table: %s)\n", truncate(autoSQL, 100), cacheTable)
							if chainStep.Arguments == nil {
								chainStep.Arguments = make(map[string]interface{})
							}
							chainStep.Arguments["sql"] = autoSQL
						} else {
							// Fallback: if sql is still empty, generate a default query
							// using the cacheId (from args or reasoning text).
							cacheId, _ := extractedArgs["cacheId"].(string)
							if cacheId == "" {
								cacheId = extractCacheIdFromText(cleanedResponse)
							}
							if fallbackSQL := defaultSQLForCacheId(cacheId); fallbackSQL != "" {
								fmt.Fprintf(os.Stderr, "[Probe] SQL fallback: empty sql, using default query for %s\n", cacheId)
								if chainStep.Arguments == nil {
									chainStep.Arguments = make(map[string]interface{})
								}
								chainStep.Arguments["sql"] = fallbackSQL
							}
						}
					}
				}
				if extractedTool == "introspect_cache" {
					cacheID, _ := extractedArgs["cacheId"].(string)
					if cacheID == "" {
						// Try to extract cache ID from reasoning text
						cacheIdRe := regexp.MustCompile(`cache_\d{10,}`)
						if match := cacheIdRe.FindString(cleanedResponse); match != "" {
							fmt.Fprintf(os.Stderr, "[Probe] Cache ID auto-extraction enriched empty args: %s\n", match)
							if chainStep.Arguments == nil {
								chainStep.Arguments = make(map[string]interface{})
							}
							chainStep.Arguments["cacheId"] = match
						}
					}
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

				// Inject probe goal into context so read_file can
				// goal-compress large outputs (ADR-0019 extension).
				toolCtx := context.WithValue(ctx, tools.FileReadGoalKey, config.Goal)

				// Inject ExtractedHolder so content extraction can pass
				// rich content (images, vision descriptions) through the
				// tool serialization boundary.
				toolCtx, extractedHolder := tools.NewExtractedCtx(toolCtx)

				// Diagnostic logging for cache tools — helps diagnose SQL generation issues
				if toolName == "sql_cached_data" || toolName == "introspect_cache" {
					if argsJSON, err := json.Marshal(args); err == nil {
						fmt.Fprintf(os.Stderr, "[Probe] Cache tool call: %s args=%s\n", toolName, string(argsJSON))
					}
				}

				result, err := tools.Call(toolCtx, toolName, args)
				if err != nil {
					toolOutput = fmt.Sprintf("Error: %v", err)
					consecutiveErrors++
					failedToolDetails = append(failedToolDetails, failedDetail{step: step, tool: toolName, errMsg: toolOutput})
					// Enhanced cache tool error diagnostics
					if toolName == "sql_cached_data" || toolName == "introspect_cache" {
						fmt.Fprintf(os.Stderr, "[Probe] Cache tool ERROR: %s → %s\n", toolName, toolOutput)
					}
				} else {
					toolOutput = result

					// If the tool produced extracted content (images, PDFs, office docs),
					// enrich the tool output with the human-readable text + image descriptions
					// so the probe model can "see" the content.
					if extractedHolder.Content != nil {
						ec := extractedHolder.Content
						if ec.Text != "" {
							// Prepend extracted text to the JSON tool output so the model
							// gets both the structured data AND the vision/text extraction.
							toolOutput = fmt.Sprintf("[Extracted Content]\n%s\n\n[Tool Output]\n%s", ec.Text, result)
						}
					}

					// Detect tool-level errors: tools return JSON with "success":false
					// for validation failures, nonexistent paths, etc. (no Go error).
					if isToolError(result) {
						consecutiveErrors++
						failedToolDetails = append(failedToolDetails, failedDetail{step: step, tool: toolName, errMsg: truncate(result, 200)})
						// Enhanced cache tool error diagnostics
						if toolName == "sql_cached_data" || toolName == "introspect_cache" {
							fmt.Fprintf(os.Stderr, "[Probe] Cache tool TOOL_ERROR: %s → %s\n", toolName, truncate(result, 300))
						}
					} else {
						consecutiveErrors = 0 // reset on success
						successfulToolCalls++
						usedToolSet[toolName] = true // Pass 3 gate tracking

						// ADR-0059: Accumulate Edge Entry with tool-type-aware truncation.
						// Score priority for overflow eviction (Fix 2, Cluster 3).
						edge := NewEdgeEntry(step, toolName, toolArgsStr, result)
						edge.Priority = ScoreEdgeEntry(toolName, result, edgeToolFirstSeen)
						edgeToolFirstSeen[toolName] = true
						edgeEntries = append(edgeEntries, edge)

						// P0: Extract URLs from successful web_search results.
						// Deterministic extraction — the 4B model cannot reliably
						// parse URLs from search result prose (run 8: 40 failures).
						if toolName == "web_search" {
							extractedURLs := extractURLsFromWebSearch(result)
							if len(extractedURLs) > 0 {
								for _, u := range extractedURLs {
									if !visitedURLs[u] && !containsString(discoveredURLs, u) {
										discoveredURLs = append(discoveredURLs, u)
									}
								}
								fmt.Fprintf(os.Stderr, "[Probe] Extracted %d URLs from web_search results at step %d (total discovered: %d)\n", len(extractedURLs), step, len(discoveredURLs))
							}
						}

						// Mark browsed URLs as visited for P0 deduplication.
						if toolName == "web_browse" {
							if browsedURL, ok := args["url"].(string); ok && browsedURL != "" {
								visitedURLs[browsedURL] = true
							}
						}

						// ADR-0055: Record tool dispatch for Execution Envelope
						if recorder, ok := ctx.Value(DispatchRecorderKey).(func(string, map[string]interface{})); ok {
							recorder(toolName, args)
						}
						// Phase gate + evidence capture (ADR-0053):
						// Track analytical calls and capture evidence for analyze nodes.
						// Counts compound tools (count_by, group_by, filter_where, top_n)
						// and sql_cached_data if it somehow remains in the allowed set.
						if analyticalTools[toolName] {
							analyticalCallCount++
							// Capture evidence: extract SQL (if raw) or tool+args, and result rows
							if isAnalyze {
								var sqlLabel string
								if s, ok := args["sql"].(string); ok && s != "" {
									sqlLabel = s
								} else {
									// For compound tools, build a descriptive label from the tool+args
									compactArgs, _ := json.Marshal(args)
									sqlLabel = fmt.Sprintf("%s(%s)", toolName, string(compactArgs))
								}
								var resultRows []map[string]interface{}
								if err := json.Unmarshal([]byte(result), &resultRows); err == nil {
									const maxEvidenceRows = 5
									capped := len(resultRows) > maxEvidenceRows
									totalRows := len(resultRows)
									if capped {
										resultRows = resultRows[:maxEvidenceRows]
									}
									analyticalEvidence = append(analyticalEvidence, evidenceItem{
										SQL:       sqlLabel,
										Rows:      resultRows,
										TotalRows: totalRows,
										Capped:    capped,
									})
									fmt.Fprintf(os.Stderr, "[Probe] Captured analytical evidence: %s=%s, rows=%d (capped=%v)\n", toolName, truncate(sqlLabel, 80), totalRows, capped)

									// FM-5: Aggregate Row-Count Validation.
									// If the SQL uses aggregate functions (GROUP BY, COUNT, SUM)
									// and we know the base table total from introspect_cache,
									// validate that the aggregate sum doesn't exceed the total.
									if corrective := ValidateAggregateRowCount(sqlLabel, resultRows, knownTotalRows); corrective != "" {
										// Inject corrective feedback into the tool output so the
										// model sees it on the next step and can fix its query.
										toolOutput = toolOutput + "\n\n" + corrective
										// Extend step budget by 1 to give the model room to retry
										stepBudget++
										fmt.Fprintf(os.Stderr, "[Probe] Injected aggregate validation feedback, extended step budget to %d\n", stepBudget)
									}
								}
							}
						}

						// FM-5: Extract total row count from introspect_cache results.
						// The introspect output typically contains "X rows" or a row_count field.
						if toolName == "introspect_cache" && isAnalyze && knownTotalRows == 0 {
							knownTotalRows = extractRowCountFromIntrospect(result)
							if knownTotalRows > 0 {
								fmt.Fprintf(os.Stderr, "[Probe] Extracted base row count from introspect_cache: %d\n", knownTotalRows)
							}
						}
						// FM-5 fallback: Extract base row count from SELECT COUNT(*) results
						// when introspect_cache didn't provide it.
						if toolName == "sql_cached_data" && isAnalyze && knownTotalRows == 0 {
							if sqlArg, ok := args["sql"].(string); ok {
								var countRows []map[string]interface{}
								if json.Unmarshal([]byte(result), &countRows) == nil {
									if extracted := ExtractRowCountFromCountQuery(sqlArg, countRows); extracted > 0 {
										knownTotalRows = extracted
										fmt.Fprintf(os.Stderr, "[Probe] Extracted base row count from COUNT(*) query: %d\n", knownTotalRows)
									}
								}
							}
						}

						// Symbol Extractor hook (ADR-0047): on successful read_file,
						// parse the source via AST and persist extracted declarations
						// to the Symbol Index side-channel table.
						if toolName == "read_file" {
							if filePath, ok := args["path"].(string); ok {
								// ADR-0058: Mark file as visited in Exploration Queue
								if explorationQueue != nil {
									explorationQueue.MarkVisited(filePath)
								}

								var parsedRes struct {
									Content string `json:"content"`
									Path    string `json:"path"`
								}
								if json.Unmarshal([]byte(result), &parsedRes) == nil && parsedRes.Path != "" {
									if explorationQueue != nil {
										explorationQueue.MarkVisited(parsedRes.Path) // Also mark resolved path
									}
									extractAndPersistSymbols(probeID, taskID, parsedRes.Path)
								} else {
									// Fallback: if json parsing fails, try to use filePath directly
									extractAndPersistSymbols(probeID, taskID, filePath)
								}
							}
						}

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
						// either force synthesis (final quarter) or lower the floor (earlier).
						if consecutiveDuplicateOutputs >= maxConsecutiveDuplicateOutputs &&
							step >= minStepBudget &&
							successfulToolCalls >= compactEvery*2 {

							if step >= stepBudget*3/4 {
								// Fix 4 (Cluster 3): Force synthesis in the final quarter.
								// Information gain has plateaued and we're burning steps.
								// Previously we only lowered minStepBudget, but the model
								// kept choosing tool_call over synthesize.
								fmt.Fprintf(os.Stderr, "[Probe] Node %s: FORCED synthesis at step %d — %d consecutive duplicate outputs in final quarter of budget (%d)\n",
									probeID, step, consecutiveDuplicateOutputs, stepBudget)
								isSynthesisReady = true
								break
							}
							// In the first 75%, just lower the floor (existing behavior)
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
				// ADR-0058: For read_file loops, redirect to next unvisited file
				// from the Exploration Queue instead of injecting a text hint.
				if toolName == "read_file" && explorationQueue != nil {
					if nextFile, ok := explorationQueue.NextUnvisited(); ok {
						fmt.Fprintf(os.Stderr, "[Probe] Exploration Queue redirect: %s -> %s\n", toolArgsStr, nextFile)
						// Re-execute with redirected path
						redirectedArgs := map[string]interface{}{"path": nextFile}
						redirectCtx := context.WithValue(ctx, tools.FileReadGoalKey, config.Goal)
						redirectedOutput, redirErr := tools.Call(redirectCtx, toolName, redirectedArgs)
						if redirErr == nil {
							explorationQueue.MarkVisited(nextFile)
							lastToolOutput = redirectedOutput
							// Reset repeat tracking for the redirected call
							recentCalls = recentCalls[:0]
						} else {
							lastToolOutput = fmt.Sprintf("Exploration Queue redirect failed for %s: %v", nextFile, redirErr)
						}
					} else {
						// Queue exhausted — all files visited, use original hint
						visited, total := explorationQueue.Stats()
						lastToolOutput = fmt.Sprintf("LOOP DETECTED: All %d/%d files already visited. Output <SYNTHESIZE_READY> to complete.", visited, total)
					}
				} else {
					fmt.Fprintf(os.Stderr, "[Probe] Loop detected: %s called %d times. Injecting hint.\n", toolName, repeats+1)
					lastToolOutput = fmt.Sprintf("LOOP DETECTED: You have called '%s' with identical arguments %d times. You MUST try something DIFFERENT, or output <SYNTHESIZE_READY>.", toolName, repeats+1)
				}
			} else {
				lastToolOutput = toolOutput
			}
		} else if isSynthesisReady {
			lastToolOutput = "Synthesis readiness signaled."
		} else {
			// Two-pass extraction returned no actionable result — burn the step
			if toolOutput == "" {
				toolOutput = "No valid action extracted from reasoning."
			}
			lastToolOutput = toolOutput
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

		// Emit probe step event for real-time progress tracking in the UI.
		// Uses a compact JSON payload: step index, budget, tool name, and confidence.
		stepPayload := fmt.Sprintf(`{"step":%d,"total":%d,"action":"%s","tool":"%s","confidence":%.2f}`,
			step, stepBudget, chainStep.Action, toolName, chainStep.Confidence)
		telemetry.Default.PublishEvent("probe_step", taskID, probeID, stepPayload)

		// Update node state with step progress so the /api/tasks poll path
		// also reflects real-time activity (belt-and-suspenders with SSE).
		stepStatus := fmt.Sprintf("Step %d/%d: %s", step, stepBudget, toolName)
		if toolName != "" {
			_ = memory.DB.SetNodeState(taskID, nodeIDFromProbeID(probeID, taskID), "running", stepStatus)
		}

		if isSynthesisReady {
			break
		}

		// ADR-0059: No in-loop compaction. Edge Entries accumulate in-memory;
		// compactThoughtChain is retained in the codebase for Recall Node and
		// future consumers but no longer called from the probe loop.
	}

	// ADR-0053: Persist analytical evidence to the database before synthesis.
	// Evidence is stored even if synthesis fails, ensuring the consuming
	// harness always has access to the raw query results.
	if len(analyticalEvidence) > 0 {
		if evidenceJSON, err := json.Marshal(analyticalEvidence); err == nil {
			// Store on the probe's node — the executor will retrieve it
			// at task completion and surface it in the MCP output.
			if err := memory.DB.SetNodeAnalyticalEvidence(taskID, probeID, string(evidenceJSON)); err != nil {
				fmt.Fprintf(os.Stderr, "[Probe] Warning: failed to persist analytical evidence: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "[Probe] Persisted %d analytical evidence items for %s\n", len(analyticalEvidence), probeID)
			}
		}
	}

	// Pass 2: Structured Synthesis (ADR-0059: reads from Edge Entry log)
	fmt.Fprintf(os.Stderr, "[Probe] Node %s executing Pass 2 Synthesis (%d edge entries).\n", probeID, len(edgeEntries))

	// Emit synthesis event so the progress UI can show the transition
	telemetry.Default.PublishEvent("probe_synthesis", taskID, probeID,
		fmt.Sprintf(`{"edgeEntries":%d}`, len(edgeEntries)))
	_ = memory.DB.SetNodeState(taskID, nodeIDFromProbeID(probeID, taskID), "running",
		fmt.Sprintf("Synthesizing (%d findings)", len(edgeEntries)))

	return runSynthesisPass(ctx, probeID, taskID, config.Goal, config.TaskContext, synthesisEngine, downstreamBindingKeys, edgeEntries, preloadedContent, isAnalyze)
}

