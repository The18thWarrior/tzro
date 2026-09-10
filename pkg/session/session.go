package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"tzro/pkg/store"
)

// FileSnapshot tracks a modified file path and its cryptographic hash.
type FileSnapshot struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// CheckExecution records an executed test, build, or verify command.
type CheckExecution struct {
	Command    string    `json:"command"`
	ExitCode   int       `json:"exit_code"`
	Timestamp  time.Time `json:"timestamp"`
	OutputID   string    `json:"output_id,omitempty"`   // Artifact ID of full output
	ScopeFiles []string  `json:"scope_files,omitempty"` // Per-check files covered
}

// SessionManifest represents a complete, portable agent session state.
type SessionManifest struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	Workspace     string           `json:"workspace"`
	Branch        string           `json:"branch"`
	CreatedAt     time.Time        `json:"created_at"`
	Objective     string           `json:"objective"`
	Constraints   []string         `json:"constraints"`
	Decisions     []string         `json:"decisions"`
	ChangedFiles  []FileSnapshot   `json:"changed_files"`
	Checks        []CheckExecution `json:"checks"`
	PendingTasks  []string         `json:"pending_tasks"`
	ArtifactIDs   []string         `json:"artifact_ids"`
}

// NewSessionManifest creates a new initialized SessionManifest with SchemaVersion 2.
func NewSessionManifest(id, workspace, branch, objective string) *SessionManifest {
	return &SessionManifest{
		SchemaVersion: 2,
		ID:            id,
		Workspace:     workspace,
		Branch:        branch,
		CreatedAt:     time.Now().UTC(),
		Objective:     objective,
		Constraints:   []string{},
		Decisions:     []string{},
		ChangedFiles:  []FileSnapshot{},
		Checks:        []CheckExecution{},
		PendingTasks:  []string{},
		ArtifactIDs:   []string{},
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
	if sm.SchemaVersion < 1 || sm.SchemaVersion > 2 {
		return nil, fmt.Errorf("incompatible schema version %d (expected 1 or 2)", sm.SchemaVersion)
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
// Supports both full 64-char SHA-256 hashes and legacy 8-char prefix hashes.
func (sm *SessionManifest) ValidateFreshness(workspaceRoot string) []FileDrift {
	var drifts []FileDrift
	for _, f := range sm.ChangedFiles {
		fullPath := filepath.Join(workspaceRoot, f.Path)

		fullHash, err := StreamSHA256(fullPath)
		if err != nil {
			drifts = append(drifts, FileDrift{
				Path:         f.Path,
				ExpectedHash: f.Hash,
				Status:       "missing",
			})
			continue
		}

		if hashesMatch(f.Hash, fullHash) {
			drifts = append(drifts, FileDrift{
				Path:         f.Path,
				ExpectedHash: f.Hash,
				ActualHash:   truncateHash(fullHash, len(f.Hash)),
				Status:       "fresh",
			})
		} else {
			drifts = append(drifts, FileDrift{
				Path:         f.Path,
				ExpectedHash: f.Hash,
				ActualHash:   truncateHash(fullHash, len(f.Hash)),
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

// ImportSession loads a session manifest, strictly verifying workspace isolation and schema version.
func ImportSession(manifestData, targetWorkspace string) (*SessionManifest, error) {
	sm, err := FromJSON(manifestData)
	if err != nil {
		return nil, err
	}

	if sm.Workspace != "" && targetWorkspace != "" && sm.Workspace != targetWorkspace {
		return nil, fmt.Errorf("cross-workspace import rejected: manifest is for %q, current workspace is %q", sm.Workspace, targetWorkspace)
	}

	return sm, nil
}

// CheckFreshnessReport provides freshness status for a single check execution.
type CheckFreshnessReport struct {
	Check        CheckExecution `json:"check"`
	Fresh        bool           `json:"fresh"`
	DriftedFiles []string       `json:"drifted_files,omitempty"`
}

// ValidateCheckFreshness evaluates freshness of changed files and individual check executions.
// A check is fresh if and only if every file in its ScopeFiles matches its snapshot hash.
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

		if len(check.ScopeFiles) == 0 {
			// Conservative fallback for v1 or unscoped checks: stale if any changed file drifted
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
			for _, sf := range check.ScopeFiles {
				if d, ok := driftMap[sf]; ok {
					if d.Status != "fresh" {
						report.Fresh = false
						report.DriftedFiles = append(report.DriftedFiles, sf)
					}
				} else {
					// Check file directly if not in ChangedFiles list
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
