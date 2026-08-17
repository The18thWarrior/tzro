package executor

import (
	"fmt"
	"strings"
	"testing"
)

// --- Slice 3b RED (Run 32 fix): n-gram idiom exclusion for codegen output ---

// TestValidateSynthesis_CodegenIdioms_NotFlagged verifies that a Go file with
// 6 HTTP handlers each containing "if err != nil { http.Error(...) }" is not
// rejected when WithCodegenOutput() is set.
//
// The minimum n-gram threshold raises from 4 to 8 with WithCodegenOutput(),
// meaning at least 8 occurrences of an identical 5-word sequence are needed
// to trigger rejection. 6 handlers produce fewer than 8 repetitions.
func TestValidateSynthesis_CodegenIdioms_NotFlagged(t *testing.T) {
	var b strings.Builder
	b.WriteString("package main\n\nimport \"net/http\"\n\n")
	// 6 handlers with structurally similar but varied implementations
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"}
	for i, method := range methods {
		fmt.Fprintf(&b, "func handle%s%d(w http.ResponseWriter, r *http.Request) {\n", method, i)
		fmt.Fprintf(&b, "\tdata, err := process%s(r)\n", method)
		fmt.Fprintf(&b, "\tif err != nil {\n\t\thttp.Error(w, \"internal error\", http.StatusInternalServerError)\n\t\treturn\n\t}\n")
		fmt.Fprintf(&b, "\tw.Write(data)\n}\n\n")
	}
	output := b.String()

	reason := validateSynthesisOutput(output, WithCodegenOutput())
	if reason != "" {
		t.Errorf("validateSynthesisOutput with WithCodegenOutput() must not reject idiomatic Go (%d handlers): %s", len(methods), reason)
	}
}

// TestValidateSynthesis_CodegenIdioms_GenuineRepetition_StillFlagged verifies
// that genuinely degenerate repetition (same non-idiomatic 5-word sequence
// 20+ times) is still caught even with WithCodegenOutput().
func TestValidateSynthesis_CodegenIdioms_GenuineRepetition_StillFlagged(t *testing.T) {
	degenPhrase := "this is degenerate output here"
	output := "package main\n\n" + strings.Repeat(degenPhrase+"\n", 20)

	reason := validateSynthesisOutput(output, WithCodegenOutput())
	if reason == "" {
		t.Error("expected validateSynthesisOutput to reject genuinely repetitive non-idiomatic content even with WithCodegenOutput()")
	}
}
