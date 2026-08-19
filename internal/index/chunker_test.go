package index

import (
	"testing"
)

func TestChunkDocument_MarkdownHeadings(t *testing.T) {
	mdContent := `# ADR-0086: Repository Pre-Index

Overview of the pre-indexing architecture.

## Decision

We introduce the ` + "`Repository Pre-Index`" + ` using ` + "`NewIndexStore`" + ` and ` + "`ChunkDocument`" + `.

### Code Plane

The code plane handles symbols like ` + "`CompileAndSort`" + `.

### Document Plane

The document plane handles markdown and text chunks.

## Consequences

Fast retrieval in < 15ms.
`

	chunks, err := ChunkDocument("docs/adr/0086.md", []byte(mdContent))
	if err != nil {
		t.Fatalf("ChunkDocument failed: %v", err)
	}

	if len(chunks) < 4 {
		t.Fatalf("expected at least 4 chunks from markdown headings, got %d", len(chunks))
	}

	// Verify header hierarchy and content
	foundDecision := false
	for _, c := range chunks {
		if c.Header == "Decision" {
			foundDecision = true
			if len(c.SymbolRefs) < 2 {
				t.Errorf("expected at least 2 symbol refs in Decision chunk, got %v", c.SymbolRefs)
			}
		}
	}
	if !foundDecision {
		t.Errorf("expected 'Decision' chunk in parsed output: %+v", chunks)
	}
}

func TestChunkDocument_PlainText(t *testing.T) {
	plainText := `Paragraph 1: Some architectural notes describing system boundaries.

Paragraph 2: Explains how data flows from ` + "`ExtractSymbols`" + ` into the database.

Paragraph 3: More information on probe node execution.
`

	chunks, err := ChunkDocument("specs/notes.txt", []byte(plainText))
	if err != nil {
		t.Fatalf("ChunkDocument failed on plain text: %v", err)
	}

	if len(chunks) != 3 {
		t.Fatalf("expected 3 paragraph chunks, got %d", len(chunks))
	}

	if chunks[1].SymbolRefs[0] != "ExtractSymbols" {
		t.Errorf("expected 'ExtractSymbols' symbol ref in paragraph 2, got %v", chunks[1].SymbolRefs)
	}
}
