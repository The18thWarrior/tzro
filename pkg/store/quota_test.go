package store

import (
	"strings"
	"testing"
	"time"
)

func TestPutArtifact_RejectsOversizedBody(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// 20 MB + 1 byte should be rejected
	oversizedBody := strings.Repeat("x", 20*1024*1024+1)

	_, err = s.PutArtifact(&Artifact{
		Type:      "log",
		Workspace: "/ws/test",
		Body:      oversizedBody,
	})
	if err == nil {
		t.Fatal("expected PutArtifact to reject body exceeding 20 MB, but got nil error")
	}

	// Exactly 20 MB should be accepted
	maxBody := strings.Repeat("x", 20*1024*1024)
	id, err := s.PutArtifact(&Artifact{
		Type:      "log",
		Workspace: "/ws/test",
		Body:      maxBody,
	})
	if err != nil {
		t.Fatalf("expected PutArtifact to accept exactly 20 MB body, got: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty artifact ID for 20 MB body")
	}
}

func TestPutArtifact_EnforcesCountQuotaOnWrite(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/ws/count-quota"

	// Insert DefaultWorkspaceMaxCount + 5 artifacts (each ~10 bytes)
	limit := DefaultWorkspaceMaxCount + 5
	for i := 0; i < limit; i++ {
		body := strings.Repeat("a", 10) + string(rune(i))
		_, err := s.PutArtifact(&Artifact{
			Type:      "text",
			Workspace: ws,
			Body:      body,
		})
		if err != nil {
			t.Fatalf("PutArtifact %d failed: %v", i, err)
		}
	}

	// After inserting above the limit, count should be <= DefaultWorkspaceMaxCount
	count, _, err := s.GetWorkspaceArtifactStats(ws)
	if err != nil {
		t.Fatalf("GetWorkspaceArtifactStats failed: %v", err)
	}
	if count > DefaultWorkspaceMaxCount {
		t.Errorf("expected count <= %d after quota enforcement, got %d", DefaultWorkspaceMaxCount, count)
	}
}

func TestPutArtifact_EnforcesBytesQuotaOnWrite(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/ws/bytes-quota"

	// Insert artifacts totaling > 100 MB, each just under 20 MB
	chunkSize := 19 * 1024 * 1024 // 19 MB each
	numChunks := 6                // 6 × 19 MB = 114 MB > 100 MB limit
	for i := 0; i < numChunks; i++ {
		body := strings.Repeat(string(rune('A'+i)), chunkSize)
		_, err := s.PutArtifact(&Artifact{
			Type:      "log",
			Workspace: ws,
			Body:      body,
		})
		if err != nil {
			t.Fatalf("PutArtifact chunk %d failed: %v", i, err)
		}
	}

	// After quota enforcement, total bytes should be <= DefaultWorkspaceMaxBytes
	_, totalBytes, err := s.GetWorkspaceArtifactStats(ws)
	if err != nil {
		t.Fatalf("GetWorkspaceArtifactStats failed: %v", err)
	}
	if totalBytes > DefaultWorkspaceMaxBytes {
		t.Errorf("expected totalBytes <= %d after quota enforcement, got %d", DefaultWorkspaceMaxBytes, totalBytes)
	}
}

func TestPutArtifact_PinnedArtifactsSurviveQuotaEviction(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/ws/pinned-survive"

	// Insert a pinned artifact first
	pinnedID, err := s.PutArtifact(&Artifact{
		Type:      "text",
		Workspace: ws,
		Body:      "pinned content",
		Pinned:    true,
	})
	if err != nil {
		t.Fatalf("PutArtifact pinned failed: %v", err)
	}

	// Fill workspace past the count quota with unpinned artifacts
	for i := 0; i < DefaultWorkspaceMaxCount+5; i++ {
		body := strings.Repeat("u", 10) + string(rune(i))
		_, err := s.PutArtifact(&Artifact{
			Type:      "text",
			Workspace: ws,
			Body:      body,
		})
		if err != nil {
			t.Fatalf("PutArtifact %d failed: %v", i, err)
		}
	}

	// Pinned artifact must still be retrievable
	art, err := s.GetArtifact(pinnedID, ws)
	if err != nil {
		t.Fatalf("pinned artifact should survive quota eviction, got error: %v", err)
	}
	if !art.Pinned {
		t.Error("expected artifact to be pinned")
	}
	if art.Body != "pinned content" {
		t.Errorf("expected pinned body preserved, got %q", art.Body)
	}
}

func TestEvictExpiredArtifacts_RemovesUnpinnedExpired(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/ws/expiry"
	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(24 * time.Hour)

	// Reset sweep timer so PutArtifact doesn't opportunistically sweep
	s.lastSweepTime = time.Now()

	// Insert expired unpinned artifact
	expiredID, _ := s.PutArtifact(&Artifact{
		ID:        "art-expired-1",
		Type:      "text",
		Workspace: ws,
		Body:      "old data",
		ExpiresAt: past,
		Pinned:    false,
	})

	// Insert unexpired artifact
	freshID, _ := s.PutArtifact(&Artifact{
		ID:        "art-fresh-1",
		Type:      "text",
		Workspace: ws,
		Body:      "fresh data",
		ExpiresAt: future,
		Pinned:    false,
	})

	deleted, err := s.EvictExpiredArtifacts()
	if err != nil {
		t.Fatalf("EvictExpiredArtifacts failed: %v", err)
	}
	if deleted == 0 {
		t.Error("expected at least 1 expired artifact evicted")
	}

	// Expired artifact should be gone
	if _, err := s.GetArtifact(expiredID, ws); err == nil {
		t.Error("expected expired artifact to be evicted")
	}

	// Fresh artifact should survive
	if _, err := s.GetArtifact(freshID, ws); err != nil {
		t.Errorf("expected fresh artifact to survive, got: %v", err)
	}
}

func TestEvictExpiredArtifacts_PreservesPinnedExpired(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/ws/expiry-pinned"
	past := time.Now().Add(-2 * time.Hour)

	// Insert expired BUT pinned artifact
	pinnedExpiredID, _ := s.PutArtifact(&Artifact{
		ID:        "art-pinned-expired",
		Type:      "text",
		Workspace: ws,
		Body:      "pinned expired data",
		ExpiresAt: past,
		Pinned:    true,
	})

	// Insert expired unpinned artifact
	_, _ = s.PutArtifact(&Artifact{
		ID:        "art-unpinned-expired",
		Type:      "text",
		Workspace: ws,
		Body:      "unpinned expired data",
		ExpiresAt: past,
		Pinned:    false,
	})

	_, err = s.EvictExpiredArtifacts()
	if err != nil {
		t.Fatalf("EvictExpiredArtifacts failed: %v", err)
	}

	// Pinned expired artifact must survive
	art, err := s.GetArtifact(pinnedExpiredID, ws)
	if err != nil {
		t.Fatalf("expected pinned expired artifact to survive eviction, got: %v", err)
	}
	if art.Body != "pinned expired data" {
		t.Errorf("expected pinned body preserved, got %q", art.Body)
	}
}

func TestListArtifacts_WorkspaceScopedWithoutBody(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/ws/list-test"
	otherWs := "/ws/other"

	_, _ = s.PutArtifact(&Artifact{
		ID: "art-list-1", Type: "text", Workspace: ws, Body: "body one",
	})
	_, _ = s.PutArtifact(&Artifact{
		ID: "art-list-2", Type: "log", Workspace: ws, Body: "body two", Pinned: true,
	})
	_, _ = s.PutArtifact(&Artifact{
		ID: "art-other", Type: "text", Workspace: otherWs, Body: "other workspace body",
	})

	artifacts, err := s.ListArtifacts(ws)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}

	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts for workspace, got %d", len(artifacts))
	}

	// Bodies should be empty (not loaded)
	for _, a := range artifacts {
		if a.Body != "" {
			t.Errorf("expected empty body in list result, got %q for %s", a.Body, a.ID)
		}
		if a.Workspace != ws {
			t.Errorf("expected workspace %q, got %q", ws, a.Workspace)
		}
	}
}

func TestGetWorkspaceArtifactStats_AccurateCountAndBytes(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/ws/stats-test"

	_, _ = s.PutArtifact(&Artifact{
		ID: "art-stat-1", Type: "text", Workspace: ws, Body: "hello",
	})
	_, _ = s.PutArtifact(&Artifact{
		ID: "art-stat-2", Type: "text", Workspace: ws, Body: "world!",
	})
	// Different workspace — should not count
	_, _ = s.PutArtifact(&Artifact{
		ID: "art-stat-other", Type: "text", Workspace: "/ws/other", Body: "nope",
	})

	count, totalBytes, err := s.GetWorkspaceArtifactStats(ws)
	if err != nil {
		t.Fatalf("GetWorkspaceArtifactStats failed: %v", err)
	}

	if count != 2 {
		t.Errorf("expected count=2, got %d", count)
	}
	expectedBytes := int64(len("hello") + len("world!"))
	if totalBytes != expectedBytes {
		t.Errorf("expected totalBytes=%d, got %d", expectedBytes, totalBytes)
	}
}
