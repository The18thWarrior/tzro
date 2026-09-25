package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tzro/pkg/store"
)

// ResumeCheckEvidence summarizes a single check execution with honest freshness status.
type ResumeCheckEvidence struct {
	Command      string          `json:"command"`
	ExitCode     int             `json:"exit_code"`
	Status       FreshnessStatus `json:"status"` // "fresh", "stale", "unknown"
	DriftedFiles []string        `json:"drifted_files,omitempty"`
	OutputID     string          `json:"output_id,omitempty"`
}

// ResumeReport captures the complete evidence and drift status for a resumed session.
type ResumeReport struct {
	Session                *SessionManifest      `json:"session"`
	BranchMismatch         bool                  `json:"branch_mismatch"`
	ExpectedBranch         string                `json:"expected_branch"`
	CurrentBranch          string                `json:"current_branch"`
	IsDetached             bool                  `json:"is_detached"`
	FileDrifts             []FileDrift           `json:"file_drifts"`
	CheckEvidences         []ResumeCheckEvidence `json:"check_evidences"`
	SymbolDrifts           []SymbolDrift         `json:"symbol_drifts"`
	MissingArtifacts       []string              `json:"missing_artifacts"`
	HydratedContextSummary string                `json:"hydrated_context_summary,omitempty"`
	HydrationError         string                `json:"hydration_error,omitempty"`
	PausedDuration         time.Duration         `json:"paused_duration"`
	PausedDurationText     string                `json:"paused_duration_text"`
}

// Hydrator defines a pluggable context hydration function.
type Hydrator func(ctx context.Context, workspaceRoot, query string, budget int) (string, error)

// GenerateResumeReport computes file drifts, check freshness, symbol drifts, and missing artifacts
// for the given session manifest without modifying any developer files on disk.
func GenerateResumeReport(
	ctx context.Context,
	sm *SessionManifest,
	workspaceRoot string,
	gitState GitState,
	s *store.Store,
	hydrator Hydrator,
) *ResumeReport {
	report := &ResumeReport{
		Session:        sm,
		ExpectedBranch: sm.Branch,
		CurrentBranch:  gitState.Branch,
		IsDetached:     gitState.IsDetached,
	}

	// 1. Branch mismatch detection
	if sm.Branch != "" && gitState.Branch != "" && sm.Branch != gitState.Branch {
		report.BranchMismatch = true
	}

	// 2. Pause duration computation with clock-skew protection
	if sm.PausedAt != nil {
		now := time.Now().UTC()
		if now.Before(*sm.PausedAt) {
			report.PausedDurationText = "unknown (clock skew)"
		} else {
			report.PausedDuration = now.Sub(*sm.PausedAt)
			report.PausedDurationText = formatDuration(report.PausedDuration)
		}
	} else {
		report.PausedDurationText = "none recorded"
	}

	// 3. File drift analysis
	fileDrifts, checkReports := sm.ValidateCheckFreshness(workspaceRoot)
	report.FileDrifts = fileDrifts

	// 4. Executed check evidence analysis
	for _, cr := range checkReports {
		ce := ResumeCheckEvidence{
			Command:      cr.Check.Command,
			ExitCode:     cr.Check.ExitCode,
			Status:       cr.Status,
			DriftedFiles: cr.DriftedFiles,
			OutputID:     cr.Check.OutputID,
		}
		report.CheckEvidences = append(report.CheckEvidences, ce)
	}

	// 5. Active symbols re-resolution
	report.SymbolDrifts = sm.ReResolveSymbols(workspaceRoot)

	// 6. Missing artifact checks
	if s != nil {
		report.MissingArtifacts = sm.CheckMissingArtifacts(s)
	}

	// 7. Context hydration
	if hydrator != nil && sm.Objective != "" {
		summary, err := hydrator(ctx, workspaceRoot, sm.Objective, 2000)
		if err != nil {
			report.HydrationError = err.Error()
		} else {
			report.HydratedContextSummary = summary
		}
	}

	return report
}

// Format renders a clean, human-readable plain text / markdown representation of the resume report.
func (r *ResumeReport) Format() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("📋 Tzro Session Resume: %s\n\n", r.Session.ID))
	sb.WriteString(fmt.Sprintf("- **Workspace:** `%s`\n", r.Session.Workspace))
	sb.WriteString(fmt.Sprintf("- **Branch:** `%s`", r.Session.Branch))
	if r.BranchMismatch {
		sb.WriteString(fmt.Sprintf(" ⚠️ **Branch Mismatch** (Current branch: `%s`)", r.CurrentBranch))
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("- **Paused Duration:** %s\n", r.PausedDurationText))
	if r.Session.HeadCommit != "" {
		sb.WriteString(fmt.Sprintf("- **Saved Commit:** `%s`\n", r.Session.HeadCommit))
	}
	sb.WriteString("\n")

	sb.WriteString("## 🎯 Objective\n")
	if r.Session.Objective != "" {
		sb.WriteString(fmt.Sprintf("%s\n\n", r.Session.Objective))
	} else {
		sb.WriteString("*(no objective recorded)*\n\n")
	}

	if len(r.Session.Decisions) > 0 {
		sb.WriteString("## 💡 Architectural Decisions\n")
		for _, d := range r.Session.Decisions {
			sb.WriteString(fmt.Sprintf("- %s\n", d))
		}
		sb.WriteString("\n")
	}

	if len(r.Session.Constraints) > 0 {
		sb.WriteString("## ⚠️ Approved Constraints\n")
		for _, c := range r.Session.Constraints {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}

	if len(r.Session.PendingTasks) > 0 {
		sb.WriteString("## 📋 Outstanding Tasks\n")
		for _, t := range r.Session.PendingTasks {
			sb.WriteString(fmt.Sprintf("- [ ] %s\n", t))
		}
		sb.WriteString("\n")
	}

	if len(r.FileDrifts) > 0 {
		sb.WriteString("## 📝 Modified Files & Drift\n")
		for _, fd := range r.FileDrifts {
			statusTag := strings.ToUpper(fd.Status)
			sb.WriteString(fmt.Sprintf("- `[%s]` `%s`\n", statusTag, fd.Path))
		}
		sb.WriteString("\n")
	}

	if len(r.CheckEvidences) > 0 {
		sb.WriteString("## ✅ Verified Evidence (Checks Freshness)\n")
		for _, ce := range r.CheckEvidences {
			res := "PASS"
			if ce.ExitCode != 0 {
				res = fmt.Sprintf("FAIL (%d)", ce.ExitCode)
			}
			freshTag := string(ce.Status)
			if len(ce.DriftedFiles) > 0 {
				freshTag = fmt.Sprintf("stale (drifted: %s)", strings.Join(ce.DriftedFiles, ", "))
			}
			artRef := ""
			if ce.OutputID != "" {
				artRef = fmt.Sprintf(" [Artifact: `%s`]", ce.OutputID)
			}
			sb.WriteString(fmt.Sprintf("- `%s` -> **%s** (Freshness: **%s**)%s\n", ce.Command, res, freshTag, artRef))
		}
		sb.WriteString("\n")
	}

	if len(r.SymbolDrifts) > 0 {
		sb.WriteString("## 🔍 Active Symbols\n")
		for _, sd := range r.SymbolDrifts {
			lineInfo := fmt.Sprintf("line %d", sd.CurrentLine)
			if sd.Status == "moved" {
				lineInfo = fmt.Sprintf("moved from line %d -> %d", sd.OriginalLine, sd.CurrentLine)
			} else if sd.Status == "missing" {
				lineInfo = "missing from source"
			}
			sb.WriteString(fmt.Sprintf("- `%s` (`%s`: %s) [%s]\n", sd.Name, sd.OriginalPath, lineInfo, sd.Status))
		}
		sb.WriteString("\n")
	}

	if len(r.MissingArtifacts) > 0 {
		sb.WriteString("## 📦 Missing Artifacts\n")
		for _, art := range r.MissingArtifacts {
			sb.WriteString(fmt.Sprintf("- ⚠️ Artifact `%s` is absent or expired from local store\n", art))
		}
		sb.WriteString("\n")
	}

	if r.HydratedContextSummary != "" {
		sb.WriteString("## 🧠 Context Hydration\n")
		sb.WriteString(fmt.Sprintf("%s\n\n", r.HydratedContextSummary))
	} else if r.HydrationError != "" {
		sb.WriteString("## 🧠 Context Hydration (Limitation)\n")
		sb.WriteString(fmt.Sprintf("⚠️ Context hydration unavailable: %s\n\n", r.HydrationError))
	}

	return sb.String()
}

// ToJSON returns formatted JSON representation of the resume report.
func (r *ResumeReport) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "< 1m"
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if hours > 24 {
		days := hours / 24
		hours = hours % 24
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
