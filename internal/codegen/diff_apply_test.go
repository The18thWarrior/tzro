package codegen

import (
	"strings"
	"testing"
)

func TestApplyDiffHunks_SingleHunk(t *testing.T) {
	existing := "package foo\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n"

	hunks := []DiffHunk{
		{
			SearchContent:  "return \"hello\"",
			ReplaceContent: "return \"world\"",
			Description:    "change greeting",
		},
	}

	result, err := ApplyDiffHunks(existing, hunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "package foo\n\nfunc Hello() string {\n\treturn \"world\"\n}\n"
	if result != expected {
		t.Errorf("got:\n%s\nwant:\n%s", result, expected)
	}
}

func TestApplyDiffHunks_MultipleHunks(t *testing.T) {
	existing := `package foo

import "fmt"

func Hello() string {
	return "hello"
}

func Goodbye() string {
	return "goodbye"
}

func Main() {
	fmt.Println("start")
}
`
	hunks := []DiffHunk{
		{
			SearchContent:  `return "hello"`,
			ReplaceContent: `return "hi"`,
		},
		{
			SearchContent:  `return "goodbye"`,
			ReplaceContent: `return "bye"`,
		},
		{
			SearchContent:  `fmt.Println("start")`,
			ReplaceContent: `fmt.Println("begin")`,
		},
	}

	result, err := ApplyDiffHunks(existing, hunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, `return "hi"`) {
		t.Error("expected first hunk applied")
	}
	if !strings.Contains(result, `return "bye"`) {
		t.Error("expected second hunk applied")
	}
	if !strings.Contains(result, `fmt.Println("begin")`) {
		t.Error("expected third hunk applied")
	}
	// Originals should be gone
	if strings.Contains(result, `return "hello"`) {
		t.Error("original 'hello' should be replaced")
	}
}

func TestApplyDiffHunks_DuplicateMatch_Fails(t *testing.T) {
	// The searchContent "return nil" appears twice
	existing := `package foo

func A() error {
	return nil
}

func B() error {
	return nil
}
`
	hunks := []DiffHunk{
		{
			SearchContent:  "return nil",
			ReplaceContent: `return fmt.Errorf("error")`,
		},
	}

	_, err := ApplyDiffHunks(existing, hunks)
	if err == nil {
		t.Fatal("expected error for duplicate match")
	}
	if !strings.Contains(err.Error(), "multiple locations") {
		t.Errorf("expected 'multiple locations' in error, got: %v", err)
	}
}

func TestApplyDiffHunks_NoMatch_Fails(t *testing.T) {
	existing := "package foo\n\nfunc Hello() {}\n"

	hunks := []DiffHunk{
		{
			SearchContent:  "this text does not exist in the file",
			ReplaceContent: "replacement",
		},
	}

	_, err := ApplyDiffHunks(existing, hunks)
	if err == nil {
		t.Fatal("expected error for no match")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestApplyDiffHunks_FuzzyWhitespaceMatch(t *testing.T) {
	// File uses tabs, but hunk searchContent uses spaces
	existing := "package foo\n\nfunc Hello() {\n\treturn\t\t\"hello\"\n}\n"

	hunks := []DiffHunk{
		{
			SearchContent:  "return \"hello\"",
			ReplaceContent: "return \"world\"",
		},
	}

	result, err := ApplyDiffHunks(existing, hunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, `return "world"`) {
		t.Error("expected fuzzy-matched replacement to be applied")
	}
	// The original tab-heavy content should be replaced
	if strings.Contains(result, "\"hello\"") {
		t.Error("original content should be replaced")
	}
}

func TestApplyDiffHunks_DeletionHunk(t *testing.T) {
	existing := "package foo\n\n// Deprecated: use NewHello instead\nfunc OldHello() {}\n\nfunc NewHello() {}\n"

	hunks := []DiffHunk{
		{
			SearchContent:  "// Deprecated: use NewHello instead\nfunc OldHello() {}\n\n",
			ReplaceContent: "", // deletion
			Description:    "remove deprecated function",
		},
	}

	result, err := ApplyDiffHunks(existing, hunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, "OldHello") {
		t.Error("deleted content should not be present")
	}
	if !strings.Contains(result, "NewHello") {
		t.Error("non-deleted content should remain")
	}
}

func TestApplyDiffHunks_InsertionHunk(t *testing.T) {
	existing := "package foo\n\nfunc Hello() {}\n"

	hunks := []DiffHunk{
		{
			SearchContent:  "func Hello() {}",
			ReplaceContent: "func Hello() {}\n\nfunc Goodbye() {}",
			Description:    "insert new function after Hello",
		},
	}

	result, err := ApplyDiffHunks(existing, hunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "func Hello() {}") {
		t.Error("original function should remain")
	}
	if !strings.Contains(result, "func Goodbye() {}") {
		t.Error("inserted function should be present")
	}
}

func TestApplyDiffHunks_EmptyFile_Fails(t *testing.T) {
	hunks := []DiffHunk{
		{
			SearchContent:  "something",
			ReplaceContent: "replacement",
		},
	}

	_, err := ApplyDiffHunks("", hunks)
	if err == nil {
		t.Fatal("expected error for empty file")
	}
	if !strings.Contains(err.Error(), "empty file") {
		t.Errorf("expected 'empty file' in error, got: %v", err)
	}
}
