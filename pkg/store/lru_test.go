package store

import (
	"testing"
	"time"
)

func TestStore_ArtifactsLRUEviction(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "test-workspace"

	// 1. Create 3 artifacts: art1, art2, art3
	id1, err := s.PutArtifact(&Artifact{
		Hash:      "hash1",
		Workspace: ws,
		Body:      "content-1",
		Pinned:    false,
	})
	if err != nil {
		t.Fatalf("PutArtifact 1 failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	id2, err := s.PutArtifact(&Artifact{
		Hash:      "hash2",
		Workspace: ws,
		Body:      "content-2",
		Pinned:    true, // PINNED!
	})
	if err != nil {
		t.Fatalf("PutArtifact 2 failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	id3, err := s.PutArtifact(&Artifact{
		Hash:      "hash3",
		Workspace: ws,
		Body:      "content-3",
		Pinned:    false,
	})
	if err != nil {
		t.Fatalf("PutArtifact 3 failed: %v", err)
	}

	// Access art1, making art3 the least-recently accessed among unpinned
	time.Sleep(10 * time.Millisecond)
	art1, err := s.GetArtifact(id1, ws)
	if err != nil {
		t.Fatalf("GetArtifact 1 failed: %v", err)
	}
	if art1.LastAccessedAt.IsZero() {
		t.Errorf("expected LastAccessedAt to be populated")
	}

	// 2. Evict when maxCount = 2 (total count is 3).
	// Unpinned are art1 (accessed recently) and art3 (older). art2 is pinned.
	// Expected eviction: art3 is pruned.
	deleted, err := s.EvictArtifactsLRU(ws, 2, 0)
	if err != nil {
		t.Fatalf("EvictArtifactsLRU failed: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted artifact, got %d", deleted)
	}

	// Verify art3 was deleted
	if _, err := s.GetArtifact(id3, ws); err == nil {
		t.Errorf("expected art3 to be deleted")
	}

	// Verify art1 and pinned art2 still exist
	if _, err := s.GetArtifact(id1, ws); err != nil {
		t.Errorf("expected art1 to still exist: %v", err)
	}
	if _, err := s.GetArtifact(id2, ws); err != nil {
		t.Errorf("expected pinned art2 to still exist: %v", err)
	}
}

func TestStore_FTS5Fallback(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	if !s.HasFTS5() {
		t.Logf("Notice: SQLite runtime did not load FTS5 virtual table, fallback active")
	}

	// Ensure IndexSymbol and SearchSymbols work in either mode
	err = s.IndexSymbol("ResolvePath", "function", "pkg/path.go", 42, "hash1234")
	if err != nil {
		t.Fatalf("IndexSymbol failed: %v", err)
	}

	res, err := s.SearchSymbols("ResolvePath", 10)
	if err != nil {
		t.Fatalf("SearchSymbols failed: %v", err)
	}
	if len(res) == 0 || res[0].Symbol != "ResolvePath" {
		t.Fatalf("expected to find ResolvePath, got: %+v", res)
	}
}
