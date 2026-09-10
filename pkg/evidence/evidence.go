package evidence

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// SourceKind represents one of the seven deterministic evidence classifications.
type SourceKind string

const (
	SourceKindCode    SourceKind = "code"
	SourceKindConfig  SourceKind = "config"
	SourceKindDoc     SourceKind = "doc"
	SourceKindLog     SourceKind = "log"
	SourceKindSession SourceKind = "session"
	SourceKindImport  SourceKind = "import"
	SourceKindData    SourceKind = "data"
)

// LineRange specifies a 1-based start and end line for text/code excerpts.
type LineRange struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

// Anchor is a union type representing either a line range or a section heading path.
type Anchor struct {
	LineRange   *LineRange `json:"line_range,omitempty"`
	SectionPath []string   `json:"section_path,omitempty"`
}

// IsValid returns true if at least one anchor variant is populated.
func (a Anchor) IsValid() bool {
	if a.LineRange != nil && a.LineRange.StartLine > 0 && a.LineRange.EndLine >= a.LineRange.StartLine {
		return true
	}
	if len(a.SectionPath) > 0 {
		return true
	}
	return false
}

// TabularStatus represents availability of structured tabular data in SQLite.
const (
	TabularStatusAvailable   = "available"
	TabularStatusNotIngested = "not_ingested"
)

// ColumnDef describes a column in an imported tabular dataset.
type ColumnDef struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"`
}

// TabularMetadata holds structured metadata for tabular data.
type TabularMetadata struct {
	Status    string      `json:"status"` // available | not_ingested
	TableName string      `json:"table_name,omitempty"`
	Schema    []ColumnDef `json:"schema,omitempty"`
	RowCount  int         `json:"row_count,omitempty"`
}

// EvidenceItem is the unified Evidence Provenance Envelope attached to every search result.
type EvidenceItem struct {
	SourcePath  string           `json:"source_path"`
	ContentHash string           `json:"content_hash"`
	Revision    string           `json:"revision,omitempty"`
	Timestamp   time.Time        `json:"timestamp"`
	Anchor      Anchor           `json:"anchor"`
	SourceKind  SourceKind       `json:"source_kind"`
	WorkspaceID string           `json:"workspace_id"`
	Content     string           `json:"content,omitempty"`
	TabularData *TabularMetadata `json:"tabular_data,omitempty"`
	Deleted     bool             `json:"deleted,omitempty"`
	Expired     bool             `json:"expired,omitempty"`
	Score       float64          `json:"score,omitempty"`
}

// Validate checks that all required fields for the provenance envelope are valid.
func (e *EvidenceItem) Validate() error {
	if strings.TrimSpace(e.SourcePath) == "" {
		return errors.New("provenance envelope missing source_path")
	}
	if strings.TrimSpace(e.ContentHash) == "" {
		return errors.New("provenance envelope missing content_hash")
	}
	if strings.TrimSpace(string(e.SourceKind)) == "" {
		return errors.New("provenance envelope missing source_kind")
	}
	if strings.TrimSpace(e.WorkspaceID) == "" {
		return errors.New("provenance envelope missing workspace_id")
	}
	if !e.Anchor.IsValid() {
		return errors.New("provenance envelope missing valid anchor (line range or section path)")
	}
	return nil
}

// IsStale returns true if the evidence is >= 100 commits behind HEAD OR >= 90 days old.
func (e *EvidenceItem) IsStale(commitsBehind int) bool {
	if commitsBehind >= 100 {
		return true
	}
	if !e.Timestamp.IsZero() && time.Since(e.Timestamp) >= 90*24*time.Hour {
		return true
	}
	return false
}

// ClassifySourceKind assigns one of the seven deterministic classifications
// strictly by file extension or store origin metadata — never by path heuristics.
func ClassifySourceKind(path string, isArtifact bool, artifactType string) SourceKind {
	if isArtifact {
		switch strings.ToLower(artifactType) {
		case "log":
			return SourceKindLog
		case "session":
			return SourceKindSession
		case "tabular", "data":
			return SourceKindData
		case "import":
			return SourceKindImport
		default:
			return SourceKindDoc
		}
	}

	if artifactType == "import" {
		return SourceKindImport
	}

	ext := strings.ToLower(filepath.Ext(path))
	base := strings.ToLower(filepath.Base(path))

	// Data files (.csv, .tsv)
	if ext == ".csv" || ext == ".tsv" {
		return SourceKindData
	}

	// Code files
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java",
		".c", ".cpp", ".cc", ".cxx", ".h", ".hpp", ".rb", ".php", ".cs":
		return SourceKindCode
	}

	// Config files
	if strings.HasPrefix(base, ".env") {
		return SourceKindConfig
	}
	switch ext {
	case ".yaml", ".yml", ".json", ".toml", ".ini", ".cfg", ".conf":
		return SourceKindConfig
	}

	// Doc files (remaining text files including markdown, text, rst, etc.)
	return SourceKindDoc
}
