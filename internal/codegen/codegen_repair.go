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
// If errorCategory/errorConstraint are non-empty (from ClassifyCompilerError),
// they are injected as a targeted constraint before the errors section.
func BuildRepairPrompt(originalCode, compilerErrors, spec, language string, maxLines int, moduleContext string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are a %s code repair assistant. Fix the compilation errors in the code below.\n\n", language))

	sb.WriteString("## Rules\n")
	sb.WriteString(fmt.Sprintf("1. Output ONLY the complete, fixed %s source file — no markdown fences, no explanations\n", language))
	sb.WriteString("2. Fix ALL compilation errors listed below\n")
	sb.WriteString("3. Preserve the original intent and functionality\n")
	sb.WriteString("4. Do not add new features or change the API surface\n")
	sb.WriteString(fmt.Sprintf("5. Keep the output under %d lines\n", maxLines))
	sb.WriteString("\n")

	// Inject error category analysis if we can classify the errors
	category, constraint := ClassifyCompilerError(compilerErrors)
	if category != "" {
		sb.WriteString("## Error Category Analysis\n")
		sb.WriteString(fmt.Sprintf("Category: %s\n", category))
		sb.WriteString(fmt.Sprintf("Constraint: %s\n\n", constraint))
	}

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
	sb.WriteString("\n\n")

	if moduleContext != "" {
		sb.WriteString("## Available Packages\n")
		sb.WriteString(moduleContext)
		if !strings.HasSuffix(moduleContext, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// ClassifyCompilerError analyzes compiler error text and returns a category
// label and a targeted constraint string for the repair prompt. Returns empty
// strings if the error doesn't match any known pattern.
//
// Categories (checked in priority order):
//   - import_violation: external/missing package imports
//   - undefined_reference: undefined symbols, missing members
//   - type_mismatch: type compatibility errors
//   - syntax_error: malformed syntax, unexpected tokens
func ClassifyCompilerError(errors string) (category, constraint string) {
	lower := strings.ToLower(errors)

	// Import violations — highest priority because they're the most common
	// root cause for local model failures (importing nonexistent packages)
	importPatterns := []string{
		"cannot find module",
		"could not import",
		"no required module provides",
		"cannot find package",
		"module not found",
		"is not in goroot",
		"unresolved import",
	}
	for _, p := range importPatterns {
		if strings.Contains(lower, p) {
			return "import_violation", "Do NOT import external packages. Use only the standard library. Remove or replace any third-party imports."
		}
	}

	// Undefined references — missing types, functions, variables
	undefinedPatterns := []string{
		"undefined:",
		"is not defined",
		"has no member",
		"has no field or method",
		"undeclared name",
		"not declared",
	}
	for _, p := range undefinedPatterns {
		if strings.Contains(lower, p) {
			return "undefined_reference", "Ensure all referenced types, functions, and methods are defined in the current file or properly imported from standard library packages."
		}
	}

	// Type mismatches
	typePatterns := []string{
		"cannot use",
		"is not assignable to type",
		"incompatible types",
		"cannot convert",
		"type mismatch",
		"wrong type",
	}
	for _, p := range typePatterns {
		if strings.Contains(lower, p) {
			return "type_mismatch", "Check type compatibility. Ensure interface implementations match exactly and return types are correct."
		}
	}

	// Syntax errors — catch-all for malformed code
	syntaxPatterns := []string{
		"expected",
		"illegal character",
		"unexpected token",
		"syntax error",
		"unexpected end",
	}
	for _, p := range syntaxPatterns {
		if strings.Contains(lower, p) {
			return "syntax_error", "Fix syntax errors. Output only valid source code with no markdown fences, comments about the code, or explanatory text."
		}
	}

	return "", ""
}

// BuildRepairDAG constructs a single-node synthesis graph that re-generates
// code using the repair prompt. Used for the compilation gate retry loop.
func BuildRepairDAG(taskID, originalCode, compilerErrors, spec, language string, maxLines int, moduleContext string) *compiler.ExecutionGraph {
	prompt := BuildRepairPrompt(originalCode, compilerErrors, spec, language, maxLines, moduleContext)

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
		Edges:      []compiler.GraphEdge{},
		GoalPrompt: fmt.Sprintf("Fix compilation errors in %s code", language),
		MaxCycles:  1,
		CreatedAt:  time.Now().Unix(),
	}
}
