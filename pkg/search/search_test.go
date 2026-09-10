package search_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tzro/pkg/dlp"
	"tzro/pkg/evidence"
	"tzro/pkg/search"
	"tzro/pkg/store"
)

// createMockDocx creates a minimal valid docx zip file containing word/document.xml with text.
func createMockDocx(t *testing.T, targetPath, text string) {
	f, err := os.Create(targetPath)
	if err != nil {
		t.Fatalf("failed to create mock docx: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("failed to create zip entry: %v", err)
	}

	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>
  </w:body>
</w:document>`

	_, _ = w.Write([]byte(xmlContent))
	_ = zw.Close()
}

func TestEvidenceSearch_FourCategoriesAndProvenance(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Repo text file (Markdown doc)
	docFile := filepath.Join(tempDir, "spec.md")
	docContent := "# Architecture Specification\n\nRate limiting must use sliding window counter.\n"
	_ = os.WriteFile(docFile, []byte(docContent), 0644)

	// 2. Repo binary doc (Office DOCX)
	docxFile := filepath.Join(tempDir, "design.docx")
	createMockDocx(t, docxFile, "Sliding window rate limiting design constraint")

	// 3. Tabular data (un-ingested CSV -> should produce discovery marker)
	csvFile := filepath.Join(tempDir, "limits.csv")
	csvContent := "route,limit,window\n/api/v1/auth,100,60s\n/api/v1/query,500,60s\n"
	_ = os.WriteFile(csvFile, []byte(csvContent), 0644)

	// Store with artifacts (Category 4)
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Ingest a table to verify Category 3 & 4 data status: available
	_ = s.ImportTabular("tbl_limits", []string{"route", "limit", "window"}, [][]string{
		{"/api/v1/auth", "100", "60s"},
		{"/api/v1/query", "500", "60s"},
	})

	// Add a log artifact
	_, _ = s.PutArtifact(&store.Artifact{
		Type:      "log",
		Workspace: tempDir,
		Body:      "ERROR: sliding window counter exceeded maximum capacity at route /api/v1/auth",
	})

	engine := search.NewEngine(s, nil)
	results, err := engine.Search(tempDir, "sliding window", 4000)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results.Items) == 0 {
		t.Fatalf("expected search results for 'sliding window', got 0")
	}

	var foundDoc, foundDocx, foundLog, foundMarker bool
	for _, item := range results.Items {
		// Verify acceptance criterion: valid provenance on EVERY result
		if err := item.Validate(); err != nil {
			t.Errorf("item %s failed provenance validation: %v", item.SourcePath, err)
		}

		if item.SourceKind == evidence.SourceKindDoc && strings.HasSuffix(item.SourcePath, "spec.md") {
			foundDoc = true
		}
		if item.SourceKind == evidence.SourceKindDoc && strings.HasSuffix(item.SourcePath, "design.docx") {
			foundDocx = true
		}
		if item.SourceKind == evidence.SourceKindLog {
			foundLog = true
		}
		if item.SourceKind == evidence.SourceKindData && item.TabularData != nil && item.TabularData.Status == evidence.TabularStatusNotIngested {
			foundMarker = true
		}
	}

	if !foundDoc {
		t.Errorf("expected doc evidence from spec.md")
	}
	if !foundDocx {
		t.Errorf("expected extracted text evidence from design.docx")
	}
	if !foundLog {
		t.Errorf("expected log evidence from store artifact")
	}
	if !foundMarker {
		t.Errorf("expected un-ingested tabular discovery marker for limits.csv")
	}
}

func TestEvidenceSearch_PrivacySilentOmission(t *testing.T) {
	tempDir := t.TempDir()

	secretFile := filepath.Join(tempDir, "secrets.env")
	_ = os.WriteFile(secretFile, []byte("API_SECRET=my-secret-sliding-window\n"), 0644)

	publicFile := filepath.Join(tempDir, "readme.md")
	_ = os.WriteFile(publicFile, []byte("sliding window counter docs\n"), 0644)

	wp := &dlp.WorkspacePolicy{
		Version:       "1.0",
		DefaultAction: dlp.ActionAllow,
		Rules: []dlp.PolicyRule{
			{PathPattern: "*.env*", Action: dlp.ActionDeny},
		},
	}
	policy := dlp.NewPolicyEngine(wp)

	engine := search.NewEngine(nil, policy)
	results, err := engine.Search(tempDir, "sliding window", 4000)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	for _, item := range results.Items {
		if strings.Contains(item.SourcePath, ".env") {
			t.Errorf("denied path %s appeared in search results (privacy violation!)", item.SourcePath)
		}
	}
}

func TestEvidenceSearch_ContentHashDedup(t *testing.T) {
	tempDir := t.TempDir()

	content := "# Duplicate Title\nSame exact content in multiple places for testing dedup.\n"
	_ = os.WriteFile(filepath.Join(tempDir, "file1.md"), []byte(content), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "file2.md"), []byte(content), 0644)

	engine := search.NewEngine(nil, nil)
	results, err := engine.Search(tempDir, "testing dedup", 4000)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results.Items) != 1 {
		t.Errorf("expected exactly 1 deduplicated item, got %d", len(results.Items))
	}
}

func TestEvidenceSearch_ExpiredArtifactTombstone(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/workspaces/my-repo"

	// Insert an expired artifact
	artID, err := s.PutArtifact(&store.Artifact{
		Type:      "log",
		Workspace: ws,
		Body:      "temporary trace log for expired test",
		ExpiresAt: time.Now().UTC().Add(-1 * time.Hour), // already expired
	})
	if err != nil {
		t.Fatalf("PutArtifact failed: %v", err)
	}

	// Evict expired
	_, _ = s.EvictExpiredArtifacts()

	engine := search.NewEngine(s, nil)
	results, err := engine.Search(ws, "temporary trace", 4000)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Should either not show or show tombstone if referenced
	for _, item := range results.Items {
		if item.SourcePath == artID && !item.Expired {
			t.Errorf("expected evicted artifact to be marked expired")
		}
	}
}
