package executor

import (
	"context"
	"strings"
	"testing"
)

func TestSliceContentForMap_SmallFile(t *testing.T) {
	content := "line1\nline2\nline3"
	sliced := SliceContentForMap("test.md", content)
	if sliced != content {
		t.Errorf("expected full content for small file, got %q", sliced)
	}
}

func TestSliceContentForMap_LargeMarkdown(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 300; i++ {
		sb.WriteString("line\n")
	}
	content := sb.String()
	sliced := SliceContentForMap("doc.md", content)
	lines := strings.Split(sliced, "\n")
	if len(lines) > 160 {
		t.Errorf("expected <= 160 lines for large markdown, got %d lines", len(lines))
	}
}

func TestExtractFileInventory_DeterministicTaggingAndRelevant(t *testing.T) {
	schema := &InventorySchema{
		Fields: []InventoryField{
			{Name: "title", MinLength: 3, MaxLength: 100},
			{Name: "status", MinLength: 3, MaxLength: 50},
			{Name: "summary", MinLength: 5, MaxLength: 200},
		},
		Grammar: CompileInventoryGBNF([]InventoryField{
			{Name: "title", MinLength: 3, MaxLength: 100},
			{Name: "status", MinLength: 3, MaxLength: 50},
			{Name: "summary", MinLength: 5, MaxLength: 200},
		}),
	}

	mock := &mockSchemaProbeEngine{
		response: `{"relevant": true, "title": "ADR-0001", "status": "Accepted", "summary": "Decouple strategist from Kahn compiler"}`,
	}

	row, err := ExtractFileInventory(context.Background(), "docs/adr/0001.md", "# ADR 1\nContent", schema, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row == nil {
		t.Fatal("expected non-nil row")
	}
	if row.File != "docs/adr/0001.md" {
		t.Errorf("expected deterministic file tag docs/adr/0001.md, got %q", row.File)
	}
	if !row.Relevant {
		t.Error("expected relevant to be true")
	}
	if row.Fields["title"] != "ADR-0001" || row.Fields["status"] != "Accepted" {
		t.Errorf("unexpected fields: %+v", row.Fields)
	}
}

func TestExtractFileInventory_SkipsIrrelevant(t *testing.T) {
	schema := &InventorySchema{
		Fields: []InventoryField{
			{Name: "title", MinLength: 3, MaxLength: 100},
		},
		Grammar: CompileInventoryGBNF([]InventoryField{
			{Name: "title", MinLength: 3, MaxLength: 100},
		}),
	}

	mock := &mockSchemaProbeEngine{
		response: `{"relevant": false}`,
	}

	row, err := ExtractFileInventory(context.Background(), "temp.txt", "some unimportant text", schema, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row != nil {
		t.Errorf("expected nil row for irrelevant file, got %+v", row)
	}
}
