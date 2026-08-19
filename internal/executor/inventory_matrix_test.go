package executor

import (
	"context"
	"strings"
	"testing"
)

func TestFormatInventoryMatrix_TaggedYAML(t *testing.T) {
	rows := []InventoryRow{
		{
			File:     "docs/adr/0001.md",
			Relevant: true,
			Fields: map[string]string{
				"title":   "ADR-0001",
				"status":  "Accepted",
				"summary": "Strategist compiler split",
			},
		},
		{
			File:     "docs/adr/0002.md",
			Relevant: true,
			Fields: map[string]string{
				"title":   "ADR-0002",
				"status":  "Accepted",
				"summary": "Execution envelopes on NodeStates",
			},
		},
	}

	matrix := FormatInventoryMatrix(rows)
	if !strings.Contains(matrix, "---") {
		t.Error("expected YAML record separator '---'")
	}
	if !strings.Contains(matrix, "file: docs/adr/0001.md") {
		t.Errorf("expected file field in matrix, got:\n%s", matrix)
	}
	if !strings.Contains(matrix, "title: ADR-0001") {
		t.Errorf("expected title in matrix, got:\n%s", matrix)
	}
	if !strings.Contains(matrix, "file: docs/adr/0002.md") {
		t.Errorf("expected second file in matrix, got:\n%s", matrix)
	}
}

func TestIsInventoryGoal_MatchesADRSummaryAndArchitecture(t *testing.T) {
	adrGoal := "Read all ADR files in docs/adr/ and produce a consolidated decision log with status, date, and key implications for each decision. Focus on technical accuracy and consistent formatting. Every ADR must be included — there are 37+ ADR files."
	if !IsInventoryGoal(context.Background(), adrGoal) {
		t.Error("expected adrGoal to match IsInventoryGoal")
	}

	archGoal := "Explore and analyze the entire internal/ codebase focusing on package dependencies, data flow, and key abstractions across executor, compiler, inference, memory, tools, compactor, config, task, symbols, cache, stream, routing, and macronodes."
	if !IsInventoryGoal(context.Background(), archGoal) {
		t.Error("expected archGoal to match IsInventoryGoal")
	}

	bugGoal := "Fix the nil pointer dereference in query_builder.go line 42."
	if IsInventoryGoal(context.Background(), bugGoal) {
		t.Error("expected localized bug goal NOT to match IsInventoryGoal")
	}
}
