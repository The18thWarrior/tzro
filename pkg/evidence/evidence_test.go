package evidence_test

import (
	"encoding/json"
	"testing"
	"time"

	"tzro/pkg/evidence"
)

func TestSourceKind_Classification(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		isArtifact   bool
		artifactType string
		expected     evidence.SourceKind
	}{
		{"Go source", "pkg/context/context.go", false, "", evidence.SourceKindCode},
		{"TypeScript source", "src/index.ts", false, "", evidence.SourceKindCode},
		{"Python source", "scripts/test.py", false, "", evidence.SourceKindCode},
		{"YAML config", "config/app.yaml", false, "", evidence.SourceKindConfig},
		{"JSON config", "settings.json", false, "", evidence.SourceKindConfig},
		{"Markdown doc", "docs/adr/0001.md", false, "", evidence.SourceKindDoc},
		{"Text doc", "README.txt", false, "", evidence.SourceKindDoc},
		{"Log artifact", "art_12345", true, "log", evidence.SourceKindLog},
		{"Session artifact", "sess_abcde", true, "session", evidence.SourceKindSession},
		{"Imported document", "imports/manual.pdf", false, "import", evidence.SourceKindImport},
		{"Tabular data", "data_table", true, "tabular", evidence.SourceKindData},
		{"CSV raw file", "data/users.csv", false, "", evidence.SourceKindData},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evidence.ClassifySourceKind(tt.path, tt.isArtifact, tt.artifactType)
			if got != tt.expected {
				t.Errorf("ClassifySourceKind(%q, %v, %q) = %q; want %q",
					tt.path, tt.isArtifact, tt.artifactType, got, tt.expected)
			}
		})
	}
}

func TestProvenanceEnvelope_SerializationAndValidation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	// Test with LineRange anchor
	itemLine := evidence.EvidenceItem{
		SourcePath:  "pkg/context/context.go",
		ContentHash: "a1b2c3d4e5f60123456789abcdef0123456789abcdef0123456789abcdef0123",
		Revision:    "c0ffee1234567890abcdef1234567890abcdef12",
		Timestamp:   now,
		Anchor: evidence.Anchor{
			LineRange: &evidence.LineRange{StartLine: 10, EndLine: 35},
		},
		SourceKind:  evidence.SourceKindCode,
		WorkspaceID: "/workspaces/my-repo",
		Content:     "func Example() string { return \"ok\" }",
	}

	if err := itemLine.Validate(); err != nil {
		t.Fatalf("itemLine.Validate() failed: %v", err)
	}

	data, err := json.Marshal(itemLine)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var unmarshaled evidence.EvidenceItem
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if unmarshaled.Anchor.LineRange == nil || unmarshaled.Anchor.LineRange.StartLine != 10 {
		t.Errorf("unmarshaled LineRange mismatch: got %+v", unmarshaled.Anchor.LineRange)
	}

	// Test with SectionPath anchor
	itemSection := evidence.EvidenceItem{
		SourcePath:  "docs/adr/0001.md",
		ContentHash: "b2c3d4e5f60123456789abcdef0123456789abcdef0123456789abcdef012345",
		Revision:    "c0ffee1234567890abcdef1234567890abcdef12",
		Timestamp:   now,
		Anchor: evidence.Anchor{
			SectionPath: []string{"# Architecture", "## Decision"},
		},
		SourceKind:  evidence.SourceKindDoc,
		WorkspaceID: "/workspaces/my-repo",
		Content:     "We choose SQLite WAL.",
	}

	if err := itemSection.Validate(); err != nil {
		t.Fatalf("itemSection.Validate() failed: %v", err)
	}

	// Test invalid: missing required fields
	invalidItem := evidence.EvidenceItem{
		SourcePath: "some/path",
		// missing ContentHash, SourceKind, WorkspaceID, Anchor
	}
	if err := invalidItem.Validate(); err == nil {
		t.Errorf("expected error for invalid item with missing required fields")
	}
}

func TestProvenanceEnvelope_TabularMetadata(t *testing.T) {
	itemTabular := evidence.EvidenceItem{
		SourcePath:  "data/benchmarks.csv",
		ContentHash: "c3d4e5f60123456789abcdef0123456789abcdef0123456789abcdef01234567",
		Timestamp:   time.Now().UTC(),
		Anchor: evidence.Anchor{
			LineRange: &evidence.LineRange{StartLine: 1, EndLine: 1},
		},
		SourceKind:  evidence.SourceKindData,
		WorkspaceID: "/workspaces/my-repo",
		TabularData: &evidence.TabularMetadata{
			Status:    evidence.TabularStatusAvailable,
			TableName: "tbl_benchmarks",
			Schema: []evidence.ColumnDef{
				{Name: "model", DataType: "TEXT"},
				{Name: "score", DataType: "REAL"},
			},
			RowCount: 120,
		},
	}

	if err := itemTabular.Validate(); err != nil {
		t.Fatalf("tabular Validate() failed: %v", err)
	}

	if itemTabular.TabularData.Status != evidence.TabularStatusAvailable {
		t.Errorf("expected status available, got %s", itemTabular.TabularData.Status)
	}
}

func TestProvenanceEnvelope_Staleness(t *testing.T) {
	// Staleness based on age >= 90 days
	oldTime := time.Now().UTC().Add(-95 * 24 * time.Hour)
	recentTime := time.Now().UTC().Add(-10 * 24 * time.Hour)

	oldItem := evidence.EvidenceItem{
		SourcePath:  "docs/old.md",
		ContentHash: "d4e5f60123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		Timestamp:   oldTime,
		Anchor:      evidence.Anchor{SectionPath: []string{"# Old"}},
		SourceKind:  evidence.SourceKindDoc,
		WorkspaceID: "/ws",
	}

	if !oldItem.IsStale(0) {
		t.Errorf("expected item >= 90 days old to be stale")
	}

	recentItem := evidence.EvidenceItem{
		SourcePath:  "docs/recent.md",
		ContentHash: "e5f60123456789abcdef0123456789abcdef0123456789abcdef012345678901",
		Timestamp:   recentTime,
		Anchor:      evidence.Anchor{SectionPath: []string{"# Recent"}},
		SourceKind:  evidence.SourceKindDoc,
		WorkspaceID: "/ws",
	}

	if recentItem.IsStale(0) {
		t.Errorf("expected recent item to not be stale")
	}

	// Staleness based on commits behind >= 100
	if !recentItem.IsStale(105) {
		t.Errorf("expected item >= 100 commits behind to be stale")
	}
}
