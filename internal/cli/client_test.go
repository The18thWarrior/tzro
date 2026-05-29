package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"tzro/internal/memory"
)

func TestDirectDBClient_MutationsBlocked(t *testing.T) {
	client := NewDirectDBClient("test_tzro.db")

	err := client.AddMemory("user_123", "preference", "bullet formatting", "TUI test context", 0.95)
	if err == nil {
		t.Error("expected error on offline write operation AddMemory, got nil")
	}
	if !errors.Is(err, ErrOfflineMutation) {
		t.Errorf("expected ErrOfflineMutation, got: %v", err)
	}

	err = client.TriggerWorkflow("wf_123")
	if err == nil {
		t.Error("expected error on offline workflow trigger, got nil")
	}
	if !errors.Is(err, ErrOfflineMutation) {
		t.Errorf("expected ErrOfflineMutation, got: %v", err)
	}
}

func TestDirectDBClient_ReadOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tzro_cli_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbFile := filepath.Join(tempDir, "test_tzro.db")

	// Temporarily switch DB path for direct writes during setup
	originalDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(dbFile)
	defer memory.DB.SetDBPathForTesting(originalDBPath)

	err = memory.DB.Init()
	if err != nil {
		t.Fatalf("failed to init SQLite memory.DB: %v", err)
	}
	defer memory.DB.Close()

	// Seed some test data inside the SQLite database using memory package direct writes
	n := memory.KGNode{
		ID:       "node_1",
		NodeType: "contact",
		Name:     "John Doe",
		Source:   "test",
		Weight:   1.0,
	}
	err = memory.DB.AddNode(n)
	if err != nil {
		t.Fatalf("failed to seed test node: %v", err)
	}

	fact := memory.FactMemory{
		ID:         "fact_1",
		UserID:     "user_1",
		Type:       "preference",
		Content:    "Seeded offline test fact",
		Context:    "Testing client.go",
		Confidence: 1.0,
		Source:     "manual",
		CreatedAt:  time.Now(),
	}
	// Direct add via sql transaction to simulate existing database records
	_, err = memory.DB.RawDB().Exec(`
		INSERT INTO fact_memories (id, user_id, type, content, context, confidence, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		fact.ID, fact.UserID, fact.Type, fact.Content, fact.Context, fact.Confidence, fact.Source, fact.CreatedAt)
	if err != nil {
		t.Fatalf("failed to seed test fact: %v", err)
	}

	client := NewDirectDBClient(dbFile)

	// Verify GetMemories reads from SQLite cleanly
	payload, err := client.GetMemories()
	if err != nil {
		t.Fatalf("failed to execute GetMemories: %v", err)
	}

	if len(payload.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(payload.Nodes))
	} else if payload.Nodes[0].Name != "John Doe" {
		t.Errorf("expected node John Doe, got: %s", payload.Nodes[0].Name)
	}

	if len(payload.Facts) != 1 {
		t.Errorf("expected 1 fact memory, got %d", len(payload.Facts))
	} else if payload.Facts[0].Content != "Seeded offline test fact" {
		t.Errorf("expected fact Seeded offline test fact, got: %s", payload.Facts[0].Content)
	}
}
