package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/executor"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/symbols"
)

// CompilationGateHook implements executor.ExecutionHook to run the compilation
// gate after source_code nodes complete. It appends the compilation result
// (PASSED or FAILED + errors) to the node output so that the Edge Thought
// inference can reason about compilation success/failure when evaluating the
// activation threshold on the validate_code edge.
//
// This is the bridge between the codegen compilation gate (RunCompilationGate)
// and the Edge Thought-driven repair loop (ADR-0036).
type CompilationGateHook struct {
	// FilePath is the path to the generated file that should be compiled.
	FilePath string
	// Language is the programming language (e.g. "go", "typescript").
	Language string
	// Spec is the original code generation specification, used to build
	// structured repair prompts when compilation fails.
	Spec string
	// AllowCloudRepair enables cloud model escalation for repair after local
	// repair attempts are exhausted (ADR-0057). Set to true in Direct mode,
	// false in Draft/Pseudocode mode.
	AllowCloudRepair bool
	// localFailureCount tracks how many times the local model has produced
	// code that fails compilation within this task. Used to decide when to
	// escalate to cloud repair.
	localFailureCount int
	// lastCompilerErrors stores the most recent compiler error output,
	// used for the complexity_exceeded response in Draft mode.
	lastCompilerErrors string
	// specComplianceAttempted tracks whether the Spec Compliance Gate has
	// already attempted a regeneration for this hook instance. Prevents
	// infinite loops (ADR-0061).
	specComplianceAttempted bool

	// TaskTier is the complexity tier (1-5) from the benchmark/comparison suite.
	// When >= 4 and AllowCloudReview is true, a cloud semantic review runs after
	// compilation and spec compliance pass (ADR-0070).
	TaskTier         int  `json:"-"`
	AllowCloudReview bool `json:"-"`

	// CloudReviewFunc is a pluggable semantic review function for testability.
	// When nil, defaults to the real cloud inference implementation.
	// Signature: (ctx, generatedCode, spec, language) -> (pass bool, reason string, error)
	CloudReviewFunc func(ctx context.Context, code, spec, language string) (bool, string, error)

	// OriginalContent holds the pre-existing file content (seed file) for update
	// tasks. When non-empty, the preservation assertion runs after compilation
	// and spec compliance pass, checking that all original public symbols survive
	// in the generated code (FM-4).
	OriginalContent string

	// cloudReviewAttempted prevents double cloud review within one hook lifecycle.
	cloudReviewAttempted bool

	// preservationAttempted prevents infinite preservation loops.
	preservationAttempted bool
}

// MaxLocalRepairAttempts is the number of local repair attempts before
// escalating to cloud repair. The initial generation counts as attempt 1.
const MaxLocalRepairAttempts = 2

// Ensure CompilationGateHook satisfies ExecutionHook at compile time.
var _ executor.ExecutionHook = (*CompilationGateHook)(nil)

func (h *CompilationGateHook) BeforeLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (executor.HookAction, error) {
	return executor.ActionContinue, nil
}

func (h *CompilationGateHook) AfterLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (executor.HookAction, error) {
	return executor.ActionContinue, nil
}

func (h *CompilationGateHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (executor.HookAction, error) {
	return executor.ActionContinue, nil
}

// AfterNode runs the compilation gate on source_code nodes. If the node's
// OutputFormat is "source_code", it writes the raw output to the target file,
// runs the compilation command, and appends the result to the raw output.
// This enriched output is then available to the Edge Thought inference.
//
// ADR-0057: After MaxLocalRepairAttempts local failures, if AllowCloudRepair
// is true, escalates the repair to the cloud model with a narrow payload.
func (h *CompilationGateHook) AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (executor.HookAction, error) {
	if node.OutputFormat != "source_code" {
		return executor.ActionContinue, nil
	}

	if rawOutput == nil || *rawOutput == "" {
		return executor.ActionContinue, nil
	}

	// Strip markdown fences and clean the output before writing
	cleanCode := StripMarkdownFences(*rawOutput)

	// Write the generated code to the target file for compilation
	if h.FilePath != "" {
		if _, _, err := WriteCodeFile(h.FilePath, cleanCode, 0); err != nil {
			fmt.Fprintf(os.Stderr, "[CompilationGateHook] Failed to write file %s: %v\n", h.FilePath, err)
			// Append write failure as evidence for Edge Thought
			*rawOutput = cleanCode + "\n\n## Compilation Result\nFAILED\nCould not write file: " + err.Error()
			return executor.ActionContinue, nil
		}
	}

	// Run compilation gate
	compResult := RunCompilationGate(h.Language, h.FilePath)

	// Build the compilation evidence section
	var evidence strings.Builder
	evidence.WriteString("\n\n## Compilation Result\n")

	if compResult.Pass {
		evidence.WriteString("PASSED\n")
		// Reset failure count on success
		h.localFailureCount = 0

		// ADR-0061: Spec Compliance Gate — check functional completeness
		// Only fires when: spec is present, compilation passed, and we haven't
		// already attempted spec compliance (prevents infinite loops).
		if h.Spec != "" && !h.specComplianceAttempted {
			complianceResult := h.runSpecComplianceGate(ctx, cleanCode, taskID)
			if complianceResult != nil && !complianceResult.Pass {
				h.specComplianceAttempted = true
				fmt.Fprintf(os.Stderr, "[CompilationGateHook] Spec compliance FAILED for %s — %d missing requirements. Triggering regeneration.\n",
					h.FilePath, len(complianceResult.MissingRequirements))

				// Build regeneration prompt with missing requirements checklist
				moduleCtx := DiscoverModuleContext(h.FilePath, h.Language)
				regenPrompt := BuildRegenerationPrompt(h.Spec, complianceResult.Checklist, h.Language, 500, moduleCtx)

				// Attempt local regeneration
				regenCode, regenErr := h.attemptLocalRegeneration(ctx, regenPrompt, taskID)
				if regenErr == nil && regenCode != "" {
					// Write regenerated code and re-run compilation
					if _, _, writeErr := WriteCodeFile(h.FilePath, regenCode, 0); writeErr == nil {
						recheck := RunCompilationGate(h.Language, h.FilePath)
						if recheck.Pass {
							fmt.Fprintf(os.Stderr, "[CompilationGateHook] Spec compliance regeneration COMPILED for %s\n", h.FilePath)
							cleanCode = regenCode
							evidence.Reset()
							evidence.WriteString("\n\n## Compilation Result\nPASSED\n")
							evidence.WriteString("(Regenerated after spec compliance failure)\n")
						} else {
							fmt.Fprintf(os.Stderr, "[CompilationGateHook] Spec compliance regeneration FAILED compilation for %s: %s\n",
								h.FilePath, recheck.Reason)
							// Keep the original compiled code — it's at least compilable
						}
					}
				} else {
					fmt.Fprintf(os.Stderr, "[CompilationGateHook] Local regeneration failed for %s: %v\n", h.FilePath, regenErr)
				}
			}
		}

		// ADR-0070: Cloud Semantic Review for T4+ tasks.
		// Fires after compilation and spec compliance both pass. The cloud model
		// reviews semantic correctness — does the code actually DO what the spec asks?
		// On rejection, triggers full cloud regeneration.
		if h.TaskTier >= 4 && h.AllowCloudReview && h.Spec != "" && !h.cloudReviewAttempted {
			h.cloudReviewAttempted = true

			reviewFunc := h.CloudReviewFunc
			if reviewFunc == nil {
				reviewFunc = h.defaultCloudSemanticReview
			}

			pass, reason, reviewErr := reviewFunc(ctx, cleanCode, h.Spec, h.Language)
			if reviewErr != nil {
				fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud semantic review error for %s: %v\n", h.FilePath, reviewErr)
			} else if !pass {
				fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud semantic review REJECTED %s: %s. Triggering full cloud regeneration.\n",
					h.FilePath, reason)

				// Full cloud regeneration with spec + rejection reason
				regenCode, regenErr := h.attemptCloudRegeneration(ctx, cleanCode, h.Spec, reason, taskID)
				if regenErr == nil && regenCode != "" {
					if _, _, writeErr := WriteCodeFile(h.FilePath, regenCode, 0); writeErr == nil {
						recheck := RunCompilationGate(h.Language, h.FilePath)
						if recheck.Pass {
							fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud regeneration after semantic review PASSED for %s\n", h.FilePath)
							cleanCode = regenCode
							evidence.Reset()
							evidence.WriteString("\n\n## Compilation Result\nPASSED\n")
							evidence.WriteString("(Cloud regeneration after semantic review rejection)\n")
						} else {
							fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud regeneration FAILED compilation for %s: %s\n",
								h.FilePath, recheck.Reason)
						}
					}
				} else if regenErr != nil {
					fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud regeneration failed for %s: %v\n", h.FilePath, regenErr)
				}
			} else {
				fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud semantic review PASSED for %s\n", h.FilePath)
			}
		}

		// FM-4: Preservation assertion for update tasks.
		// When OriginalContent is provided, extract public symbols from both
		// the original and generated code. If any original symbols are missing,
		// trigger regeneration with explicit preservation instructions.
		if h.OriginalContent != "" && !h.preservationAttempted {
			missing := h.checkSymbolPreservation(cleanCode)
			if len(missing) > 0 {
				h.preservationAttempted = true
				fmt.Fprintf(os.Stderr, "[CompilationGateHook] Preservation FAILED for %s — %d original symbols removed: %v\n",
					h.FilePath, len(missing), missing)

				// Build regeneration prompt with preservation requirement
				moduleCtx := DiscoverModuleContext(h.FilePath, h.Language)
				preservationPrompt := fmt.Sprintf(
					"The generated code is missing the following original public symbols that MUST be preserved: %s\n\n"+
						"Regenerate the code to include ALL original public symbols while also fulfilling the spec.\n\n"+
						"Original file content:\n```\n%s\n```\n\n"+
						"Spec: %s",
					strings.Join(missing, ", "), h.OriginalContent, h.Spec)
				_ = moduleCtx // available for future prompt enrichment

				regenCode, regenErr := h.attemptLocalRegeneration(ctx, preservationPrompt, taskID)
				if regenErr == nil && regenCode != "" {
					if _, _, writeErr := WriteCodeFile(h.FilePath, regenCode, 0); writeErr == nil {
						recheck := RunCompilationGate(h.Language, h.FilePath)
						if recheck.Pass {
							fmt.Fprintf(os.Stderr, "[CompilationGateHook] Preservation regeneration COMPILED for %s\n", h.FilePath)
							cleanCode = regenCode
							evidence.Reset()
							evidence.WriteString("\n\n## Compilation Result\nPASSED\n")
							evidence.WriteString("(Regenerated after preservation failure)\n")
						} else {
							fmt.Fprintf(os.Stderr, "[CompilationGateHook] Preservation regeneration FAILED compilation for %s: %s\n",
								h.FilePath, recheck.Reason)
						}
					}
				} else {
					fmt.Fprintf(os.Stderr, "[CompilationGateHook] Preservation regeneration failed for %s: %v\n", h.FilePath, regenErr)
				}
			}
		}
	} else {
		h.localFailureCount++
		h.lastCompilerErrors = compResult.Reason
		evidence.WriteString("FAILED\n")
		evidence.WriteString(compResult.Reason)
		evidence.WriteString("\n")

		// ADR-0057: Cloud repair escalation after local attempts exhausted
		if h.localFailureCount >= MaxLocalRepairAttempts && h.AllowCloudRepair && !isCloudRepairBlocked() {
			fmt.Fprintf(os.Stderr, "[CompilationGateHook] Local repair exhausted (%d attempts). Escalating to cloud repair for %s\n",
				h.localFailureCount, h.FilePath)

			cloudCode, cloudErr := h.attemptCloudRepair(ctx, cleanCode, compResult.Reason, taskID)
			if cloudErr == nil {
				// Write cloud-repaired code and re-run compilation
				if _, _, writeErr := WriteCodeFile(h.FilePath, cloudCode, 0); writeErr == nil {
					recheck := RunCompilationGate(h.Language, h.FilePath)
					if recheck.Pass {
						fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud repair PASSED for %s\n", h.FilePath)
						cleanCode = cloudCode
						evidence.Reset()
						evidence.WriteString("\n\n## Compilation Result\nPASSED\n")
						evidence.WriteString("(Cloud repair after local exhaustion)\n")
						h.localFailureCount = 0
					} else {
						fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud repair still FAILED for %s: %s\n",
							h.FilePath, recheck.Reason)
						// Write the cloud attempt anyway — it may be closer to correct
						cleanCode = cloudCode
						evidence.Reset()
						evidence.WriteString("\n\n## Compilation Result\nFAILED\n")
						evidence.WriteString(recheck.Reason)
						evidence.WriteString("\n")
						h.lastCompilerErrors = recheck.Reason
					}
				}
			} else {
				fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud repair failed for %s: %v\n", h.FilePath, cloudErr)
			}
		}
	}

	// Also inject available module context for repair decisions
	moduleCtx := DiscoverModuleContext(h.FilePath, h.Language)
	if moduleCtx != "" {
		evidence.WriteString("\n## Available Packages\n")
		evidence.WriteString(moduleCtx)
	}

	// Append compilation evidence to the raw output
	// The Edge Thought will see this when evaluating the activation threshold
	*rawOutput = cleanCode + evidence.String()

	fmt.Fprintf(os.Stderr, "[CompilationGateHook] %s → %s (file: %s, attempt: %d)\n",
		node.ID, map[bool]string{true: "PASSED", false: "FAILED"}[compResult.Pass || h.localFailureCount == 0], h.FilePath, h.localFailureCount)

	return executor.ActionContinue, nil
}

// OnEdgeTraversal overrides the Edge Thought confidence when the source node's
// output contains compilation failure evidence. The LM-generated confidence
// score is unreliable for code quality (benchmark #4 showed 0.95 confidence
// on code that scored 2.0/5.0). This deterministic override ensures the
// activation gate always sees confidence=0.0 for broken code, triggering
// a repair spawn or budget-exhaustion halt.
func (h *CompilationGateHook) OnEdgeTraversal(ctx context.Context, taskID string, sourceNode, targetNode *compiler.GraphNode, edgeThought *memory.EdgeThought) (executor.HookAction, error) {
	if edgeThought == nil || sourceNode.OutputFormat != "source_code" {
		return executor.ActionContinue, nil
	}

	// Check if the source node's output contains compilation failure evidence
	if state, ok := memory.DB.GetNodeState(taskID, sourceNode.ID); ok {
		output := state.RawOutput
		if output == "" {
			output = state.Output
		}
		if strings.Contains(output, "## Compilation Result\nFAILED") {
			edgeThought.GoalConfidence = 0.0
			edgeThought.GoalAchieved = false
			fmt.Fprintf(os.Stderr, "[CompilationGateHook] Compilation failed — overriding confidence to 0.0 for edge %s→%s\n",
				sourceNode.ID, targetNode.ID)

			// Build a structured repair prompt so spawned nodes get exact
			// compiler errors instead of a generic "continue working" template.
			originalCode, compilerErrors := extractCompilationEvidence(output)
			if compilerErrors != "" && h.Spec != "" {
				moduleCtx := DiscoverModuleContext(h.FilePath, h.Language)
				edgeThought.Thought = BuildRepairPrompt(
					originalCode, compilerErrors, h.Spec, h.Language, 500, moduleCtx,
				)

				// Inject error category analysis for targeted repair constraints
				category, constraint := ClassifyCompilerError(compilerErrors)
				if category != "" {
					fmt.Fprintf(os.Stderr, "[CompilationGateHook] Error category: %s for edge %s→%s\n",
						category, sourceNode.ID, targetNode.ID)
					// Prepend constraint to thought for maximum visibility to the repair model
					edgeThought.Thought = fmt.Sprintf("## CRITICAL CONSTRAINT\n%s\n\n%s", constraint, edgeThought.Thought)
				}

				fmt.Fprintf(os.Stderr, "[CompilationGateHook] Injected structured repair prompt (%d chars) for edge %s→%s\n",
					len(edgeThought.Thought), sourceNode.ID, targetNode.ID)
			}
		}
	}

	return executor.ActionContinue, nil
}

// GetLastCompilerErrors returns the most recent compiler errors, used by
// the complexity_exceeded response in Draft mode (ADR-0057).
func (h *CompilationGateHook) GetLastCompilerErrors() string {
	return h.lastCompilerErrors
}

// GetLocalFailureCount returns the number of local compilation failures.
func (h *CompilationGateHook) GetLocalFailureCount() int {
	return h.localFailureCount
}

// attemptCloudRepair sends a narrow repair payload to the cloud model:
// compiler errors, spec, broken code, and module context. Returns the
// repaired code or an error.
func (h *CompilationGateHook) attemptCloudRepair(ctx context.Context, brokenCode, compilerErrors, taskID string) (string, error) {
	moduleCtx := DiscoverModuleContext(h.FilePath, h.Language)

	systemPrompt := fmt.Sprintf(`You are a code repair agent. Fix the compilation errors in the %s code below.
Output ONLY the corrected source code — no explanations, no markdown fences, no commentary.

## Specification
%s

## Module Context
%s`, h.Language, h.Spec, moduleCtx)

	userPrompt := fmt.Sprintf(`## Broken Code
%s

## Compiler Errors
%s

Fix ALL compiler errors. Output the complete corrected file.`, brokenCode, compilerErrors)

	messages := []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	result, err := inference.CallCloudModel(ctx, messages, "")
	if err != nil {
		return "", fmt.Errorf("cloud repair inference failed: %w", err)
	}

	// Strip markdown fences from cloud response
	result = StripMarkdownFences(result)

	fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud repair response: %d chars for task %s\n",
		len(result), taskID)

	return result, nil
}

// runSpecComplianceGate evaluates whether the generated code implements all
// requirements from the spec. Uses the Local Model to produce a structured
// IMPLEMENTED/MISSING checklist. Returns nil if evaluation fails or if no
// spec is provided.
//
// ADR-0061: Spec Compliance Gate — post-compilation functional completeness check.
func (h *CompilationGateHook) runSpecComplianceGate(ctx context.Context, generatedCode, taskID string) *SpecComplianceResult {
	if h.Spec == "" {
		return nil
	}

	evalPrompt := BuildComplianceEvalPrompt(generatedCode, h.Spec, h.Language)

	messages := []inference.InferenceMessage{
		{Role: "user", Content: evalPrompt},
	}

	result, err := inference.GlobalWorkerModel.CallLocalModel(ctx, messages, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[CompilationGateHook] Spec compliance evaluation failed for %s: %v\n", taskID, err)
		return nil
	}

	compliance := ParseComplianceChecklist(result.Content)
	fmt.Fprintf(os.Stderr, "[CompilationGateHook] Spec compliance: pass=%v, missing=%d for %s\n",
		compliance.Pass, len(compliance.MissingRequirements), taskID)

	return compliance
}

// attemptLocalRegeneration runs a full code regeneration using the local model
// with the given prompt. Returns the generated code and any error.
//
// ADR-0061: Uses full regeneration (not targeted patching) because the failed
// code's structure may be fundamentally incompatible with the missing requirements.
func (h *CompilationGateHook) attemptLocalRegeneration(ctx context.Context, prompt, taskID string) (string, error) {
	messages := []inference.InferenceMessage{
		{Role: "user", Content: prompt},
	}

	result, err := inference.GlobalWorkerModel.CallLocalModel(ctx, messages, "")
	if err != nil {
		return "", fmt.Errorf("local regeneration inference failed: %w", err)
	}

	// Strip markdown fences from the response
	code := StripMarkdownFences(result.Content)

	fmt.Fprintf(os.Stderr, "[CompilationGateHook] Local regeneration: %d chars for task %s\n",
		len(code), taskID)

	return code, nil
}

// isCloudRepairBlocked returns true when cloud repair must not be attempted.
func isCloudRepairBlocked() bool {
	cfg := config.Get()
	return cfg.PrivacyLevel == "strict-local" || cfg.ModelMode == "local"
}

// extractCompilationEvidence splits node output into the original code and
// the compiler error text. The output format from AfterNode is:
//
//	<original code>
//	\n\n## Compilation Result\nFAILED\n<errors>
//	\n\n## Available Packages\n...
func extractCompilationEvidence(output string) (originalCode, compilerErrors string) {
	const marker = "\n\n## Compilation Result\n"
	idx := strings.Index(output, marker)
	if idx < 0 {
		return output, ""
	}

	originalCode = output[:idx]

	// Everything after "FAILED\n" until the next section or end
	afterMarker := output[idx+len(marker):]
	if !strings.HasPrefix(afterMarker, "FAILED\n") {
		return originalCode, ""
	}
	errText := afterMarker[len("FAILED\n"):]

	// Trim at the next "## " section header (e.g. "## Available Packages")
	if secIdx := strings.Index(errText, "\n\n## "); secIdx >= 0 {
		errText = errText[:secIdx]
	}

	return originalCode, strings.TrimSpace(errText)
}

// defaultCloudSemanticReview sends the generated code and spec to the cloud model
// for semantic correctness review (ADR-0070). Returns (pass, reason, error).
func (h *CompilationGateHook) defaultCloudSemanticReview(ctx context.Context, code, spec, language string) (bool, string, error) {
	systemPrompt := fmt.Sprintf(`Review this %s code against the specification. Evaluate semantic correctness — does the code actually DO what the spec asks? Focus on logic errors, not style. Output JSON: {"pass": true/false, "reason": "string"}`, language)

	userPrompt := fmt.Sprintf("## Specification\n%s\n\n## Generated Code\n%s", spec, code)

	messages := []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	// Use JSON schema to constrain the response format
	reviewSchema := `{"type":"object","properties":{"pass":{"type":"boolean"},"reason":{"type":"string"}},"required":["pass","reason"]}`

	result, err := inference.CallCloudModel(ctx, messages, reviewSchema)
	if err != nil {
		return false, "", fmt.Errorf("cloud semantic review inference failed: %w", err)
	}

	// Parse the JSON response
	type reviewResponse struct {
		Pass   bool   `json:"pass"`
		Reason string `json:"reason"`
	}

	var resp reviewResponse
	if parseErr := json.Unmarshal([]byte(result), &resp); parseErr != nil {
		// If we can't parse, assume pass (fail-open to avoid blocking)
		fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud review response unparseable, assuming pass: %v\n", parseErr)
		return true, "", nil
	}

	return resp.Pass, resp.Reason, nil
}

// attemptCloudRegeneration performs full cloud regeneration from spec + rejection
// reason (ADR-0070). Unlike attemptCloudRepair which fixes compiler errors, this
// regenerates from the spec because the code has semantic (logic) errors.
func (h *CompilationGateHook) attemptCloudRegeneration(ctx context.Context, brokenCode, spec, rejectionReason, taskID string) (string, error) {
	moduleCtx := DiscoverModuleContext(h.FilePath, h.Language)

	systemPrompt := fmt.Sprintf(`You are a code generation agent. Generate %s code that correctly implements the specification.
A previous attempt was rejected for semantic errors. Generate correct code from scratch.
Output ONLY the source code — no explanations, no markdown fences, no commentary.

## Module Context
%s`, h.Language, moduleCtx)

	userPrompt := fmt.Sprintf(`## Specification
%s

## Previous Attempt (REJECTED)
%s

## Rejection Reason
%s

Generate the complete corrected file implementing the specification correctly.`, spec, brokenCode, rejectionReason)

	messages := []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	result, err := inference.CallCloudModel(ctx, messages, "")
	if err != nil {
		return "", fmt.Errorf("cloud regeneration inference failed: %w", err)
	}

	result = StripMarkdownFences(result)

	fmt.Fprintf(os.Stderr, "[CompilationGateHook] Cloud regeneration response: %d chars for task %s\n",
		len(result), taskID)

	return result, nil
}

// checkSymbolPreservation extracts public symbols from both the original
// content and the generated code, returning the names of any original symbols
// that are missing in the generated version. Uses the Symbol Extractor
// (tree-sitter-based AST parsing) for deterministic, language-aware comparison.
func (h *CompilationGateHook) checkSymbolPreservation(generatedCode string) []string {
	filename := filepath.Base(h.FilePath)

	originalSyms, origErr := symbols.ExtractSymbols(filename, []byte(h.OriginalContent))
	if origErr != nil || len(originalSyms) == 0 {
		return nil // Can't extract — skip preservation check
	}

	generatedSyms, genErr := symbols.ExtractSymbols(filename, []byte(generatedCode))
	if genErr != nil {
		return nil // Can't extract — skip preservation check
	}

	// Build a set of generated symbol names (exported only)
	generatedSet := make(map[string]bool)
	for _, s := range generatedSyms {
		if s.Exported {
			generatedSet[s.Name] = true
		}
	}

	// Find original exported symbols missing from generated code
	var missing []string
	for _, s := range originalSyms {
		if s.Exported && !generatedSet[s.Name] {
			missing = append(missing, fmt.Sprintf("%s (%s)", s.Name, s.Kind))
		}
	}

	return missing
}
