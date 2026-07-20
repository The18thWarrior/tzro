package compactor

import (
	"strings"
	"testing"
)

func TestSegmentContent_PureCode(t *testing.T) {
	code := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	segments := SegmentContent(code)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	if segments[0].Type != SegmentCode {
		t.Errorf("expected SegmentCode, got %s", segments[0].Type)
	}
}

func TestSegmentContent_FencedCodeBlocks(t *testing.T) {
	content := "# Architecture\n\nThe system uses DAGs.\n\n```go\nfunc RunProbe() {\n}\n```\n\nThe probe supports exploration.\n"

	segments := SegmentContent(content)
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d: %+v", len(segments), segments)
	}

	if segments[0].Type != SegmentText {
		t.Errorf("segment 0: expected Text, got %s", segments[0].Type)
	}
	if segments[1].Type != SegmentCode {
		t.Errorf("segment 1: expected Code, got %s", segments[1].Type)
	}
	if segments[1].Language != "go" {
		t.Errorf("segment 1: expected language 'go', got %q", segments[1].Language)
	}
	if segments[2].Type != SegmentText {
		t.Errorf("segment 2: expected Text, got %s", segments[2].Type)
	}
}

func TestSegmentContent_Tabular(t *testing.T) {
	csv := "name,age,city\nAlice,30,NYC\nBob,25,LA\nCharlie,35,CHI\n"
	segments := SegmentContent(csv)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	if segments[0].Type != SegmentTabular {
		t.Errorf("expected SegmentTabular, got %s", segments[0].Type)
	}
}

func TestSegmentContent_Empty(t *testing.T) {
	segments := SegmentContent("")
	if len(segments) != 0 {
		t.Errorf("expected 0 segments, got %d", len(segments))
	}
}

func TestClassifyContent_GoCode(t *testing.T) {
	code := `package executor

import "context"

// RunProbe executes a bounded exploration.
func RunProbe(ctx context.Context) error {
	return nil
}
`
	if ct := ClassifyContent(code); ct != SegmentCode {
		t.Errorf("expected SegmentCode, got %s", ct)
	}
}

func TestTruncateTabular_SmallTable(t *testing.T) {
	table := "a,b\n1,2\n3,4\n"
	result := TruncateTabular(table, 1000)
	if result != table {
		t.Errorf("expected unchanged table, got %q", result)
	}
}

func TestTruncateTabular_LargeTable(t *testing.T) {
	var lines []string
	lines = append(lines, "name\tage\tcity")
	for i := 0; i < 50; i++ {
		lines = append(lines, "Alice\t30\tNYC")
	}
	table := strings.Join(lines, "\n")

	result := TruncateTabular(table, 1000)
	if !strings.Contains(result, "more rows omitted") {
		t.Error("expected truncation marker in result")
	}
	if !strings.Contains(result, "name\tage\tcity") {
		t.Error("expected header preserved")
	}
}

func TestTruncateTextMiddleOut(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "This is a line of text content for testing purposes.")
	}
	text := strings.Join(lines, "\n")

	result := TruncateTextMiddleOut(text, 3000)
	if !strings.Contains(result, "lines omitted") {
		t.Error("expected omission marker")
	}
	if len(result) > 3000 {
		t.Errorf("expected within budget, got %d chars", len(result))
	}
}
