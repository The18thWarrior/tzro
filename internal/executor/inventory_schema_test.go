package executor

import (
	"context"
	"strings"
	"testing"

	"tzro/internal/inference"
)

type mockSchemaProbeEngine struct {
	response string
	err      error
}

func (m *mockSchemaProbeEngine) Infer(ctx context.Context, systemPrompt, userPrompt, gbnfSchema string, target ModelTarget) (string, error) {
	return m.response, m.err
}

func (m *mockSchemaProbeEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	return m.response, m.err
}

func TestCompileInventoryGBNF_Structure(t *testing.T) {
	fields := []InventoryField{
		{Name: "title", MinLength: 5, MaxLength: 100, Description: "Title of document"},
		{Name: "status", MinLength: 3, MaxLength: 50, Description: "Document status"},
		{Name: "decision", MinLength: 10, MaxLength: 256, Description: "Core decision"},
	}

	grammar := CompileInventoryGBNF(fields)
	if !strings.Contains(grammar, "root ::=") {
		t.Errorf("expected grammar to contain root rule, got:\n%s", grammar)
	}
	if !strings.Contains(grammar, `"\"relevant\": false"`) && !strings.Contains(grammar, `\"relevant\":`) {
		t.Errorf("expected grammar to contain relevant boolean escape, got:\n%s", grammar)
	}
	if !strings.Contains(grammar, `"\"title\":"`) {
		t.Errorf("expected grammar to contain title field, got:\n%s", grammar)
	}
	if !strings.Contains(grammar, `"\"status\":"`) {
		t.Errorf("expected grammar to contain status field, got:\n%s", grammar)
	}
	if !strings.Contains(grammar, `"\"decision\":"`) {
		t.Errorf("expected grammar to contain decision field, got:\n%s", grammar)
	}
}

func TestDeriveInventorySchema_Fallback(t *testing.T) {
	mock := &mockSchemaProbeEngine{
		response: "Not a valid JSON response",
	}

	schema, err := DeriveInventorySchema(context.Background(), "Summarize all ADRs", mock)
	if err != nil {
		t.Fatalf("expected fallback schema, got error: %v", err)
	}
	if len(schema.Fields) < 3 || len(schema.Fields) > 6 {
		t.Errorf("expected between 3 and 6 fields in fallback schema, got %d", len(schema.Fields))
	}
	if schema.Grammar == "" {
		t.Error("expected non-empty GBNF grammar in schema")
	}
}

func TestDeriveInventorySchema_Success(t *testing.T) {
	mock := &mockSchemaProbeEngine{
		response: `{
			"fields": [
				{"name": "id", "description": "ADR number", "minLength": 3, "maxLength": 20},
				{"name": "title", "description": "ADR title", "minLength": 5, "maxLength": 100},
				{"name": "status", "description": "ADR status", "minLength": 4, "maxLength": 30},
				{"name": "summary", "description": "Core decision", "minLength": 10, "maxLength": 200}
			]
		}`,
	}

	schema, err := DeriveInventorySchema(context.Background(), "Read all ADR files in docs/adr/ and summarize them", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(schema.Fields) != 4 {
		t.Fatalf("expected 4 fields, got %d", len(schema.Fields))
	}
	if schema.Fields[0].Name != "id" || schema.Fields[1].Name != "title" {
		t.Errorf("unexpected fields: %+v", schema.Fields)
	}
	if schema.Grammar == "" {
		t.Error("expected non-empty GBNF grammar")
	}
}
