package store

import (
	"testing"
	"time"
)

func TestStore_ArtifactsPersistenceAndCollisions(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Two datasets with identical prefix rows but different tails must produce DIFFERENT artifact IDs
	data1 := "colA,colB\n1,2\n3,4\n5,6\nTAIL_ONE"
	data2 := "colA,colB\n1,2\n3,4\n5,6\nTAIL_TWO"

	id1, err := s.PutArtifact(&Artifact{
		Type:       "tabular",
		Workspace:  "/workspace/repoA",
		IsRedacted: false,
		Body:       data1,
	})
	if err != nil {
		t.Fatalf("PutArtifact 1 failed: %v", err)
	}

	id2, err := s.PutArtifact(&Artifact{
		Type:       "tabular",
		Workspace:  "/workspace/repoA",
		IsRedacted: true, // flagged as non-byte-exact original
		Body:       data2,
	})
	if err != nil {
		t.Fatalf("PutArtifact 2 failed: %v", err)
	}

	if id1 == id2 {
		t.Errorf("datasets with distinct tails produced colliding artifact IDs: %s == %s", id1, id2)
	}

	// Retrieve artifact 1 and verify properties
	art1, err := s.GetArtifact(id1, "/workspace/repoA")
	if err != nil {
		t.Fatalf("GetArtifact failed: %v", err)
	}
	if art1.Body != data1 {
		t.Errorf("expected body %q, got %q", data1, art1.Body)
	}
	if art1.IsRedacted {
		t.Errorf("expected art1 not redacted")
	}

	// Verify redacted flag on artifact 2
	art2, err := s.GetArtifact(id2, "/workspace/repoA")
	if err != nil {
		t.Fatalf("GetArtifact 2 failed: %v", err)
	}
	if !art2.IsRedacted {
		t.Errorf("expected art2 to be flagged as redacted")
	}

	// Verify multi-workspace isolation
	_, err = s.GetArtifact(id1, "/workspace/repoB")
	if err == nil {
		t.Errorf("expected access denied for cross-workspace retrieval")
	}

	// Test Quota Enforcement
	deleted, err := s.EnforceQuota("/workspace/repoA", 30) // forces deletion of oldest artifact
	if err != nil {
		t.Fatalf("EnforceQuota failed: %v", err)
	}
	if deleted == 0 {
		t.Errorf("expected at least 1 artifact pruned under tight quota")
	}

	// Test TTL Pruning
	past := time.Now().Add(-1 * time.Hour)
	_, _ = s.PutArtifact(&Artifact{
		ID:        "expired_art",
		Body:      "expired content",
		Workspace: "/workspace/repoA",
		ExpiresAt: past,
		Pinned:    false,
	})
	pruned, err := s.PruneExpiredArtifacts()
	if err != nil {
		t.Fatalf("PruneExpiredArtifacts failed: %v", err)
	}
	if pruned == 0 {
		t.Errorf("expected expired artifact to be pruned")
	}
}

func TestStore_GetRecentUnexpiredArtifactIDs(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/workspace/test-artifacts"
	otherWs := "/workspace/other"
	now := time.Now().UTC()

	// Insert: 1 pinned (no expiry), 1 unexpired, 1 expired, 1 in other workspace
	_, err = s.PutArtifact(&Artifact{
		ID:        "art-pinned",
		Type:      "text",
		Workspace: ws,
		Pinned:    true,
		ExpiresAt: now.Add(-1 * time.Hour), // expired but pinned
		Body:      "pinned body",
	})
	if err != nil {
		t.Fatalf("PutArtifact pinned: %v", err)
	}

	_, err = s.PutArtifact(&Artifact{
		ID:        "art-fresh",
		Type:      "text",
		Workspace: ws,
		Pinned:    false,
		ExpiresAt: now.Add(24 * time.Hour), // unexpired
		Body:      "fresh body",
	})
	if err != nil {
		t.Fatalf("PutArtifact fresh: %v", err)
	}

	_, err = s.PutArtifact(&Artifact{
		ID:        "art-expired",
		Type:      "text",
		Workspace: ws,
		Pinned:    false,
		ExpiresAt: now.Add(-1 * time.Hour), // expired and not pinned
		Body:      "expired body",
	})
	if err != nil {
		t.Fatalf("PutArtifact expired: %v", err)
	}

	_, err = s.PutArtifact(&Artifact{
		ID:        "art-other-ws",
		Type:      "text",
		Workspace: otherWs,
		Pinned:    false,
		ExpiresAt: now.Add(24 * time.Hour),
		Body:      "other workspace",
	})
	if err != nil {
		t.Fatalf("PutArtifact other ws: %v", err)
	}

	// Query: should return pinned + fresh, NOT expired or other-ws
	ids, err := s.GetRecentUnexpiredArtifactIDs(ws, 20)
	if err != nil {
		t.Fatalf("GetRecentUnexpiredArtifactIDs: %v", err)
	}

	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	if !idSet["art-pinned"] {
		t.Error("pinned artifact should be included even with past expiry")
	}
	if !idSet["art-fresh"] {
		t.Error("unexpired artifact should be included")
	}
	if idSet["art-expired"] {
		t.Error("expired unpinned artifact should be excluded")
	}
	if idSet["art-other-ws"] {
		t.Error("artifact from other workspace should be excluded")
	}

	// Test limit
	idsLimited, err := s.GetRecentUnexpiredArtifactIDs(ws, 1)
	if err != nil {
		t.Fatalf("GetRecentUnexpiredArtifactIDs with limit: %v", err)
	}
	if len(idsLimited) != 1 {
		t.Errorf("expected 1 result with limit=1, got %d", len(idsLimited))
	}
}
