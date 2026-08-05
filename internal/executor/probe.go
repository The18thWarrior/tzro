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
	"tzro/internal/symbols"
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
//
// classifyProbeGoal determines the substrate mode from the probe's goal text
// using keyword matching. This is a fast, deterministic fallback when
// SubstrateMode is not set by the planner.
//
// Returns "overview", "focused", "aggregate", or "" (unknown → Thought Chain fallback).
func classifyProbeGoal(goal string) string {
	lower := strings.ToLower(goal)

	// Overview patterns: broad documentation, README, architecture summaries
	overviewKeywords := []string{
		"readme", "overview", "architecture", "project structure",
		"comprehensive", "high-level", "documentation", "describe the project",
		"explain the codebase", "write a readme",
	}
	for _, kw := range overviewKeywords {
		if strings.Contains(lower, kw) {
			return "overview"
		}
	}

	// Aggregate patterns: summarize collections, list items, consolidate
	aggregateKeywords := []string{
		"summarize all", "summarize the", "list all", "aggregate",
		"consolidate", "compile all", "summarize adr", "summarize each",
		"all files in", "every file", "catalog",
	}
	for _, kw := range aggregateKeywords {
		if strings.Contains(lower, kw) {
			return "aggregate"
		}
	}

	// Focused patterns: specific function/module analysis, deep dives
	focusedKeywords := []string{
		"explain how", "trace the", "follow the", "debug",
		"how does", "call graph", "entry point", "specific",
		"deep dive", "detailed analysis of",
	}
	for _, kw := range focusedKeywords {
		if strings.Contains(lower, kw) {
			return "focused"
		}
	}

	return "" // Unknown → Thought Chain fallback
}

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
		ctxWithLimit := context.WithValue(ctx, inference.MaxTokensKey, 4096)
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
	var synthesisRejections int
	const maxSynthesisRejections = 2

	// Phase gate for analyze nodes (ADR-0053): synthesis requires at least
	// minAnalyticalCalls successful sql_cached_data calls. A single sampling
	// query (e.g., SELECT * LIMIT 10) is insufficient — the probe must also
	// run aggregate/analytical queries before synthesis is allowed.
	const minAnalyticalCalls = 2
	var analyticalCallCount int
	isAnalyze := isAnalyzeConfig(config.AllowedTools)
	// Phase gate only applies when sql_cached_data is actually available.
	// Research probes that happen to have introspect_cache but no sql_cached_data
	// should not be blocked by the analytical call requirement.
	phaseGateApplies := isAnalyze && containsTool(config.AllowedTools, "sql_cached_data")
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
		if len(fullContent) <= maxDirectSynthesisChars && !config.DirectSynthesis {
			// Content fits DirectSynthesis — always promote (even if within preload budget).
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
				fmt.Fprintf(os.Stderr, "[Probe] Node %s signaled synthesis at step %d but phase gate blocked — only %d/%d required sql_cached_data calls. Continuing exploration.\n", probeID, step, analyticalCallCount, minAnalyticalCalls)
				chainStep.Action = "tool_call"
				var phaseGateFeedback string
				if isExtractionGoal {
					phaseGateFeedback = fmt.Sprintf("Synthesis signal ignored: you have only completed %d of %d required data queries. Your goal asks for specific records/fields — run sql_cached_data queries that SELECT the actual columns mentioned in the goal (e.g., SELECT name, email FROM table WHERE condition). Do NOT just run COUNT(*) — retrieve the actual data rows the goal asks for.", analyticalCallCount, minAnalyticalCalls)
				} else {
					phaseGateFeedback = fmt.Sprintf("Synthesis signal ignored: you have only completed %d of %d required data queries. Run more sql_cached_data queries with aggregate functions (COUNT, GROUP BY, SUM) before synthesizing. Use introspect_cache first if you need to see the schema.", analyticalCallCount, minAnalyticalCalls)
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
						edgeEntries = append(edgeEntries, NewEdgeEntry(step, toolName, toolArgsStr, result))

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
						if toolName == "sql_cached_data" {
							analyticalCallCount++
							// Capture evidence: extract SQL and result rows
							if isAnalyze {
								var sqlArg string
								if s, ok := args["sql"].(string); ok {
									sqlArg = s
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
										SQL:       sqlArg,
										Rows:      resultRows,
										TotalRows: totalRows,
										Capped:    capped,
									})
									fmt.Fprintf(os.Stderr, "[Probe] Captured analytical evidence: sql=%s, rows=%d (capped=%v)\n", truncate(sqlArg, 80), totalRows, capped)
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

	return runSynthesisPass(ctx, probeID, taskID, config.Goal, synthesisEngine, downstreamBindingKeys, edgeEntries, preloadedContent, isAnalyze)
}

// hybridSynthesisThreshold returns the configured context size (in chars) above
// which synthesis uses a two-phase approach: local outline + cloud polish.
// Reads from config.json ("hybridSynthesisThresholdChars"), defaults to 50000.
func hybridSynthesisThreshold() int {
	return cfgpkg.GetHybridSynthesisThresholdChars()
}

func runSynthesisPass(ctx context.Context, probeID, taskID, goal string, engine ProbeInferenceEngine, bindingKeys []string, edgeEntries []EdgeEntry, preloadedContent string, isAnalyze bool) (string, error) {
	// ADR-0059: Synthesis reads from the Edge Entry log accumulated during the
	// Thought Chain loop. No more reading compaction summaries or raw thought
	// steps from SQLite — the edge log is the authoritative exploration record.
	fmt.Fprintf(os.Stderr, "[Probe] Synthesis Pass: probeID=%s, edgeEntries=%d\n", probeID, len(edgeEntries))

	var contextStr string

	// Inject pre-loaded source material directly into synthesis context.
	// This bypasses the compaction pipeline that would otherwise destroy the content.
	// The pre-loaded content is the GROUND TRUTH source data; the edge entries
	// provide the probe's exploration findings on top of it.
	if preloadedContent != "" {
		// Budget: use at most 16K chars of preloaded content for synthesis
		// to avoid overwhelming the local model's context window.
		const maxPreloadInSynthesis = 16384
		if len(preloadedContent) > maxPreloadInSynthesis {
			preloadedContent = preloadedContent[:maxPreloadInSynthesis] + "\n[... truncated ...]"
		}
		contextStr += "## Source Material (pre-loaded)\n" + preloadedContent + "\n\n"
	}

	// ADR-0059: For Analyze Nodes, also read sql_cached_data results from SQLite
	// ThoughtSteps as a high-fidelity supplement. The edge log has truncated snippets;
	// the raw ThoughtSteps have full query results that are critical for data analysis.
	if isAnalyze {
		steps, _ := memory.DB.GetThoughtSteps(probeID)
		const maxQueryResultsInSynthesis = 12288
		var queryResultsBuf strings.Builder
		for _, s := range steps {
			if (s.ToolName == "sql_cached_data" || s.ToolName == "introspect_cache") && s.ToolOutput != "" {
				// Skip error outputs
				if strings.HasPrefix(s.ToolOutput, "Error:") || strings.HasPrefix(s.ToolOutput, "error:") {
					continue
				}
				queryResultsBuf.WriteString(fmt.Sprintf("### Step %d: %s\nArgs: %s\nResult:\n%s\n\n", s.StepIndex, s.ToolName, s.ToolArgs, s.ToolOutput))
			}
		}
		if queryResultsBuf.Len() > 0 {
			qr := queryResultsBuf.String()
			if len(qr) > maxQueryResultsInSynthesis {
				qr = qr[:maxQueryResultsInSynthesis] + "\n[... query results truncated ...]\n"
			}
			contextStr += "## Query Results (compaction-exempt)\n" + qr + "\n"
			fmt.Fprintf(os.Stderr, "[Probe] Injected %d chars of sql_cached_data/introspect_cache results into synthesis context\n", len(qr))
		}
	}

	// ADR-0059: Compile the Edge Entry log for synthesis.
	// CompileEdgeLog returns the formatted exploration log and whether overflow was detected.
	edgeLog, overflow := CompileEdgeLog(edgeEntries)
	if overflow {
		fmt.Fprintf(os.Stderr, "[Probe] Edge log overflow detected (%d entries, %d chars) — truncated oldest entries\n", len(edgeEntries), len(edgeLog))
	}
	contextStr += edgeLog

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

	// Synthesis needs more output tokens than regular probe steps.
	// The default 2048 truncates content-heavy outputs (e.g., ADR logs).
	synthCtx := context.WithValue(ctx, inference.MaxTokensKey, 4096)

	// Temperature 0.6 for synthesis: sharper distribution reduces repetitive
	// phrasing while min_p 0.1 still provides dynamic token pruning.
	synthCtx = context.WithValue(synthCtx, inference.TemperatureKey, 0.6)

	// DRY (Don't Repeat Yourself) sampling for synthesis: the 4B model reliably
	// degenerates into repetitive phrase loops during synthesis (benchmark runs
	// 10-11: 4-5/5 tasks hit repetitive content detection, e.g., "Consider Using
	// a Security Toolchain" ×115). DRY is sequence-aware — it detects repeated
	// multi-token sequences and applies exponential penalties based on match
	// length. This directly targets phrase-level repetition without degrading
	// code quality or structured output the way frequency_penalty would.
	// Values: multiplier=0.8 (community default), base=1.75, allowed_length=2,
	// full-context lookback (-1), markdown-aware sequence breakers.
	synthCtx = context.WithValue(synthCtx, inference.DRYSamplingKey, inference.DRYSamplingConfig{
		Multiplier:       0.8,
		Base:             1.75,
		AllowedLength:    2,
		PenaltyLastN:     -1,
		SequenceBreakers: []string{"\n", ":", "\"", "*"},
	})

	// P1: Hybrid Synthesis — when context is large, local synthesis reliably
	// fails with repetitive content (benchmark run 8: 100% failure rate).
	// Use a two-phase approach: local model generates a structured outline,
	// cloud model expands it into polished prose.
	if len(contextStr) > hybridSynthesisThreshold() && !isCloudEscalationBlocked() {
		fmt.Fprintf(os.Stderr, "[Probe] Hybrid synthesis triggered: context=%d chars exceeds threshold=%d\n", len(contextStr), hybridSynthesisThreshold())

		// Phase 1: Local outline — the local model is good at organizing
		// and extracting facts, even from large contexts.
		outlinePrompt := fmt.Sprintf(`You are a structured note-taker. Your goal was: %s

Given the exploration findings below, produce a CONCISE STRUCTURED OUTLINE with:
- Section headers for each major topic
- Key bullet points with specific data values, names, and numbers
- Source URLs where available
- NO prose paragraphs — bullet points ONLY
- Include ALL relevant facts discovered during exploration`, goal)

		outline, outlineErr := engine.Infer(synthCtx, outlinePrompt, contextStr, "", TargetWorker)
		if outlineErr == nil && len(strings.TrimSpace(outline)) > 100 {
			fmt.Fprintf(os.Stderr, "[Probe] Hybrid Phase 1 (local outline): %d chars\n", len(outline))

			// Phase 2: Cloud expansion — cloud model expands the outline
			// into polished prose. Low token cost (~500-1K tokens).
			expandPrompt := fmt.Sprintf(`You are the Synthesis Engine for a Probe Node.
Goal: %s

Expand the structured outline below into a comprehensive, well-cited final answer.
Preserve all data values, names, and numbers from the outline.
Add proper prose transitions and paragraph structure.
Include source citations where the outline references URLs.%s`, goal, extractionHint)

			cloudResult, cloudErr := retryWithCloud(ctx, []inference.InferenceMessage{
				{Role: "system", Content: expandPrompt},
				{Role: "user", Content: outline},
			}, synthSchema, taskID)

			if cloudErr == nil && validateSynthesisOutput(cloudResult) == "" {
				fmt.Fprintf(os.Stderr, "[Probe] Hybrid synthesis succeeded: outline=%d chars, expansion=%d chars\n", len(outline), len(cloudResult))
				result := stripControlTokens(cloudResult)
				if len(bindingKeys) > 0 {
					var check map[string]interface{}
					if json.Unmarshal([]byte(result), &check) == nil {
						return result, nil
					}
				}
				var parsed struct {
					Synthesis string `json:"synthesis"`
				}
				if err := json.Unmarshal([]byte(result), &parsed); err != nil {
					return result, nil
				}
				return parsed.Synthesis, nil
			}
			fmt.Fprintf(os.Stderr, "[Probe] Hybrid Phase 2 (cloud expansion) failed or invalid, falling through to standard synthesis\n")
		} else {
			if outlineErr != nil {
				fmt.Fprintf(os.Stderr, "[Probe] Hybrid Phase 1 (local outline) failed: %v, falling through to standard synthesis\n", outlineErr)
			} else {
				fmt.Fprintf(os.Stderr, "[Probe] Hybrid Phase 1 produced degenerate outline (%d chars), falling through to standard synthesis\n", len(strings.TrimSpace(outline)))
			}
		}
	}

	// Build pruned edge evidence for cloud escalation. This uses FullResult
	// (uncompacted web search JSON, full browse content) instead of the truncated
	// ResultSnippets in contextStr. Injected as supplementary context so the cloud
	// model has actual factual data to prevent hallucination.
	const cloudEvidenceBudgetChars = 12288 // ~3K tokens, fits comfortably in cloud context
	prunedEvidence := PruneEdgeContext(edgeEntries, cloudEvidenceBudgetChars)

	// Helper: build cloud retry messages with optional pruned evidence injection.
	buildCloudMessages := func() []inference.InferenceMessage {
		msgs := []inference.InferenceMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: contextStr},
		}
		if prunedEvidence != "" {
			msgs = append(msgs, inference.InferenceMessage{
				Role:    "user",
				Content: "IMPORTANT: The following is the full exploration evidence from the probe. Use ONLY the facts, URLs, and data below. Do NOT invent or hallucinate any details not present in this evidence.\n\n" + prunedEvidence,
			})
			fmt.Fprintf(os.Stderr, "[Probe] Injecting %d chars of pruned edge evidence into cloud escalation\n", len(prunedEvidence))
		}
		return msgs
	}

	// Standard synthesis path (local-try → cloud-fallback on repetition)
	var result string
	var err error
	result, err = engine.Infer(synthCtx, systemPrompt, contextStr, synthSchema, TargetWorker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Probe] Primary synthesis engine failed: %v. Attempting cloud escalation.\n", err)
		if !isCloudEscalationBlocked() {
			cloudResult, cloudErr := retryWithCloud(ctx, buildCloudMessages(), synthSchema, taskID)
			if cloudErr == nil {
				fmt.Fprintf(os.Stderr, "[Probe] Cloud escalation succeeded for synthesis after engine failure (%d chars)\n", len(cloudResult))
				result = cloudResult
			} else {
				return "Synthesis inference failed (primary: " + err.Error() + "; cloud fallback: " + cloudErr.Error() + ")", nil
			}
		} else {
			return "Synthesis inference failed: " + err.Error(), nil
		}
	}

	// Fix 3 (Synthesis Generation Guard): Validate probe synthesis output.
	// Detect control token leaks, degenerate output, and repetitive content.
	// Re-attempt with cloud model on failure (same pattern as ConfidenceTier escalation).
	// ADR-0058: Analyze Node synthesis is exempt from the repetition detector.
	var validationOpts []ValidationOption
	if isAnalyze {
		validationOpts = append(validationOpts, WithAnalyzeNode())
	}
	reason := validateSynthesisOutput(result, validationOpts...)
	if reason != "" {
		fmt.Fprintf(os.Stderr, "[Probe] Synthesis output invalid (%s), escalating to cloud\n", reason)
		if !isCloudEscalationBlocked() {
			cloudResult, cloudErr := retryWithCloud(ctx, buildCloudMessages(), synthSchema, taskID)
		if cloudErr == nil && validateSynthesisOutput(cloudResult, validationOpts...) == "" {
				fmt.Fprintf(os.Stderr, "[Probe] Cloud escalation succeeded for synthesis (%d chars)\n", len(cloudResult))
				result = cloudResult
				// Record escalation as a thought step so downstream Recall nodes
				// can detect that local synthesis was insufficient and default to cloud.
				escalationStep := memory.ThoughtStep{
					ID:        fmt.Sprintf("%s_synthesis_escalation", probeID),
					ProbeID:   probeID,
					TaskID:    taskID,
					StepIndex: -1, // sentinel: not a regular step
					ToolName:  "_cloud_synthesis_escalation",
					CreatedAt: time.Now().Unix(),
				}
				_ = memory.DB.AddThoughtStep(escalationStep)
			}
		}
	}

	// Strip control tokens from the result before downstream processing
	result = stripControlTokens(result)

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
func buildProbeSystemPrompt(goal string, allowedTools []string, taskContext string, upstreamContext string) string {
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

	// ADR-0059: Upstream context baked into system prompt for maximum KV cache reuse.
	var upstreamSection string
	if upstreamContext != "" {
		upstreamSection = fmt.Sprintf(`
## Upstream Node Outputs (from completed DAG steps)
%s

`, upstreamContext)
	}

	return fmt.Sprintf(`You are a Probe Node — an autonomous code exploration agent.
%s%sYour goal: %s

You have access to these tools: [%s]

## Tool Parameter Reference
%s
On each step, reason about what to explore next. 
If you need to use a tool, output an XML tag: <ACTION>{"tool": "tool_name", "arguments": {"param": "value"}}</ACTION>.
If you have gathered enough information and are ready to synthesize a final answer, output <SYNTHESIZE_READY>.

IMPORTANT: Do NOT output markdown JSON blocks for the action, use the raw <ACTION> tag.

Be systematic. Build understanding incrementally.
Exploration strategy: list_dir for structure, read_file for content of files relevant to your goal, search_files for patterns (like grep) to locate specific definitions across multiple files when you do not know the exact filenames.
If you list a directory and see files that are directly related to your goal, read them using read_file directly rather than trying to search or guess. Do not assume search_files is required if you already know which files to read.
Do not read the same file multiple times with overlapping ranges. Once you have read a file, assume you know its structure and move on to the other files in the directory to ensure complete coverage. Do not exhaust your step budget on a single file.
If a path fails with "does not exist", DO NOT call list_dir or read_file on that path again. You MUST use search_files to locate the correct file instead of guessing directory names.
Do not assume documentation files describe implementation — verify by reading source code.`, taskContextSection, upstreamSection, goal, toolList, toolSchemas)
}

// isAnalyzeConfig returns true if the allowed tools contain cache tools,
// indicating this is an analyze node's Thought Chain rather than a probe.
func isAnalyzeConfig(allowedTools []string) bool {
	for _, t := range allowedTools {
		if t == "introspect_cache" || t == "sql_cached_data" {
			return true
		}
	}
	return false
}

// containsTool checks if a specific tool name is in the allowed tools list.
func containsTool(allowedTools []string, tool string) bool {
	for _, t := range allowedTools {
		if t == tool {
			return true
		}
	}
	return false
}

// extractSearchQueryFromGoal derives a web search query from the probe goal text.
// Extracts the first meaningful sentence/clause (up to 100 chars), stripping
// common instruction prefixes like "Search for", "Find", "Research".
// Used as a fallback when the GBNF extraction fails to populate the query field.
func extractSearchQueryFromGoal(goal string) string {
	q := goal
	// Strip common instruction prefixes
	prefixes := []string{
		"Search for ", "search for ",
		"Find ", "find ",
		"Research ", "research ",
		"Look up ", "look up ",
		"Investigate ", "investigate ",
		"Explore ", "explore ",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(q, p) {
			q = q[len(p):]
			break
		}
	}
	// Take the first sentence (up to period, newline, or 100 chars)
	for i, c := range q {
		if c == '.' || c == '\n' {
			q = q[:i]
			break
		}
		if i >= 100 {
			q = q[:i]
			break
		}
	}
	return strings.TrimSpace(q)
}

// goalImpliesExtraction returns true when the goal text suggests the user
// wants specific data fields extracted (names, emails, records) rather than
// computed aggregates (counts, totals, distributions). This biases the
// Probe's SQL queries toward SELECT with specific columns instead of COUNT(*).
//
// Intentionally broad: false positives (treating aggregation as extraction)
// are low-cost because SELECT queries still work for aggregations.
func goalImpliesExtraction(goal string) bool {
	lower := strings.ToLower(goal)
	// Action verbs that imply returning specific records.
	// Note: "return the" is intentionally omitted — it's too broad and
	// matches aggregation goals like "return the top 5 countries".
	extractionVerbs := []string{
		"extract ", "list the ", "list all ", "find the ",
		"show the ", "get the ", "retrieve the ", "look up ", "lookup ",
		"fetch the ", "pull the ", "display the ",
		"find and return", "find all ",
		"return the name", "return the email", "return the record",
		"return the detail", "return the value", "return the data",
		"return their ",
	}
	for _, verb := range extractionVerbs {
		if strings.Contains(lower, verb) {
			return true
		}
	}
	// Field-level nouns that suggest row-level data is needed
	extractionFields := []string{
		"name column", "email column", "names and email",
		"email address", "for each matching", "for each row",
		"each matching row", "each matching lead",
		"for each lead", "for every ",
	}
	for _, field := range extractionFields {
		if strings.Contains(lower, field) {
			return true
		}
	}
	return false
}

// extractionStrategySection returns an additional prompt section that biases
// the Probe toward SELECT queries with specific columns when the goal implies
// field-level data extraction. Returns empty string for aggregation goals.
func extractionStrategySection(extractionMode bool) string {
	if !extractionMode {
		return ""
	}
	return `
## EXTRACTION MODE (your goal asks for specific records/fields)
Your goal asks you to find and return specific data (names, emails, values, etc.).
- PRIORITIZE queries that SELECT the actual columns mentioned in the goal
- Example: SELECT name, email FROM <table> WHERE account_name = 'Target'
- Do NOT waste queries on COUNT(*) alone — the goal needs actual data rows, not just counts
- Your FIRST sql_cached_data call should retrieve the data requested by the goal
- You may run a COUNT query for verification AFTER retrieving the actual data
`
}

// buildAnalyzeSystemPrompt constructs the system prompt for an Analyze Node's
// Thought Chain. Unlike the probe prompt (codebase exploration), this teaches
// the model to use cache exploration tools for data analysis, filtering, and
// aggregation. When no cached data is available, it degrades to synthesis.
func buildAnalyzeSystemPrompt(goal string, allowedTools []string, taskContext string, extractedCacheIds []string, upstreamContext string, isExtractionGoal ...bool) string {
	extractionMode := len(isExtractionGoal) > 0 && isExtractionGoal[0]
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
	// Build the available cacheId section from deterministically extracted IDs
	var cacheIdSection string
	if len(extractedCacheIds) > 0 {
		cacheIdSection = "## AVAILABLE CACHE IDS (use these EXACT strings — do NOT invent your own)\n"
		for _, id := range extractedCacheIds {
			cacheIdSection += fmt.Sprintf("- **%s** — use this as both the cacheId argument and the SQL table name\n", id)
			cacheIdSection += fmt.Sprintf("  Example: introspect_cache({\"cacheId\": \"%s\"})\n", id)
			cacheIdSection += fmt.Sprintf("  Example: sql_cached_data({\"cacheId\": \"%s\", \"sql\": \"SELECT * FROM %s LIMIT 5\"})\n", id, id)
		}
		cacheIdSection += "\nIMPORTANT: Use ONLY the cacheIds listed above. Do NOT fabricate or guess cacheIds.\n"
	} else {
		cacheIdSection = "## CACHE ID DISCOVERY\nNo cacheIds were pre-extracted from upstream context. Check the upstream context for cacheId values, or use introspect_cache with any cacheId you find.\n"
	}

	// Build few-shot examples with real cacheId substitution.
	// The 4B model needs concrete examples of the exact <ACTION> XML format
	// to reliably emit tool calls. Without these, it often generates reasoning
	// text without valid tags (benchmark: 0/15 successful tool calls).
	//
	// IMPORTANT: Only show cache tool examples when real cache IDs are available.
	// When no real IDs exist, omit these examples entirely — if there's no cache
	// to query, showing examples teaches the model to hallucinate cache IDs.
	var fewShotSection string
	if len(extractedCacheIds) > 0 {
		exampleCacheId := extractedCacheIds[0]
		fewShotSection = fmt.Sprintf(`## MANDATORY: Tool Call Format — Follow These Examples EXACTLY

Your FIRST action must be to inspect the cache schema. Output this EXACT format:

Step 1 — Always start here:
<ACTION>{"tool": "introspect_cache", "arguments": {"cacheId": "%s"}}</ACTION>

Step 2 — Count total records:
<ACTION>{"tool": "sql_cached_data", "arguments": {"cacheId": "%s", "sql": "SELECT COUNT(*) as total FROM %s"}}</ACTION>

Step 3 — Group and count by a column:
<ACTION>{"tool": "sql_cached_data", "arguments": {"cacheId": "%s", "sql": "SELECT column_name, COUNT(*) as cnt FROM %s GROUP BY column_name ORDER BY cnt DESC"}}</ACTION>

CRITICAL RULES:
- You MUST wrap every tool call in <ACTION>...</ACTION> tags — no other format works
- You MUST use the exact JSON structure shown above — {"tool": "...", "arguments": {...}}
- Do NOT use markdown code blocks, do NOT describe what you would do — CALL THE TOOL
- If you want data, you MUST call sql_cached_data. Do NOT try to count or aggregate manually from text.
`, exampleCacheId, exampleCacheId, exampleCacheId, exampleCacheId, exampleCacheId)
	} else {
		// No cache data available — show generic ACTION format without cache-specific examples.
		// This prevents the model from hallucinating cache IDs while still teaching the XML format.
		fewShotSection = `## MANDATORY: Tool Call Format

You MUST wrap every tool call in <ACTION>...</ACTION> tags — no other format works.
You MUST use this exact JSON structure: {"tool": "tool_name", "arguments": {"key": "value"}}
Do NOT use markdown code blocks, do NOT describe what you would do — CALL THE TOOL.

No cached data is available for this task. Synthesize your analysis from the text data in the accumulated context above.
`
	}

	// ADR-0059: Upstream context baked into system prompt.
	var upstreamSection string
	if upstreamContext != "" {
		upstreamSection = fmt.Sprintf(`
## Upstream Node Outputs (from completed DAG steps)
%s

`, upstreamContext)
	}

	return fmt.Sprintf(`You are an Analyze Node — an autonomous data analysis agent.
%s%sYour goal: %s

You have access to these tools: [%s]

## Tool Parameter Reference
%s

## Data Analysis Strategy
You analyze data from upstream nodes using a systematic approach:

1. First, check the accumulated context for a cacheId from an upstream data source.
2. If a cacheId is available:
   - Use 'introspect_cache' to understand the data schema (column names, types, sample records)
   - IMPORTANT: Use the EXACT column names from introspect_cache in your SQL queries
   - Use 'sql_cached_data' to query the data using **SQLite** SQL dialect
   - The table name is the cacheId itself
3. If no cacheId is available, synthesize your analysis from the raw text data in the accumulated context.

%s
## CRITICAL: cacheId Handling
The cacheId is an OPAQUE STRING identifier.
- You MUST copy the cacheId EXACTLY as it appears in the examples or context — do NOT round, truncate, or modify the digits
- The cacheId is NOT a number — it is a string. Copy it character-by-character
- Do NOT invent or guess a cacheId — only use IDs that appear in the context above

## SQLite SQL Dialect
The query engine is SQLite. Use SQLite-compatible syntax ONLY:
- String concatenation: Use || not CONCAT()
- GROUP_CONCAT: Use GROUP_CONCAT(col) or GROUP_CONCAT(DISTINCT col) — NO 'SEPARATOR' keyword
- Boolean: Use 1/0, not TRUE/FALSE
- Case-insensitive LIKE is default in SQLite
- LIMIT/OFFSET for pagination (no FETCH/OFFSET)

## Data Quality Best Practices
- ALWAYS start with introspect_cache to see column names, then SELECT COUNT(*) to verify record count
- Check for empty/blank values: SELECT COUNT(*) FROM <table> WHERE ColName IS NULL OR TRIM(ColName) = ''
- Use COALESCE to handle NULLs: SELECT COALESCE(ColName, 'Unspecified') as ColName
- Use TRIM() to clean whitespace: SELECT TRIM(ColName) as ColName
- When grouping text data, first run SELECT DISTINCT ColName to see actual values
- Validate your results: if a GROUP BY total doesn't match the overall COUNT(*), investigate why
- CRITICAL: Run aggregation queries (GROUP BY with COUNT) as a SINGLE complete SQL query
  - Do NOT try to count items incrementally or by hand — let SQL do the counting
  - After grouping, verify: SELECT SUM(cnt) FROM (SELECT COUNT(*) as cnt FROM table GROUP BY col)
    should equal SELECT COUNT(*) FROM table
`+extractionStrategySection(extractionMode)+`

## Text Matching and Filtering
- For exact value lookups, use LIKE with wildcards: WHERE ColName LIKE '%%%%value%%%%'
- For case-insensitive matching: WHERE LOWER(ColName) = LOWER('value')
- When filtering by a company or category name, always try case-insensitive LIKE first

%s
On each step, reason about what analysis to perform next.
If you need to use a tool, output an XML tag: <ACTION>{"tool": "tool_name", "arguments": {"param": "value"}}</ACTION>.
If you have gathered enough information and are ready to synthesize a final answer, output <SYNTHESIZE_READY>.

IMPORTANT: Do NOT output markdown JSON blocks for the action, use the raw <ACTION> tag.

Be systematic. Start by understanding the data schema, then build your analysis incrementally.
If a SQL query returns an error, try a simpler approach or inspect the data with introspect_cache first.`, taskContextSection, upstreamSection, goal, toolList, toolSchemas, cacheIdSection, fewShotSection)
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
		// Include tool description (capped at 100 chars) for semantic context
		desc := t.Description()
		if desc != "" {
			if len(desc) > 100 {
				desc = desc[:97] + "..."
			}
			sb.WriteString(fmt.Sprintf("_%s_\n", desc))
		}
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
func buildProbeSegmentedMessages(systemPrompt, userPrompt, probeID, upstreamContext string) []inference.InferenceMessage {
	var msgs []inference.InferenceMessage

	// Segment 1: Static system prompt (goal + tool schemas) — identical every step
	msgs = append(msgs, inference.InferenceMessage{
		Role:    "system",
		Content: systemPrompt,
	})

	// Fix (ADR-benchmark-data-3): Cap upstream context to prevent overwhelming
	// the local model's context window. When a read_file node returns a 42KB+
	// JSON file, the entire dataProfile gets injected here without any limit,
	// causing validator stalls and probe performance collapse. Use content-aware
	// compaction to preserve structural metadata (cacheId, column names) while
	// trimming raw data rows.
	if upstreamContext != "" {
		const maxUpstreamContextChars = 24576 // ~6K tokens
		if len(upstreamContext) > maxUpstreamContextChars {
			upstreamContext = compactor.CompactContent(upstreamContext, maxUpstreamContextChars)
			fmt.Fprintf(os.Stderr, "[Probe] Capped upstream context from %d to %d chars for probe %s\n", len(upstreamContext), maxUpstreamContextChars, probeID)
		}
		msgs = append(msgs, inference.InferenceMessage{
			Role:    "user",
			Content: "## Upstream Node Outputs (from completed DAG steps)\n" + upstreamContext,
		})
		msgs = append(msgs, inference.InferenceMessage{
			Role:    "assistant",
			Content: "I have reviewed the upstream node outputs. I can see the data context from prior steps.",
		})
	}

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
		// Defense-in-depth: cap accumulated context to fit within the router's 16K
		// context window. Reserve ~3K tokens for system prompt + user prompt.
		// 13K tokens ≈ ~52K chars at ~4 chars/token.
		const maxAccumulatedCtxChars = 52000
		ctxStr := accumulatedCtx.String()
		if len(ctxStr) > maxAccumulatedCtxChars {
			fmt.Fprintf(os.Stderr, "[Probe] Warning: accumulated context (%d chars) exceeds %d limit, truncating\n", len(ctxStr), maxAccumulatedCtxChars)
			ctxStr = ctxStr[:maxAccumulatedCtxChars] + "\n[... context truncated to fit model window ...]\n"
		}
		msgs = append(msgs, inference.InferenceMessage{
			Role:    "user",
			Content: ctxStr,
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

// estimateConversationTokens provides a fast heuristic token count for a message
// array. Uses the ~4 chars/token approximation (standard for English text with
// code and JSON). This is used by the sliding window compaction to decide when
// to drop oldest turns — exact counts aren't needed, just a budget estimate.
func estimateConversationTokens(messages []inference.InferenceMessage) int {
	total := 0
	for _, m := range messages {
		// ~4 chars per token + overhead for role/template tokens
		total += len(m.Content)/4 + 4
	}
	return total
}

// slidingWindowCompact implements the sliding window strategy for append-only
// conversations (ADR-0056). When the estimated token count exceeds the budget,
// it drops the oldest user/assistant turn pairs (after the static prefix)
// while keeping the most recent turns that fit within the budget.
//
// Parameters:
//   - messages: the full conversation history
//   - staticPrefixLen: number of messages in the immutable prefix (system + upstream)
//   - budgetTokens: maximum estimated tokens for the conversation
//
// Returns the compacted message slice. If no compaction is needed, returns the
// original slice unchanged. When compaction occurs, a brief "[N earlier turns
// compacted]" marker is injected after the static prefix.
func slidingWindowCompact(messages []inference.InferenceMessage, staticPrefixLen, budgetTokens int) []inference.InferenceMessage {
	estimated := estimateConversationTokens(messages)
	if estimated <= budgetTokens {
		return messages
	}

	// Static prefix is immutable — only compact dynamic turns
	prefix := messages[:staticPrefixLen]
	dynamic := messages[staticPrefixLen:]

	if len(dynamic) <= 2 {
		// Can't compact further — only 1 turn pair remains
		return messages
	}

	// Drop oldest turn pairs (user + assistant) until we're within budget.
	// Keep dropping pairs from the front of dynamic turns.
	prefixTokens := estimateConversationTokens(prefix)
	droppedCount := 0

	for len(dynamic) > 2 {
		// Estimate tokens for prefix + remaining dynamic
		remainingTokens := prefixTokens
		for _, m := range dynamic {
			remainingTokens += len(m.Content)/4 + 4
		}
		if remainingTokens <= budgetTokens {
			break
		}

		// Drop the oldest pair (user + assistant) or single message
		if len(dynamic) >= 2 && dynamic[0].Role == "user" && dynamic[1].Role == "assistant" {
			dynamic = dynamic[2:]
			droppedCount += 2
		} else {
			dynamic = dynamic[1:]
			droppedCount++
		}
	}

	if droppedCount == 0 {
		return messages
	}

	fmt.Fprintf(os.Stderr, "[Probe] Sliding window compaction: dropped %d messages, keeping %d dynamic + %d prefix (est. %d → %d tokens)\n",
		droppedCount, len(dynamic), staticPrefixLen, estimated, estimateConversationTokens(append(prefix, dynamic...)))

	// Reassemble: prefix + compaction marker + remaining dynamic turns
	result := make([]inference.InferenceMessage, 0, staticPrefixLen+1+len(dynamic))
	result = append(result, prefix...)
	result = append(result, inference.InferenceMessage{
		Role:    "user",
		Content: fmt.Sprintf("[%d earlier exploration turns compacted to fit context window]", droppedCount/2),
	})
	result = append(result, inference.InferenceMessage{
		Role:    "assistant",
		Content: "Understood. I will continue exploration from the most recent context.",
	})
	result = append(result, dynamic...)

	return result
}

// compactThoughtChain creates a rolling summary of recent thought chain steps.
// The compactionLevel parameter is retained for API compatibility but
// the structured compactor handles content-type-aware compaction internally.
// Code tool outputs are deterministically skeletonized (signatures preserved).
// Model reasoning text is compressed via the router LLM.
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

	// Convert to compactor steps.
	// Fix (ADR-benchmark-data-3): Steps whose ToolName is sql_cached_data or
	// introspect_cache have their ToolOutput preserved verbatim through
	// compaction. These outputs contain actual query result rows that the
	// 1B router model would otherwise strip as "verbose tabular content",
	// causing downstream synthesis to lose all data.
	hasCacheResults := false
	compactorSteps := make([]compactor.Step, len(windowSteps))
	for i, s := range windowSteps {
		toolOutput := s.ToolOutput
		isCacheTool := s.ToolName == "sql_cached_data" || s.ToolName == "introspect_cache"
		if isCacheTool && toolOutput != "" {
			hasCacheResults = true
		}
		compactorSteps[i] = compactor.Step{
			Index:      s.StepIndex,
			Thought:    s.Thought,
			ToolName:   s.ToolName,
			ToolArgs:   s.ToolArgs,
			ToolOutput: toolOutput,
		}
	}

	// Use RouterEngine for reasoning compression (fast, cheap via 1B router).
	// Tool outputs are handled deterministically — never LLM-compressed.
	compactEngine := &compactor.RouterEngine{}
	// Budget: reserve ~3K tokens for system prompt + recent steps + user prompt.
	// Compaction summary gets ~13K tokens of the router's 16K window ≈ ~52K chars.
	const compactionBudgetChars = 52000
	// Preserve tool output when explicitly set by compaction level OR when
	// the window contains cache query results that must survive for synthesis.
	preserveOutput := compactionLevel == compiler.CompactPreserve || hasCacheResults
	if hasCacheResults && compactionLevel != compiler.CompactPreserve {
		fmt.Fprintf(os.Stderr, "[Probe Compactor] Cache tool results detected in window — preserving tool outputs through compaction\n")
	}
	result, err := compactor.CompactSteps(ctx, compactorSteps, "", compactionBudgetChars, compactEngine, preserveOutput)

	// Fix 4: Post-compaction size validation — detect inflation and warn.
	// If compaction output exceeds input, the LLM reasoning compression is
	// inflating instead of compressing. Log a warning for diagnostics.
	if err == nil && result.OutputChars > result.InputChars {
		fmt.Fprintf(os.Stderr, "[Probe Compactor] WARNING: compaction inflated output (%d→%d chars, %.1fx). Router may be generating verbose responses.\n",
			result.InputChars, result.OutputChars, float64(result.OutputChars)/float64(result.InputChars))
	}
	if err != nil {
		return fmt.Errorf("structured compaction failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[Probe Compactor] Steps %d-%d: %d→%d chars (%d LLM calls)\n",
		startStep, currentStep, result.InputChars, result.OutputChars, result.LLMCalls)

	summary := memory.ThoughtSummary{
		ID:        fmt.Sprintf("%s_summary_%d_%d", probeID, startStep, currentStep),
		ProbeID:   probeID,
		TaskID:    taskID,
		StepRange: fmt.Sprintf("%d-%d", startStep, currentStep),
		Summary:   result.Output,
		CreatedAt: time.Now().Unix(),
	}

	return memory.DB.AddThoughtSummary(summary)
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

	// Priority 1: Absolute paths (e.g., /home/user/project/tzro/CONTEXT.md)
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

// extractAndPersistSymbols runs the Symbol Extractor on a file's content
// and persists any extracted symbols to the Symbol Index. Called as a
// post-read_file hook in the Thought Chain loop (ADR-0047).
//
// Errors are logged but not propagated — symbol extraction is best-effort
// and must not disrupt the probe's primary exploration loop.
func extractAndPersistSymbols(probeID, taskID, resolvedPath string) {
	contentBytes, err := os.ReadFile(resolvedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Probe] Symbol extraction: failed to read resolved path %s: %v\n", resolvedPath, err)
		return
	}
	syms, err := symbols.ExtractSymbols(filepath.Base(resolvedPath), contentBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Probe] Symbol extraction error for %s: %v\n", resolvedPath, err)
		return
	}
	if len(syms) == 0 {
		return
	}

	// Set full file paths (extractor only sees the basename for language detection)
	for i := range syms {
		syms[i].File = resolvedPath
	}

	if err := memory.DB.InsertSymbols(probeID, taskID, syms); err != nil {
		fmt.Fprintf(os.Stderr, "[Probe] Symbol index persist error: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "[Probe] Extracted %d symbols from %s\n", len(syms), resolvedPath)
}

// extractURLsFromWebSearch parses web_search JSON output and returns discovered URLs.
// Uses structured JSON parsing first (for the ToolSuccess envelope format), then
// falls back to regex extraction for non-standard output formats.
//
// P0 fix: The 4B local model cannot reliably extract URLs from search result
// prose to pass as web_browse arguments (benchmark run 8: 40 empty-URL rejections).
// This function deterministically extracts URLs so the probe can auto-populate
// web_browse calls without requiring model-side URL parsing.
func extractURLsFromWebSearch(toolOutput string) []string {
	// Primary: parse the ToolSuccess JSON envelope
	// Format: {"success":true,"data":{"results":[{"title":"...","url":"...","snippet":"..."}],...}}
	var envelope struct {
		Data struct {
			Results []struct {
				URL string `json:"url"`
			} `json:"results"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(toolOutput), &envelope) == nil && len(envelope.Data.Results) > 0 {
		var urls []string
		for _, r := range envelope.Data.Results {
			if r.URL != "" {
				urls = append(urls, r.URL)
			}
		}
		if len(urls) > 0 {
			return urls
		}
	}

	// Secondary: try flat results array (raw SearchResult format)
	var flat struct {
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if json.Unmarshal([]byte(toolOutput), &flat) == nil && len(flat.Results) > 0 {
		var urls []string
		for _, r := range flat.Results {
			if r.URL != "" {
				urls = append(urls, r.URL)
			}
		}
		if len(urls) > 0 {
			return urls
		}
	}

	// Fallback: regex extraction for non-standard formats
	urlRe := regexp.MustCompile(`https?://[^\s"',\]}>]+`)
	matches := urlRe.FindAllString(toolOutput, 20)
	// Deduplicate
	seen := make(map[string]bool)
	var unique []string
	for _, u := range matches {
		if !seen[u] {
			seen[u] = true
			unique = append(unique, u)
		}
	}
	return unique
}

// nodeIDFromProbeID extracts the original node ID from the probe's composite ID.
// Probe IDs follow the pattern taskID + "_" + nodeID (set in executor.go).
// Falls back to probeID if the prefix doesn't match.
func nodeIDFromProbeID(probeID, taskID string) string {
	prefix := taskID + "_"
	if strings.HasPrefix(probeID, prefix) {
		return strings.TrimPrefix(probeID, prefix)
	}
	return probeID
}
