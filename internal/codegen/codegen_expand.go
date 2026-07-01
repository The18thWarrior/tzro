package codegen

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"tzro/internal/compiler"
)

// BuildPseudocodeExpansionPrompt assembles the structured prompt for expanding
// pseudo-code into compilable source code. This is used when the task complexity
// exceeds T1 and the harness provides pseudo-code for the local model to expand.
func BuildPseudocodeExpansionPrompt(pseudocode, spec, filePath, language, action, existingContent string, siblings map[string]string, maxLines int, moduleContext string) string {
	var b strings.Builder

	b.WriteString("You are a code expander. Convert the following pseudo-code into complete,\n")
	b.WriteString(fmt.Sprintf("compilable %s source code for the target file.\n\n", language))

	b.WriteString("## Pseudo-code\n")
	b.WriteString(pseudocode)
	b.WriteString("\n\n")

	if strings.TrimSpace(spec) != "" {
		b.WriteString("## Spec (for additional context)\n")
		b.WriteString(spec)
		b.WriteString("\n\n")
	}

	b.WriteString("## Target File\n")
	b.WriteString(fmt.Sprintf("Path: %s\n", filePath))
	b.WriteString(fmt.Sprintf("Language: %s\n", language))
	b.WriteString(fmt.Sprintf("Action: %s\n\n", action))

	if action == "update" && existingContent != "" {
		b.WriteString("## Existing Content\n")
		b.WriteString("```\n")
		b.WriteString(existingContent)
		if !strings.HasSuffix(existingContent, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}

	if len(siblings) > 0 {
		b.WriteString("## Sibling Files (for context — follow their conventions)\n")
		names := make([]string, 0, len(siblings))
		for name := range siblings {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			b.WriteString(fmt.Sprintf("### %s\n```\n%s", name, siblings[name]))
			if !strings.HasSuffix(siblings[name], "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n\n")
		}
	}

	if moduleContext != "" {
		b.WriteString("## Available Packages\n")
		b.WriteString(moduleContext)
		if !strings.HasSuffix(moduleContext, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Rules\n")
	b.WriteString("- Output ONLY the complete file content\n")
	b.WriteString("- No markdown fences, no explanation, no commentary\n")
	b.WriteString(fmt.Sprintf("- Expand ALL pseudo-code constructs into valid %s syntax\n", language))
	b.WriteString("- Include ALL necessary imports, type declarations, and error handling\n")
	b.WriteString("- Follow the conventions visible in sibling files (naming, formatting, imports)\n")
	b.WriteString(fmt.Sprintf("- Maximum %d lines\n", maxLines))

	return b.String()
}

// BuildPseudocodeExpansionDAG constructs the execution graph for pseudo-code expansion.
// It produces a single reason_code node with the expansion prompt baked in.
func BuildPseudocodeExpansionDAG(taskID, pseudocode, spec, filePath, language string, maxLines int, codeCtx *CodeContext) *compiler.ExecutionGraph {
	action := "create"
	if codeCtx != nil && codeCtx.Exists {
		action = "update"
	}
	if codeCtx != nil && codeCtx.Language != "" {
		language = codeCtx.Language
	}

	var existingContent string
	var siblings map[string]string
	if codeCtx != nil {
		existingContent = codeCtx.ExistingContent
		siblings = codeCtx.Siblings
	}

	moduleContext := DiscoverModuleContext(filePath, language)
	fullPrompt := BuildPseudocodeExpansionPrompt(pseudocode, spec, filePath, language, action,
		existingContent, siblings, maxLines, moduleContext)

	return &compiler.ExecutionGraph{
		TaskID:     taskID,
		CreatedAt:  time.Now().Unix(),
		MaxCycles:  1,
		GoalPrompt: fmt.Sprintf("Expand pseudo-code into compilable %s for %s", language, filePath),
		MutationBudget: &compiler.MutationBudget{
			MaxSpawns:       2,
			RemainingSpawns: 2,
		},
		Nodes: []compiler.GraphNode{
			{
				ID:             "reason_code",
				Type:           "synthesis",
				Instructions:   fullPrompt,
				AllowedTools:   []string{},
				Status:         "pending",
				OutputFormat:   "source_code",
				OutputLanguage: language,
			},
			{
				ID:                  "validate_code",
				Type:                "synthesis",
				Instructions:        fmt.Sprintf("Validate that the expanded %s code compiles successfully.", language),
				AllowedTools:        []string{},
				Status:              "pending",
				ActivationThreshold: 0.9,
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "reason_code", TargetID: "validate_code"},
		},
	}
}
