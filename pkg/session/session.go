package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)


// FileSnapshot tracks a modified file path and its cryptographic hash.
type FileSnapshot struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// CheckExecution records an executed test, build, or verify command.
type CheckExecution struct {
	Command   string    `json:"command"`
	ExitCode  int       `json:"exit_code"`
	Timestamp time.Time `json:"timestamp"`
	OutputID  string    `json:"output_id,omitempty"` // Artifact ID of full output
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

// NewSessionManifest creates a new initialized SessionManifest with SchemaVersion 1.
func NewSessionManifest(id, workspace, branch, objective string) *SessionManifest {
	return &SessionManifest{
		SchemaVersion: 1,
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
	if sm.SchemaVersion != 1 {
		return nil, fmt.Errorf("incompatible schema version %d (expected 1)", sm.SchemaVersion)
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

