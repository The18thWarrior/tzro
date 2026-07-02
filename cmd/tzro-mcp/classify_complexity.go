package main

import (
	"tzro/internal/codegen"
)

// classifyCodeComplexity delegates to codegen.ClassifyCodeComplexity.
// This wrapper preserves the unexported calling convention used throughout tools.go.
func classifyCodeComplexity(spec string, codeCtx *codegen.CodeContext) string {
	return codegen.ClassifyCodeComplexity(spec, codeCtx)
}

// contains checks if a slice includes a value.
func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}
