package codegen

import (
	"fmt"
	"time"

	"tzro/internal/compiler"
)

// BuildDiffDAG constructs a single-node DAG for diff-mode code generation.
// The reason_code node uses structured JSON output (GBNF-constrained) to
// produce DiffOutput instead of raw source code.
func BuildDiffDAG(taskID, spec, filePath, language string,
	codeCtx *CodeContext) *compiler.ExecutionGraph {

	if codeCtx != nil && codeCtx.Language != "" {
		language = codeCtx.Language
	}

	existingContent := ""
	siblings := make(map[string]string)
	if codeCtx != nil {
		existingContent = codeCtx.ExistingContent
		siblings = codeCtx.Siblings
	}

	fullPrompt := BuildDiffPrompt(spec, filePath, language, existingContent, siblings)

	return &compiler.ExecutionGraph{
		TaskID:     taskID,
		CreatedAt:  time.Now().Unix(),
		MaxCycles:  1,
		GoalPrompt: fmt.Sprintf("Apply diff edits to %s: %s", filePath, spec),
		Nodes: []compiler.GraphNode{
			{
				ID:           "reason_code",
				Type:         "synthesis",
				Instructions: fullPrompt,
				AllowedTools: []string{},
				Status:       "pending",
				OutputSchema: DiffHunkSchema,
			},
		},
	}
}
