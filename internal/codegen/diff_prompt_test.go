package codegen

import (
	"strings"
	"testing"
)

func TestBuildDiffPrompt_IncludesExistingContent(t *testing.T) {
	existing := "package foo\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n"

	prompt := BuildDiffPrompt(
		"Change greeting to world",
		"/path/to/hello.go",
		"go",
		existing,
		nil,
	)

	// Must include the spec
	if !strings.Contains(prompt, "Change greeting to world") {
		t.Error("prompt should contain the spec")
	}
	// Must include existing content
	if !strings.Contains(prompt, "func Hello()") {
		t.Error("prompt should contain existing file content")
	}
	// Must include file path
	if !strings.Contains(prompt, "/path/to/hello.go") {
		t.Error("prompt should contain the file path")
	}
	// Must include language
	if !strings.Contains(prompt, "go") {
		t.Error("prompt should mention the language")
	}
	// Must instruct structured JSON output
	if !strings.Contains(prompt, "hunks") {
		t.Error("prompt should instruct hunks-based JSON output")
	}
	// Must mention searchContent
	if !strings.Contains(prompt, "searchContent") {
		t.Error("prompt should reference searchContent field")
	}
}

func TestBuildDiffPrompt_IncludesSiblings(t *testing.T) {
	existing := "package foo\n\nfunc Hello() {}\n"

	siblings := map[string]string{
		"types.go": "package foo\n\ntype Config struct{}\n",
		"utils.go": "package foo\n\nfunc Helper() {}\n",
	}

	prompt := BuildDiffPrompt(
		"Add logging",
		"/path/to/hello.go",
		"go",
		existing,
		siblings,
	)

	if !strings.Contains(prompt, "types.go") {
		t.Error("prompt should include sibling file name types.go")
	}
	if !strings.Contains(prompt, "type Config struct") {
		t.Error("prompt should include sibling content")
	}
	if !strings.Contains(prompt, "utils.go") {
		t.Error("prompt should include sibling file name utils.go")
	}
}
