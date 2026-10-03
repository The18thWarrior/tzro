package session

import (
	cryptoRand "crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tzro/pkg/store"
)

// FileSnapshot tracks a modified file path and its cryptographic hashes and status.
type FileSnapshot struct {
	Path           string `json:"path"`
	Hash           string `json:"hash,omitempty"`            // Legacy / primary hash
	OldPath        string `json:"old_path,omitempty"`        // Original path if renamed
	StagedHash     string `json:"staged_hash,omitempty"`     // SHA-256 of index version
	WorktreeHash   string `json:"worktree_hash,omitempty"`   // SHA-256 of worktree version
	Status         string `json:"status,omitempty"`          // "modified", "untracked", "deleted", "renamed", etc.
	StagedStatus   string `json:"staged_status,omitempty"`   // Git index status code (e.g. M, A, D, R)
	WorktreeStatus string `json:"worktree_status,omitempty"` // Git worktree status code (e.g. M, D, ?)
}

// CheckExecution records an executed test, build, or verify command.
type CheckExecution struct {
	Command      string            `json:"command"`
	ExitCode     int               `json:"exit_code"`
	Timestamp    time.Time         `json:"timestamp"`
	OutputID     string            `json:"output_id,omitempty"`    // Artifact ID of full output
	ScopeFiles   []string          `json:"scope_files,omitempty"`  // Per-check files covered
	ScopeHashes  map[string]string `json:"scope_hashes,omitempty"` // Execution-time hashes: path -> sha256
	Fingerprints map[string]string `json:"fingerprints,omitempty"` // Execution-time config/dependency fingerprints
}

// ActiveSymbol captures an affected symbol definition from git diff analysis.
type ActiveSymbol struct {
	Name           string `json:"name"`
	Kind           string `json:"kind,omitempty"`
	FilePath       string `json:"file_path"`
	Line           int    `json:"line"`
	EndLine        int    `json:"end_line,omitempty"`
	Package        string `json:"package,omitempty"`
	SourceSnapshot string `json:"source_snapshot,omitempty"`
	Precision      string `json:"precision,omitempty"` // "precise" | "syntactic"
}

// CommandEvent records a structured shell command execution for task continuity.
type CommandEvent struct {
	ID          string     `json:"id"`
	DisplayText string     `json:"display_text"`
	Cwd         string     `json:"cwd"`
	Workspace   string     `json:"workspace"`
	SessionID   string     `json:"session_id"`
	ShellID     string     `json:"shell_id,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ExitStatus  *int       `json:"exit_status,omitempty"`
}

// SessionManifest represents a complete, portable agent session state.
type SessionManifest struct {
	SchemaVersion  int              `json:"schema_version"`
	ID             string           `json:"id"`
	Workspace      string           `json:"workspace"`
	Worktree       string           `json:"worktree,omitempty"`
	Branch         string           `json:"branch"`
	IsDetached     bool             `json:"is_detached,omitempty"`
	HeadCommit     string           `json:"head_commit,omitempty"`
	IndexState     string           `json:"index_state,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	PausedAt       *time.Time       `json:"paused_at,omitempty"`
	Objective      string           `json:"objective"`
	Constraints    []string         `json:"constraints"`
	Decisions      []string         `json:"decisions"`
	ChangedFiles   []FileSnapshot   `json:"changed_files"`
	ActiveSymbols  []ActiveSymbol   `json:"active_symbols,omitempty"`
	Checks         []CheckExecution `json:"checks"`
	RecentCommands []CommandEvent   `json:"recent_commands,omitempty"`
	PendingTasks   []string         `json:"pending_tasks"`
	ArtifactIDs    []string         `json:"artifact_ids"`
}

// NewSessionManifest creates a new initialized SessionManifest with SchemaVersion 2.
func NewSessionManifest(id, workspace, branch, objective string) *SessionManifest {
	return &SessionManifest{
		SchemaVersion:  2,
		ID:             id,
		Workspace:      workspace,
		Branch:         branch,
		CreatedAt:      time.Now().UTC(),
		Objective:      objective,
		Constraints:    []string{},
		Decisions:      []string{},
		ChangedFiles:   []FileSnapshot{},
		ActiveSymbols:  []ActiveSymbol{},
		Checks:         []CheckExecution{},
		RecentCommands: []CommandEvent{},
		PendingTasks:   []string{},
		ArtifactIDs:    []string{},
	}
}

// NewSessionManifestV3 creates a new initialized SessionManifest with SchemaVersion 3.
func NewSessionManifestV3(id, workspace, branch, objective string) *SessionManifest {
	return &SessionManifest{
		SchemaVersion:  3,
		ID:             id,
		Workspace:      workspace,
		Branch:         branch,
		CreatedAt:      time.Now().UTC(),
		Objective:      objective,
		Constraints:    []string{},
		Decisions:      []string{},
		ChangedFiles:   []FileSnapshot{},
		ActiveSymbols:  []ActiveSymbol{},
		Checks:         []CheckExecution{},
		RecentCommands: []CommandEvent{},
		PendingTasks:   []string{},
		ArtifactIDs:    []string{},
	}
}

// ToJSON serializes the session manifest to formatted JSON.
func (sm *SessionManifest) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(sm, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// FromJSON deserializes a JSON string into a SessionManifest, checking version compatibility.
func FromJSON(data string) (*SessionManifest, error) {
	var sm SessionManifest
	if err := json.Unmarshal([]byte(data), &sm); err != nil {
		return nil, fmt.Errorf("invalid session manifest JSON: %w", err)
	}
	if sm.SchemaVersion < 1 || sm.SchemaVersion > 3 {
		return nil, fmt.Errorf("incompatible schema version %d (expected 1, 2, or 3)", sm.SchemaVersion)
	}
	return &sm, nil
}

// FormatMarkdown outputs a clean, readable human Markdown summary for handoff review.
func (sm *SessionManifest) FormatMarkdown() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Agent Session Handoff: %s\n\n", sm.ID))
	sb.WriteString(fmt.Sprintf("- **Workspace:** `%s`\n", sm.Workspace))
	sb.WriteString(fmt.Sprintf("- **Branch:** `%s`\n", sm.Branch))
	sb.WriteString(fmt.Sprintf("- **Saved At:** %s\n\n", sm.CreatedAt.Format(time.RFC3339)))

	sb.WriteString("## 🎯 Objective\n")
	sb.WriteString(fmt.Sprintf("%s\n\n", sm.Objective))

	if len(sm.Constraints) > 0 {
		sb.WriteString("## ⚠️ Approved Constraints\n")
		for _, c := range sm.Constraints {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}

	if len(sm.Decisions) > 0 {
		sb.WriteString("## 💡 Architectural Decisions\n")
		for _, d := range sm.Decisions {
			sb.WriteString(fmt.Sprintf("- %s\n", d))
		}
		sb.WriteString("\n")
	}

	if len(sm.ChangedFiles) > 0 {
		sb.WriteString("## 📝 Modified Files\n")
		for _, f := range sm.ChangedFiles {
			sb.WriteString(fmt.Sprintf("- `%s` (hash: `%s`)\n", f.Path, f.Hash))
		}
		sb.WriteString("\n")
	}

	if len(sm.Checks) > 0 {
		sb.WriteString("## ✅ Verified Evidence (Executed Checks)\n")
		for _, ch := range sm.Checks {
			status := "PASS"
			if ch.ExitCode != 0 {
				status = fmt.Sprintf("FAIL (%d)", ch.ExitCode)
			}
			artRef := ""
			if ch.OutputID != "" {
				artRef = fmt.Sprintf(" [Artifact: `%s`]", ch.OutputID)
			}
			sb.WriteString(fmt.Sprintf("- `%s` -> **%s** (%s)%s\n", ch.Command, status, ch.Timestamp.Format("15:04:05"), artRef))
		}
		sb.WriteString("\n")
	}

	if len(sm.PendingTasks) > 0 {
		sb.WriteString("## 📋 Outstanding Tasks\n")
		for _, t := range sm.PendingTasks {
			sb.WriteString(fmt.Sprintf("- [ ] %s\n", t))
		}
		sb.WriteString("\n")
	}

	if len(sm.ArtifactIDs) > 0 {
		sb.WriteString("## 📦 Linked Artifacts\n")
		for _, a := range sm.ArtifactIDs {
			sb.WriteString(fmt.Sprintf("- `%s`\n", a))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// FileDrift reports discrepancies between a session snapshot and the current filesystem.
type FileDrift struct {
	Path         string
	ExpectedHash string
	ActualHash   string
	Status       string // "modified", "missing", "fresh"
}

// ValidateFreshness compares snapshot hashes with current workspace files on disk.
// Supports full 64-char SHA-256 hashes, legacy 8-char prefixes, and expected deletions.
func (sm *SessionManifest) ValidateFreshness(workspaceRoot string) []FileDrift {
	var drifts []FileDrift
	for _, f := range sm.ChangedFiles {
		fullPath := filepath.Join(workspaceRoot, f.Path)

		fullHash, err := StreamSHA256(fullPath)
		if err != nil {
			// Expected deletion: if marked deleted, not existing is expected (fresh)
			if f.Status == "deleted" || f.WorktreeStatus == "D" {
				drifts = append(drifts, FileDrift{
					Path:         f.Path,
					ExpectedHash: f.Hash,
					Status:       "fresh",
				})
				continue
			}
			drifts = append(drifts, FileDrift{
				Path:         f.Path,
				ExpectedHash: f.Hash,
				Status:       "missing",
			})
			continue
		}

		// File exists on disk, but was marked deleted
		if f.Status == "deleted" || f.WorktreeStatus == "D" {
			drifts = append(drifts, FileDrift{
				Path:         f.Path,
				ExpectedHash: f.Hash,
				ActualHash:   truncateHash(fullHash, len(f.Hash)),
				Status:       "modified",
			})
			continue
		}

		expectedHash := f.WorktreeHash
		if expectedHash == "" {
			expectedHash = f.Hash
		}

		if hashesMatch(expectedHash, fullHash) {
			drifts = append(drifts, FileDrift{
				Path:         f.Path,
				ExpectedHash: expectedHash,
				ActualHash:   truncateHash(fullHash, len(expectedHash)),
				Status:       "fresh",
			})
		} else {
			drifts = append(drifts, FileDrift{
				Path:         f.Path,
				ExpectedHash: expectedHash,
				ActualHash:   truncateHash(fullHash, len(expectedHash)),
				Status:       "modified",
			})
		}
	}
	return drifts
}

// hashesMatch compares an expected hash against a full 64-char SHA-256.
// If expected is 8 chars (legacy), compare against the first 8 chars of full.
// Otherwise, compare full strings.
func hashesMatch(expected, fullHash string) bool {
	if len(expected) <= 8 && len(fullHash) >= len(expected) {
		return fullHash[:len(expected)] == expected
	}
	return expected == fullHash
}

// truncateHash returns the hash truncated to the given length for display consistency.
func truncateHash(hash string, targetLen int) string {
	if targetLen > 0 && targetLen < len(hash) {
		return hash[:targetLen]
	}
	return hash
}

// ImportSession loads a session manifest, strictly verifying workspace isolation, path boundaries, and schema version.
func ImportSession(manifestData, targetWorkspace string) (*SessionManifest, error) {
	sm, err := FromJSON(manifestData)
	if err != nil {
		return nil, err
	}

	if sm.Workspace != "" && targetWorkspace != "" && sm.Workspace != targetWorkspace {
		return nil, fmt.Errorf("cross-workspace import rejected: manifest is for %q, current workspace is %q", sm.Workspace, targetWorkspace)
	}

	// Validate path boundaries: reject paths escaping workspace
	for _, f := range sm.ChangedFiles {
		if filepath.IsAbs(f.Path) {
			rel, err := filepath.Rel(targetWorkspace, f.Path)
			if err != nil || strings.HasPrefix(rel, "..") {
				return nil, fmt.Errorf("path escape rejected: file %q is outside workspace %q", f.Path, targetWorkspace)
			}
		} else {
			clean := filepath.Clean(f.Path)
			if strings.HasPrefix(clean, "..") || clean == ".." {
				return nil, fmt.Errorf("path escape rejected: file %q is outside workspace", f.Path)
			}
		}
	}

	return sm, nil
}

// FreshnessStatus represents the honest evidence freshness state.
type FreshnessStatus string

const (
	FreshnessFresh   FreshnessStatus = "fresh"
	FreshnessStale   FreshnessStatus = "stale"
	FreshnessUnknown FreshnessStatus = "unknown"
)

// CheckFreshnessReport provides freshness status for a single check execution.
type CheckFreshnessReport struct {
	Check        CheckExecution  `json:"check"`
	Fresh        bool            `json:"fresh"`
	Status       FreshnessStatus `json:"status"` // "fresh", "stale", "unknown"
	DriftedFiles []string        `json:"drifted_files,omitempty"`
}

// ValidateCheckFreshness evaluates freshness of changed files and individual check executions.
// When execution-time scope hashes are present, checks against those exact hashes.
// Without execution-time hashes, marks the check unknown (or uses legacy file drift).
func (sm *SessionManifest) ValidateCheckFreshness(workspaceRoot string) ([]FileDrift, []CheckFreshnessReport) {
	fileDrifts := sm.ValidateFreshness(workspaceRoot)
	driftMap := make(map[string]FileDrift)
	for _, fd := range fileDrifts {
		driftMap[fd.Path] = fd
	}

	var reports []CheckFreshnessReport
	for _, check := range sm.Checks {
		report := CheckFreshnessReport{
			Check: check,
			Fresh: true,
		}

		if len(check.ScopeHashes) > 0 {
			// v3: Strict execution-time scope hash validation
			isFresh := true
			for sf, expectedHash := range check.ScopeHashes {
				fullPath := filepath.Join(workspaceRoot, sf)
				currHash, err := StreamSHA256(fullPath)
				if err != nil || !hashesMatch(expectedHash, currHash) {
					isFresh = false
					report.DriftedFiles = append(report.DriftedFiles, sf)
				}
			}
			if isFresh {
				report.Status = FreshnessFresh
				report.Fresh = true
			} else {
				report.Status = FreshnessStale
				report.Fresh = false
			}
		} else if len(check.ScopeFiles) == 0 {
			// Missing execution-time scope: status is unknown
			report.Status = FreshnessUnknown
			var anyDrifted []string
			for _, fd := range fileDrifts {
				if fd.Status != "fresh" {
					anyDrifted = append(anyDrifted, fd.Path)
				}
			}
			if len(anyDrifted) > 0 {
				report.Fresh = false
				report.DriftedFiles = anyDrifted
			}
		} else {
			// Legacy v2 record with ScopeFiles but without execution-time ScopeHashes
			// Freshness status in v3 is unknown (since execution-time hash wasn't captured),
			// but Fresh bool preserves v2 compatibility.
			report.Status = FreshnessUnknown
			for _, sf := range check.ScopeFiles {
				if d, ok := driftMap[sf]; ok {
					if d.Status != "fresh" {
						report.Fresh = false
						report.DriftedFiles = append(report.DriftedFiles, sf)
					}
				} else {
					fullPath := filepath.Join(workspaceRoot, sf)
					if _, err := StreamSHA256(fullPath); err != nil {
						report.Fresh = false
						report.DriftedFiles = append(report.DriftedFiles, sf)
					}
				}
			}
		}

		reports = append(reports, report)
	}

	return fileDrifts, reports
}

// SymbolDrift represents the status of an active symbol definition on resume.
type SymbolDrift struct {
	Name         string `json:"name"`
	OriginalPath string `json:"original_path"`
	OriginalLine int    `json:"original_line"`
	CurrentLine  int    `json:"current_line"`
	Status       string `json:"status"` // "intact", "moved", "missing"
	Precision    string `json:"precision"`
}

// ReResolveSymbols checks whether active symbols are still intact, moved, or missing on disk.
func (sm *SessionManifest) ReResolveSymbols(workspaceRoot string) []SymbolDrift {
	var drifts []SymbolDrift
	for _, sym := range sm.ActiveSymbols {
		d := SymbolDrift{
			Name:         sym.Name,
			OriginalPath: sym.FilePath,
			OriginalLine: sym.Line,
			CurrentLine:  sym.Line,
			Precision:    sym.Precision,
		}
		if d.Precision == "" {
			d.Precision = "precise"
		}

		fullPath := filepath.Join(workspaceRoot, sym.FilePath)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			d.Status = "missing"
			drifts = append(drifts, d)
			continue
		}

		lines := strings.Split(string(data), "\n")
		// Check original line first (1-indexed)
		if sym.Line >= 1 && sym.Line <= len(lines) && strings.Contains(lines[sym.Line-1], sym.Name) {
			d.Status = "intact"
			d.CurrentLine = sym.Line
			drifts = append(drifts, d)
			continue
		}

		// Search in file
		foundLine := -1
		for i, line := range lines {
			if strings.Contains(line, sym.Name) {
				foundLine = i + 1
				break
			}
		}

		if foundLine != -1 {
			d.Status = "moved"
			d.CurrentLine = foundLine
		} else {
			d.Status = "missing"
		}
		drifts = append(drifts, d)
	}
	return drifts
}

// GenerateSessionID creates a collision-resistant session ID using nano timestamp and random bytes.
func GenerateSessionID() string {
	nano := time.Now().UTC().UnixNano()
	b := make([]byte, 4)
	if _, err := cryptoRand.Read(b); err != nil {
		return fmt.Sprintf("sess_%d_%08x", nano, time.Now().Nanosecond())
	}
	return fmt.Sprintf("sess_%d_%x", nano, b)
}

// CheckMissingArtifacts identifies any referenced artifact IDs that are absent from the store.
func (sm *SessionManifest) CheckMissingArtifacts(s *store.Store) []string {
	if s == nil {
		return nil
	}
	var missing []string
	for _, id := range sm.ArtifactIDs {
		if _, err := s.GetArtifact(id, sm.Workspace); err != nil {
			missing = append(missing, id)
		}
	}
	return missing
}

// ResolveSession resolves a session manifest following the task selection hierarchy:
// 1. Explicit override (if explicitID != "")
// 2. Branch-keyed lookup (current git branch)
// 3. Most recent for workspace root
// 4. Clean start (nil, nil)
func ResolveSession(s *store.Store, workspace, branch, explicitID string) (*SessionManifest, error) {
	if s == nil {
		return nil, nil
	}

	if explicitID != "" {
		data, err := s.GetSession(explicitID, workspace)
		if err != nil {
			return nil, err
		}
		return FromJSON(data)
	}

	if branch != "" {
		data, err := s.GetLatestSessionByBranch(workspace, branch)
		if err == nil && data != "" {
			return FromJSON(data)
		}
	}

	if workspace != "" {
		data, err := s.GetLatestSession(workspace)
		if err == nil && data != "" {
			return FromJSON(data)
		}
	}

	return nil, nil
}
