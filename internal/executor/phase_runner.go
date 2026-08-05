package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tzro/internal/inference"
	"tzro/internal/memory"
)

// phaseContextKey is a context key used to pass the current phase name
// to the inference engine, enabling phase-aware mock routing in tests.
type phaseContextKeyType struct{}

var phaseContextKey = phaseContextKeyType{}

// --- Core Types (Design Spec §Phase Runner Contract) ---

// PhaseResult carries structured output from one phase to the next.
type PhaseResult struct {
	PhaseName   string                 `json:"phaseName"`
	Summary     string                 `json:"summary"`              // Compacted text for next phase's context
	Artifacts   map[string]interface{} `json:"artifacts,omitempty"`  // Structured data (URLs, file lists, SQL results)
	ToolsCalled []string               `json:"toolsCalled"`
	StepsUsed   int                    `json:"stepsUsed"`
	Backtracks  int                    `json:"backtracks"`           // Number of times this phase was re-entered
}

// Phase defines a single execution phase within a node.
type Phase struct {
	Name         string
	AllowedTools []string
	SystemPrompt string
	StepBudget   int
	Pass1Target  ModelTarget
	Recovery     PhaseRecovery
	// Transition determines the next phase after each step completes.
	// Returns "" to continue current phase, or a phase name to transition.
	Transition func(step int, result PhaseResult, err error) string
}

// PhaseRecovery defines per-phase error handling.
type PhaseRecovery struct {
	MaxRetries   int                // Backtrack re-entry limit
	OnExhaustion ExhaustionStrategy // Skip | Backtrack | Fail
	OnError      ErrorStrategy      // Retry | Transition | Abort
	BacktrackTo  string             // Target phase for Backtrack strategies
}

// ExhaustionStrategy controls what happens when a phase's step budget is exhausted.
type ExhaustionStrategy int

const (
	ExhaustionSkip      ExhaustionStrategy = iota // Skip to next phase
	ExhaustionBacktrack                           // Backtrack to a prior phase
	ExhaustionFail                                // Fail the entire node
)

// ErrorStrategy controls what happens when a step within a phase errors.
type ErrorStrategy int

const (
	ErrorRetry      ErrorStrategy = iota // Retry the step
	ErrorTransition                      // Transition to another phase
	ErrorAbort                           // Abort the entire node
	ErrorFail                            // Alias for Abort (matches spec language)
)

// PhaseManifest is the structured document carrying phase summaries
// from the Phase Runner to the Recall Node and Execution Envelope.
type PhaseManifest struct {
	Phases          []PhaseResult `json:"phases"`
	TotalBacktracks int           `json:"totalBacktracks"`
	TotalStepsUsed  int           `json:"totalStepsUsed"`
}

// PhaseRunner is the cyclic state machine that manages phase transitions
// with fresh KV contexts per phase. Operates within executeSingleNode.
type PhaseRunner struct {
	Phases       map[string]*Phase
	PhaseOrder   []string // Ordered phase names for sequential fallthrough
	InitialPhase string
	MaxCycles    int // Global backtrack budget (default: 3)
}

// Run executes the Phase Runner state machine from InitialPhase through
// all phase transitions until a terminal phase completes or an error occurs.
//
// Each phase gets a fresh inference context. Completed phases produce
// PhaseResults that are accumulated and injected as context summaries
// into subsequent phases.
func (pr *PhaseRunner) Run(
	ctx context.Context,
	taskID, probeID string,
	engine ProbeInferenceEngine,
	synthesisEngine ProbeInferenceEngine,
) ([]PhaseResult, error) {
	if pr.MaxCycles <= 0 {
		pr.MaxCycles = 3
	}

	// Slice 8: Checkpoint recovery — load completed phases from DB.
	// If phases were persisted from a prior run, skip them and resume
	// from the first incomplete phase.
	var results []PhaseResult
	completedPhases := make(map[string]bool)

	// Try loading checkpoints — nil-safe (DB may be uninitialized in tests)
	var checkpointRecords []memory.PhaseResultRecord
	func() {
		defer func() { recover() }()
		checkpointRecords, _ = memory.DB.GetPhaseResults(taskID, probeID)
	}()
	if len(checkpointRecords) > 0 {
		fmt.Fprintf(os.Stderr, "[PhaseRunner] Resuming from %d checkpointed phases\n", len(checkpointRecords))
		for _, record := range checkpointRecords {
			result := recordToPhaseResult(record)
			results = append(results, result)
			completedPhases[record.PhaseName] = true
		}
	}

	currentPhaseName := pr.InitialPhase

	for currentPhaseName != "" {
		// Skip already-checkpointed phases
		if completedPhases[currentPhaseName] {
			fmt.Fprintf(os.Stderr, "[PhaseRunner] Skipping checkpointed phase %q\n", currentPhaseName)
			// Find the transition from this completed phase
			phase, ok := pr.Phases[currentPhaseName]
			if !ok {
				break
			}
			// Find the matching result to determine next phase
			for _, r := range results {
				if r.PhaseName == currentPhaseName && phase.Transition != nil {
					next := phase.Transition(r.StepsUsed, r, nil)
					if next != "" {
						currentPhaseName = next
					} else {
						currentPhaseName = ""
					}
					break
				}
			}
			continue
		}

		phase, ok := pr.Phases[currentPhaseName]
		if !ok {
			return results, fmt.Errorf("phase %q not found in PhaseRunner", currentPhaseName)
		}

		fmt.Fprintf(os.Stderr, "[PhaseRunner] Starting phase %q (budget: %d steps)\n", phase.Name, phase.StepBudget)

		// Execute the phase's step loop
		result, nextPhase, err := pr.executePhase(ctx, phase, results, engine, synthesisEngine)
		if err != nil {
			// Handle error according to phase recovery strategy
			switch phase.Recovery.OnError {
			case ErrorFail, ErrorAbort:
				return results, fmt.Errorf("phase %q failed: %w", phase.Name, err)
			case ErrorRetry:
				return results, fmt.Errorf("phase %q failed (retry not yet implemented): %w", phase.Name, err)
			case ErrorTransition:
				return results, fmt.Errorf("phase %q failed (transition not yet implemented): %w", phase.Name, err)
			}
		}

		results = append(results, result)
		fmt.Fprintf(os.Stderr, "[PhaseRunner] Phase %q completed: %d steps, %d tools called\n",
			phase.Name, result.StepsUsed, len(result.ToolsCalled))

		// Persist checkpoint (best-effort — don't fail execution on DB errors)
		pr.persistPhaseResult(taskID, probeID, result)

		// Determine next phase
		if nextPhase != "" {
			currentPhaseName = nextPhase
		} else {
			currentPhaseName = ""
		}

		// Safety: prevent infinite loops
		if len(results) > 20 {
			return results, fmt.Errorf("PhaseRunner exceeded 20 phases — possible infinite loop")
		}
	}

	return results, nil
}

// executePhase runs a single phase's step loop using Two-Pass Extraction.
// Returns the PhaseResult, the next phase name (or "" for terminal), and any error.
func (pr *PhaseRunner) executePhase(
	ctx context.Context,
	phase *Phase,
	priorResults []PhaseResult,
	engine ProbeInferenceEngine,
	synthesisEngine ProbeInferenceEngine,
) (PhaseResult, string, error) {
	result := PhaseResult{
		PhaseName: phase.Name,
		Artifacts: make(map[string]interface{}),
	}

	// Inject phase name into context for mock engine routing
	phaseCtx := context.WithValue(ctx, phaseContextKey, phase.Name)

	// Build phase-specific system prompt with prior phase summaries
	systemPrompt := pr.buildPhaseSystemPrompt(phase, priorResults)

	var lastToolOutput string
	var toolsCalled []string

	for step := 1; step <= phase.StepBudget; step++ {
		// --- Pass 1: Free-text reasoning (phase-specific model target) ---
		var userPrompt strings.Builder
		if lastToolOutput != "" {
			userPrompt.WriteString(fmt.Sprintf("## Last Tool Output\n```\n%s\n```\n\n", lastToolOutput))
		}
		userPrompt.WriteString(fmt.Sprintf("Phase %q, step %d/%d: What should we do next?", phase.Name, step, phase.StepBudget))

		messages := []inference.InferenceMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt.String()},
		}

		reasoning, err := engine.InferMessages(phaseCtx, messages, "", phase.Pass1Target)
		if err != nil {
			return result, "", fmt.Errorf("phase %q step %d pass 1 failed: %w", phase.Name, step, err)
		}

		// --- Pass 2: GBNF-constrained action extraction ---
		action, toolName, args, err := extractToolAction(phaseCtx, engine, reasoning, phase.AllowedTools, false)
		if err != nil {
			return result, "", fmt.Errorf("phase %q step %d pass 2 failed: %w", phase.Name, step, err)
		}

		if action == "synthesize" {
			// Phase synthesis — generate summary from accumulated work
			result.StepsUsed = step
			result.ToolsCalled = toolsCalled
			result.Summary = pr.synthesizePhase(phaseCtx, phase, priorResults, toolsCalled, synthesisEngine)

			// Check transition
			nextPhase := ""
			if phase.Transition != nil {
				nextPhase = phase.Transition(step, result, nil)
			}
			return result, nextPhase, nil
		}

		// --- Tool dispatch ---
		toolsCalled = append(toolsCalled, toolName)

		// Simulate tool execution (in production, this calls the real tool dispatcher)
		toolOutput, toolErr := pr.dispatchPhaseTool(phaseCtx, toolName, args)
		if toolErr != nil {
			lastToolOutput = fmt.Sprintf("Error: %s", toolErr.Error())
		} else {
			lastToolOutput = toolOutput
		}

		// Check transition after each step
		result.StepsUsed = step
		result.ToolsCalled = toolsCalled
		if phase.Transition != nil {
			nextPhase := phase.Transition(step, result, toolErr)
			if nextPhase != "" {
				// Transition triggered — synthesize current phase and move on
				result.Summary = pr.synthesizePhase(phaseCtx, phase, priorResults, toolsCalled, synthesisEngine)
				return result, nextPhase, nil
			}
		}
	}

	// Step budget exhausted
	result.StepsUsed = phase.StepBudget
	result.ToolsCalled = toolsCalled
	result.Summary = pr.synthesizePhase(phaseCtx, phase, priorResults, toolsCalled, synthesisEngine)

	// Handle exhaustion according to recovery strategy
	switch phase.Recovery.OnExhaustion {
	case ExhaustionFail:
		return result, "", fmt.Errorf("phase %q exhausted step budget (%d)", phase.Name, phase.StepBudget)
	case ExhaustionSkip:
		// Skip — no next phase from exhaustion (terminal)
		fmt.Fprintf(os.Stderr, "[PhaseRunner] Phase %q budget exhausted — skipping\n", phase.Name)
		return result, "", nil
	case ExhaustionBacktrack:
		// TODO: Slice 3
		return result, "", nil
	}

	return result, "", nil
}

// buildPhaseSystemPrompt constructs a fresh system prompt for a phase,
// injecting compacted summaries from prior completed phases.
func (pr *PhaseRunner) buildPhaseSystemPrompt(phase *Phase, priorResults []PhaseResult) string {
	var b strings.Builder
	b.WriteString(phase.SystemPrompt)

	if len(priorResults) > 0 {
		b.WriteString("\n\n## Prior Phase Results\n")
		for _, r := range priorResults {
			b.WriteString(fmt.Sprintf("\n### %s (completed in %d steps)\n%s\n", r.PhaseName, r.StepsUsed, r.Summary))
		}
	}

	// Inject allowed tools as context
	if len(phase.AllowedTools) > 0 {
		b.WriteString(fmt.Sprintf("\n\n## Available Tools\nYou may use ONLY these tools: %s\n", strings.Join(phase.AllowedTools, ", ")))
	}

	return b.String()
}

// synthesizePhase generates a compacted summary of the phase's work.
func (pr *PhaseRunner) synthesizePhase(
	ctx context.Context,
	phase *Phase,
	priorResults []PhaseResult,
	toolsCalled []string,
	synthesisEngine ProbeInferenceEngine,
) string {
	systemPrompt := fmt.Sprintf(
		"Summarize the work done in phase %q. List key findings and outputs concisely. "+
			"Tools used: %s.", phase.Name, strings.Join(toolsCalled, ", "))
	userPrompt := "Produce a concise summary of this phase's findings."

	result, err := synthesisEngine.Infer(ctx, systemPrompt, userPrompt, "", TargetWorker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[PhaseRunner] Phase %q synthesis failed: %v\n", phase.Name, err)
		return fmt.Sprintf("[Synthesis failed for phase %s: %s]", phase.Name, err.Error())
	}
	return result
}

// dispatchPhaseTool dispatches a tool call within a phase.
// In production, this delegates to the real tool dispatcher.
// For now, returns a stub response — production wiring happens in Slice 10.
func (pr *PhaseRunner) dispatchPhaseTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	// Production implementation will call tools.Dispatch(toolName, args)
	// For now, return args as a JSON string to confirm dispatch happened
	argsJSON, _ := json.Marshal(args)
	return fmt.Sprintf("[%s result: args=%s]", toolName, string(argsJSON)), nil
}

// BuildManifest assembles a PhaseManifest from completed PhaseResults.
func (pr *PhaseRunner) BuildManifest(results []PhaseResult) PhaseManifest {
	manifest := PhaseManifest{
		Phases: results,
	}
	for _, r := range results {
		manifest.TotalStepsUsed += r.StepsUsed
		manifest.TotalBacktracks += r.Backtracks
	}
	return manifest
}

// persistPhaseResult saves a completed phase result to the DB for checkpointing.
// Best-effort: DB errors are logged but do not fail execution.
func (pr *PhaseRunner) persistPhaseResult(taskID, nodeID string, result PhaseResult) {
	// Guard against uninitialized DB (common in unit tests)
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[PhaseRunner] Warning: checkpoint persistence unavailable (DB not initialized)\n")
		}
	}()

	artifactsJSON, _ := json.Marshal(result.Artifacts)
	toolsJSON, _ := json.Marshal(result.ToolsCalled)

	record := memory.PhaseResultRecord{
		TaskID:      taskID,
		NodeID:      nodeID,
		PhaseName:   result.PhaseName,
		Summary:     result.Summary,
		Artifacts:   string(artifactsJSON),
		ToolsCalled: string(toolsJSON),
		StepsUsed:   result.StepsUsed,
		Backtracks:  result.Backtracks,
	}

	if err := memory.DB.SavePhaseResult(record); err != nil {
		fmt.Fprintf(os.Stderr, "[PhaseRunner] Warning: failed to persist phase %q checkpoint: %v\n", result.PhaseName, err)
	}
}

// recordToPhaseResult converts a DB PhaseResultRecord back to an in-memory PhaseResult.
func recordToPhaseResult(record memory.PhaseResultRecord) PhaseResult {
	result := PhaseResult{
		PhaseName:  record.PhaseName,
		Summary:    record.Summary,
		StepsUsed:  record.StepsUsed,
		Backtracks: record.Backtracks,
		Artifacts:  make(map[string]interface{}),
	}

	// Parse tools_called JSON
	if record.ToolsCalled != "" {
		_ = json.Unmarshal([]byte(record.ToolsCalled), &result.ToolsCalled)
	}

	// Parse artifacts JSON
	if record.Artifacts != "" {
		_ = json.Unmarshal([]byte(record.Artifacts), &result.Artifacts)
	}

	return result
}
