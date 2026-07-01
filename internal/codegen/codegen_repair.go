package codegen

import (
	"fmt"
	"strings"
	"time"

	"tzro/internal/compiler"
)

// BuildRepairPrompt constructs a prompt for the local model to fix compilation
// errors in generated code. The model receives the original code, the compiler
// error output, and instructions to fix the errors while preserving intent.
func BuildRepairPrompt(originalCode, compilerErrors, spec, language string, maxLines int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are a %s code repair assistant. Fix the compilation errors in the code below.\n\n", language))

	sb.WriteString("## Rules\n")
	sb.WriteString(fmt.Sprintf("1. Output ONLY the complete, fixed %s source file — no markdown fences, no explanations\n", language))
	sb.WriteString("2. Fix ALL compilation errors listed below\n")
	sb.WriteString("3. Preserve the original intent and functionality\n")
	sb.WriteString("4. Do not add new features or change the API surface\n")
	sb.WriteString(fmt.Sprintf("5. Keep the output under %d lines\n", maxLines))
	sb.WriteString("\n")

	sb.WriteString("## Compilation Errors\n")
	sb.WriteString("```\n")
	sb.WriteString(compilerErrors)
	if !strings.HasSuffix(compilerErrors, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("```\n\n")

	sb.WriteString("## Original Code (with errors)\n")
	sb.WriteString(fmt.Sprintf("```%s\n", language))
	sb.WriteString(originalCode)
	if !strings.HasSuffix(originalCode, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("```\n\n")

	sb.WriteString("## Original Spec\n")
	sb.WriteString(spec)
	sb.WriteString("\n")

	return sb.String()
}

// BuildRepairDAG constructs a single-node synthesis graph that re-generates
// code using the repair prompt. Used for the compilation gate retry loop.
func BuildRepairDAG(taskID, originalCode, compilerErrors, spec, language string, maxLines int) *compiler.ExecutionGraph {
	prompt := BuildRepairPrompt(originalCode, compilerErrors, spec, language, maxLines)

	return &compiler.ExecutionGraph{
		TaskID: taskID,
		Nodes: []compiler.GraphNode{
			{
				ID:             "reason_code",
				Type:           "synthesis",
				Instructions:   prompt,
				AllowedTools:   []string{},
				Status:         "pending",
				OutputFormat:   "source_code",
				OutputLanguage: language,
			},
		},
		Edges:     []compiler.GraphEdge{},
		GoalPrompt: fmt.Sprintf("Fix compilation errors in %s code", language),
		MaxCycles:  1,
		CreatedAt:  time.Now().Unix(),
	}
}

