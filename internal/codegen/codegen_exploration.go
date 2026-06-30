package codegen

import (
	"fmt"
	"time"

	"tzro/internal/compiler"
)

// BuildCodeDAGWithExploration constructs an execution graph that first explores
// the codebase context, then reasons about the code to generate, and finally
// validates the output via deterministic compilation.
//
// When codeCtx is provided (non-nil), the context has been pre-computed by the
// caller via GatherContext. The DAG is a 3-node graph: explore_context -> reason_code -> validate_code.
// The reason_code node's prompt is fully assembled using BuildCodePrompt with the pre-fetched
// existing content and siblings.
//
// When codeCtx is nil, the legacy 3-node DAG is built (check_context -> reason_code -> write_code)
// where the executor uses inference to extract tool arguments.
// This path is deprecated and should not be used for new code.
func BuildCodeDAGWithExploration(taskID, spec, filePath, language string, maxLines int, codeCtx *CodeContext) *compiler.ExecutionGraph {
	action := "create"
	if codeCtx != nil && codeCtx.Exists {
		action = "update"
	}
	if codeCtx != nil && codeCtx.Language != "" {
		language = codeCtx.Language
	}

	existingContent := ""
	siblings := make(map[string]string)
	if codeCtx != nil {
		existingContent = codeCtx.ExistingContent
		siblings = codeCtx.Siblings
	}

	nodes := []compiler.GraphNode{
		{
			ID:                  "explore_context",
			Type:                "action",
			Instructions:        fmt.Sprintf("Explore the codebase to understand context for generating %s code.\nSpec: %s\nTarget: %s\nAction: %s", language, spec, filePath, action),
			AllowedTools:        []string{"read_file", "list_dir", "search_files"},
			Status:              "pending",
			ActivationThreshold: 0.8,
			OutputFormat:        "source_code",
			OutputLanguage:      language,
		},
		{
			ID:           "reason_code",
			Type:         "synthesis",
			Instructions: BuildCodePrompt(spec, filePath, language, action, existingContent, siblings, maxLines),
			AllowedTools: []string{},
			Status:       "pending",
			OutputFormat:   "source_code",
			OutputLanguage: language,
		},
		{
			ID:                  "validate_code",
			Type:                "deterministic",
			Action:              "validate_code",
			Instructions:        fmt.Sprintf("Validate the generated code by running compilation against %s", filePath),
			AllowedTools:        []string{"validate_code"},
			Status:              "pending",
			ActivationThreshold: 0.7,
		},
	}

	edges := []compiler.GraphEdge{
		{SourceID: "explore_context", TargetID: "reason_code"},
		{SourceID: "reason_code", TargetID: "validate_code"},
	}

	budget := &compiler.MutationBudget{MaxSpawns: 10, RemainingSpawns: 10}

	return &compiler.ExecutionGraph{
		TaskID:         taskID,
		Nodes:          nodes,
		Edges:          edges,
		MutationBudget: budget,
		GoalPrompt:     fmt.Sprintf("Generate %s code for %s: %s", language, filePath, spec),
		MaxCycles:      1,
		CreatedAt:      time.Now().Unix(),
	}
}

// BuildDiffDAGWithExploration constructs an execution graph for diff-mode code
// generation with codebase exploration. Mirrors BuildCodeDAGWithExploration but
// uses the diff prompt format and GBNF-constrained DiffOutput schema on the
// reason_code node.
//
// Three nodes:
//  1. explore_context: action node (ActivationThreshold: 0.8) — explores codebase
//  2. reason_code: synthesis node with GBNF-constrained DiffOutput schema
//  3. validate_code: deterministic node (ActivationThreshold: 0.7) — compilation gate
func BuildDiffDAGWithExploration(taskID, spec, filePath, language string,
	codeCtx *CodeContext) *compiler.ExecutionGraph {

	action := "update" // diff mode always updates existing files
	if codeCtx != nil && codeCtx.Language != "" {
		language = codeCtx.Language
	}

	existingContent := ""
	siblings := make(map[string]string)
	if codeCtx != nil {
		existingContent = codeCtx.ExistingContent
		siblings = codeCtx.Siblings
	}

	nodes := []compiler.GraphNode{
		{
			ID:                  "explore_context",
			Type:                "action",
			Instructions:        fmt.Sprintf("Explore the codebase to understand context for editing %s code.\nSpec: %s\nTarget: %s\nAction: %s", language, spec, filePath, action),
			AllowedTools:        []string{"read_file", "list_dir", "search_files"},
			Status:              "pending",
			ActivationThreshold: 0.8,
			OutputFormat:        "source_code",
			OutputLanguage:      language,
		},
		{
			ID:             "reason_code",
			Type:           "synthesis",
			Instructions:   BuildDiffPrompt(spec, filePath, language, existingContent, siblings),
			AllowedTools:   []string{},
			Status:         "pending",
			OutputSchema:   DiffHunkSchema,
			OutputFormat:   "source_code",
			OutputLanguage: language,
		},
		{
			ID:                  "validate_code",
			Type:                "deterministic",
			Action:              "validate_code",
			Instructions:        fmt.Sprintf("Validate the patched code by running compilation against %s", filePath),
			AllowedTools:        []string{"validate_code"},
			Status:              "pending",
			ActivationThreshold: 0.7,
		},
	}

	edges := []compiler.GraphEdge{
		{SourceID: "explore_context", TargetID: "reason_code"},
		{SourceID: "reason_code", TargetID: "validate_code"},
	}

	budget := &compiler.MutationBudget{MaxSpawns: 10, RemainingSpawns: 10}

	return &compiler.ExecutionGraph{
		TaskID:         taskID,
		Nodes:          nodes,
		Edges:          edges,
		MutationBudget: budget,
		GoalPrompt:     fmt.Sprintf("Apply diff edits to %s %s: %s", language, filePath, spec),
		MaxCycles:      1,
		CreatedAt:      time.Now().Unix(),
	}
}
