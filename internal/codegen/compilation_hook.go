package codegen

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/executor"
	"tzro/internal/memory"
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
}

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
	} else {
		evidence.WriteString("FAILED\n")
		evidence.WriteString(compResult.Reason)
		evidence.WriteString("\n")
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

	fmt.Fprintf(os.Stderr, "[CompilationGateHook] %s → %s (file: %s)\n",
		node.ID, map[bool]string{true: "PASSED", false: "FAILED"}[compResult.Pass], h.FilePath)

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
		}
	}

	return executor.ActionContinue, nil
}
