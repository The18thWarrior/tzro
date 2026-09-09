package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/pkg/compactor"
	"tzro/pkg/store"
)

func TestCLI_ExpandAndQueryArtifacts(t *testing.T) {
	tempHome := t.TempDir()
	tzroDir := filepath.Join(tempHome, ".tzro")
	_ = os.MkdirAll(tzroDir, 0755)
	dbPath := filepath.Join(tzroDir, "token_shield.db")

	s, err := store.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Store log artifact
	multilineLog := "Line 1: init\nLine 2: loading\nLine 3: processing\nLine 4: error occurred\nLine 5: shutdown"
	logArtID, err := s.PutArtifact(&store.Artifact{
		Type:      "log",
		Workspace: "/test",
		Body:      multilineLog,
	})
	if err != nil {
		t.Fatalf("PutArtifact log failed: %v", err)
	}

	// 1. Verify byte-for-byte original retrieval
	retrieved, err := s.GetArtifact(logArtID, "")
	if err != nil || retrieved.Body != multilineLog {
		t.Errorf("expected exact recovery of original log")
	}

	// 2. Test line range slice (Lines 2-4)
	allLines := strings.Split(retrieved.Body, "\n")
	slice := strings.Join(allLines[1:4], "\n")
	if !strings.Contains(slice, "Line 2: loading") || !strings.Contains(slice, "Line 4: error occurred") {
		t.Errorf("unexpected slice result: %s", slice)
	}

	// 3. Store tabular artifact
	csvData := "user,metric,score\nalice,latency,12\nbob,latency,45\ncharlie,latency,8"
	td, _ := compactor.DetectTabular(csvData)
	csvArtID, err := s.PutArtifact(&store.Artifact{
		Type:      "tabular",
		Workspace: "/test",
		Body:      csvData,
	})
	if err != nil {
		t.Fatalf("PutArtifact tabular failed: %v", err)
	}

	// Import tabular table
	tblName := "tbl_" + store.SHA256Full(csvData)[:12]
	_ = s.ImportTabular(tblName, td.Columns, td.Rows)

	// Execute SQL query
	res, cols, err := s.QuerySQL("SELECT user, score FROM " + tblName + " WHERE CAST(score AS INTEGER) < 20 ORDER BY user")
	if err != nil {
		t.Fatalf("QuerySQL failed: %v", err)
	}
	if len(res) != 2 || len(cols) != 2 {
		t.Errorf("expected 2 results (alice and charlie), got %d", len(res))
	}

	_ = csvArtID
}

