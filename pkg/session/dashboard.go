package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"tzro/pkg/store"
)

// SanitizeTerminalText removes terminal control characters and escape codes from text.
func SanitizeTerminalText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' {
			b.WriteRune(r)
		} else if r == '\t' {
			b.WriteString("  ")
		} else if r < 32 || r == 127 || (r >= 0x80 && r <= 0x9F) {
			continue
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DashboardFileItem represents a single file entry in the dashboard.
type DashboardFileItem struct {
	Path         string `json:"path"`
	Status       string `json:"status"` // "MODIFIED", "STAGED", "UNSTAGED", "STAGED+UNSTAGED", "UNTRACKED", "DELETED", "RENAMED"
	Drift        string `json:"drift"`  // "fresh", "modified", "missing", "expected_deleted"
	OldPath      string `json:"old_path,omitempty"`
	StagedHash   string `json:"staged_hash,omitempty"`
	WorktreeHash string `json:"worktree_hash,omitempty"`
}

// DashboardCheckItem represents a single check execution in the dashboard.
type DashboardCheckItem struct {
	Command       string          `json:"command"`
	ExitCode      int             `json:"exit_code"`
	Outcome       string          `json:"outcome"` // "PASS", "FAIL (code)"
	Freshness     FreshnessStatus `json:"freshness"`
	DriftedFiles  []string        `json:"drifted_files,omitempty"`
	FailureDetail string          `json:"failure_detail,omitempty"`
	OutputID      string          `json:"output_id,omitempty"`
}

// DashboardSymbolItem represents an active symbol in the dashboard.
type DashboardSymbolItem struct {
	Name         string `json:"name"`
	FilePath     string `json:"file_path"`
	OriginalLine int    `json:"original_line"`
	CurrentLine  int    `json:"current_line"`
	Status       string `json:"status"` // "intact", "moved", "missing"
	Detail       string `json:"detail"`
}

// DashboardCommandItem represents a recently captured command in the dashboard.
type DashboardCommandItem struct {
	ID          string `json:"id"`
	DisplayText string `json:"display_text"`
	ExitStatus  string `json:"exit_status"`
	Timestamp   string `json:"timestamp"`
}

// DashboardViewModel contains all the prepared data for dashboard presentation.
type DashboardViewModel struct {
	SessionID              string                 `json:"session_id"`
	Workspace              string                 `json:"workspace"`
	Branch                 string                 `json:"branch"`
	CurrentBranch          string                 `json:"current_branch"`
	BranchMismatch         bool                   `json:"branch_mismatch"`
	BranchDivergence       string                 `json:"branch_divergence"`
	IsDetached             bool                   `json:"is_detached"`
	HeadCommit             string                 `json:"head_commit,omitempty"`
	PauseDurationText      string                 `json:"pause_duration_text"`
	Objective              string                 `json:"objective"`
	PendingTasks           []string               `json:"pending_tasks"`
	Decisions              []string               `json:"decisions"`
	Constraints            []string               `json:"constraints"`
	ModifiedFiles          []DashboardFileItem    `json:"modified_files"`
	ModifiedFilesOmitted   int                    `json:"modified_files_omitted"`
	Checks                 []DashboardCheckItem   `json:"checks"`
	ActiveSymbols          []DashboardSymbolItem  `json:"active_symbols"`
	ActiveSymbolsOmitted   int                    `json:"active_symbols_omitted"`
	RecentCommands         []DashboardCommandItem `json:"recent_commands"`
	MissingArtifacts       []string               `json:"missing_artifacts"`
	ContextHydrationStatus string                 `json:"context_hydration_status"`
	HydrationError         string                 `json:"hydration_error,omitempty"`
}

const (
	maxDisplayFiles   = 10
	maxDisplaySymbols = 10
	maxDisplayCmds    = 5
)

// BuildDashboardViewModel transforms a ResumeReport into a presentation-ready DashboardViewModel.
func BuildDashboardViewModel(
	report *ResumeReport,
	workspaceRoot string,
	s *store.Store,
	clock func() time.Time,
) *DashboardViewModel {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}

	sm := report.Session
	vm := &DashboardViewModel{
		SessionID:        SanitizeTerminalText(sm.ID),
		Workspace:        SanitizeTerminalText(sm.Workspace),
		Branch:           SanitizeTerminalText(sm.Branch),
		CurrentBranch:    SanitizeTerminalText(report.CurrentBranch),
		BranchMismatch:   report.BranchMismatch,
		IsDetached:       report.IsDetached,
		HeadCommit:       SanitizeTerminalText(sm.HeadCommit),
		Objective:        SanitizeTerminalText(sm.Objective),
		PendingTasks:     sanitizeSlice(sm.PendingTasks),
		Decisions:        sanitizeSlice(sm.Decisions),
		Constraints:      sanitizeSlice(sm.Constraints),
		MissingArtifacts: sanitizeSlice(report.MissingArtifacts),
		HydrationError:   SanitizeTerminalText(report.HydrationError),
	}

	// 1. Pause duration calculation
	if sm.PausedAt != nil {
		now := clock()
		if now.Before(*sm.PausedAt) {
			vm.PauseDurationText = "unknown (clock skew)"
		} else {
			vm.PauseDurationText = formatDuration(now.Sub(*sm.PausedAt))
		}
	} else {
		vm.PauseDurationText = "none recorded"
	}

	// 2. Branch divergence detection using local git evidence
	vm.BranchDivergence = senseBranchDivergence(workspaceRoot)

	// 3. Modified files view mapping
	fileDriftMap := make(map[string]string)
	for _, fd := range report.FileDrifts {
		fileDriftMap[fd.Path] = fd.Status
	}

	var allFiles []DashboardFileItem
	for _, cf := range sm.ChangedFiles {
		item := DashboardFileItem{
			Path:         SanitizeTerminalText(cf.Path),
			OldPath:      SanitizeTerminalText(cf.OldPath),
			StagedHash:   cf.StagedHash,
			WorktreeHash: cf.WorktreeHash,
		}

		// Change status calculation
		stagedActive := cf.StagedStatus != "" && cf.StagedStatus != " " && cf.StagedStatus != "?"
		worktreeActive := cf.WorktreeStatus != "" && cf.WorktreeStatus != " " && cf.WorktreeStatus != "?"

		if cf.Status == "renamed" {
			item.Status = "RENAMED"
		} else if cf.Status == "untracked" || cf.StagedStatus == "?" {
			item.Status = "UNTRACKED"
		} else if cf.Status == "deleted" || cf.WorktreeStatus == "D" || cf.StagedStatus == "D" {
			item.Status = "DELETED"
		} else if stagedActive && worktreeActive {
			item.Status = "STAGED+UNSTAGED"
		} else if stagedActive {
			item.Status = "STAGED"
		} else {
			item.Status = "UNSTAGED"
		}

		if d, ok := fileDriftMap[cf.Path]; ok {
			item.Drift = d
		} else {
			item.Drift = "fresh"
		}

		allFiles = append(allFiles, item)
	}

	// Stable sort by Path
	sort.Slice(allFiles, func(i, j int) bool {
		return allFiles[i].Path < allFiles[j].Path
	})

	if len(allFiles) > maxDisplayFiles {
		vm.ModifiedFiles = allFiles[:maxDisplayFiles]
		vm.ModifiedFilesOmitted = len(allFiles) - maxDisplayFiles
	} else {
		vm.ModifiedFiles = allFiles
	}

	// 4. Executed checks
	for _, ce := range report.CheckEvidences {
		outcome := "PASS"
		if ce.ExitCode != 0 {
			outcome = fmt.Sprintf("FAIL (%d)", ce.ExitCode)
		}

		item := DashboardCheckItem{
			Command:      SanitizeTerminalText(ce.Command),
			ExitCode:     ce.ExitCode,
			Outcome:      outcome,
			Freshness:    ce.Status,
			DriftedFiles: sanitizeSlice(ce.DriftedFiles),
			OutputID:     ce.OutputID,
		}

		// Extract failure detail if available from stored artifact
		if ce.ExitCode != 0 && ce.OutputID != "" && s != nil {
			if art, err := s.GetArtifact(ce.OutputID, sm.Workspace); err == nil && art != nil {
				item.FailureDetail = extractFailureSnippet(art.Body)
			}
		}

		vm.Checks = append(vm.Checks, item)
	}

	// 5. Active symbols mapping
	var allSymbols []DashboardSymbolItem
	for _, sd := range report.SymbolDrifts {
		detail := fmt.Sprintf("line %d", sd.CurrentLine)
		if sd.Status == "moved" {
			detail = fmt.Sprintf("moved from line %d -> %d", sd.OriginalLine, sd.CurrentLine)
		} else if sd.Status == "missing" {
			detail = "missing from source"
		}

		allSymbols = append(allSymbols, DashboardSymbolItem{
			Name:         SanitizeTerminalText(sd.Name),
			FilePath:     SanitizeTerminalText(sd.OriginalPath),
			OriginalLine: sd.OriginalLine,
			CurrentLine:  sd.CurrentLine,
			Status:       sd.Status,
			Detail:       detail,
		})
	}

	// Stable sort by FilePath then Line
	sort.Slice(allSymbols, func(i, j int) bool {
		if allSymbols[i].FilePath == allSymbols[j].FilePath {
			return allSymbols[i].CurrentLine < allSymbols[j].CurrentLine
		}
		return allSymbols[i].FilePath < allSymbols[j].FilePath
	})

	if len(allSymbols) > maxDisplaySymbols {
		vm.ActiveSymbols = allSymbols[:maxDisplaySymbols]
		vm.ActiveSymbolsOmitted = len(allSymbols) - maxDisplaySymbols
	} else {
		vm.ActiveSymbols = allSymbols
	}

	// 6. Recent commands mapping
	for _, cmd := range sm.RecentCommands {
		exitText := "unknown"
		if cmd.ExitStatus != nil {
			if *cmd.ExitStatus == 0 {
				exitText = "0"
			} else {
				exitText = strconv.Itoa(*cmd.ExitStatus)
			}
		}

		vm.RecentCommands = append(vm.RecentCommands, DashboardCommandItem{
			ID:          cmd.ID,
			DisplayText: SanitizeTerminalText(cmd.DisplayText),
			ExitStatus:  exitText,
			Timestamp:   cmd.StartedAt.Format("15:04:05"),
		})
	}

	if len(vm.RecentCommands) > maxDisplayCmds {
		vm.RecentCommands = vm.RecentCommands[len(vm.RecentCommands)-maxDisplayCmds:]
	}

	// 7. Context hydration status
	if report.HydratedContextSummary != "" {
		vm.ContextHydrationStatus = SanitizeTerminalText(report.HydratedContextSummary)
	} else if report.HydrationError != "" {
		vm.ContextHydrationStatus = fmt.Sprintf("unavailable (%s)", SanitizeTerminalText(report.HydrationError))
	} else {
		vm.ContextHydrationStatus = "none"
	}

	return vm
}

// senseBranchDivergence computes local ahead/behind divergence against configured upstream.
func senseBranchDivergence(workspaceRoot string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	upstream, err := runGit(ctx, workspaceRoot, "rev-parse", "--abbrev-ref", "@{u}")
	if err != nil || upstream == "" {
		return "no upstream configured"
	}

	counts, err := runGit(ctx, workspaceRoot, "rev-list", "--left-right", "--count", "HEAD...@{u}")
	if err != nil {
		return fmt.Sprintf("upstream %s (divergence unknown)", upstream)
	}

	parts := strings.Fields(counts)
	if len(parts) >= 2 {
		ahead, _ := strconv.Atoi(parts[0])
		behind, _ := strconv.Atoi(parts[1])
		return fmt.Sprintf("ahead %d, behind %d (%s)", ahead, behind, upstream)
	}

	return fmt.Sprintf("upstream %s", upstream)
}

func extractFailureSnippet(body string) string {
	lines := strings.Split(SanitizeTerminalText(body), "\n")
	var nonBlank []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			nonBlank = append(nonBlank, trimmed)
		}
	}
	if len(nonBlank) == 0 {
		return ""
	}
	// Return up to first 5 lines
	limit := 5
	if len(nonBlank) < limit {
		limit = len(nonBlank)
	}
	return strings.Join(nonBlank[:limit], "\n")
}

func sanitizeSlice(slice []string) []string {
	if slice == nil {
		return nil
	}
	var res []string
	for _, s := range slice {
		res = append(res, SanitizeTerminalText(s))
	}
	return res
}

// RenderDashboard renders the view model in "tty", "plain", or "json" format.
func RenderDashboard(vm *DashboardViewModel, format string, termWidth int, noColor bool) string {
	if termWidth <= 0 {
		termWidth = 80
	}

	if format == "json" {
		data, err := json.MarshalIndent(vm, "", "  ")
		if err != nil {
			return "{}"
		}
		return string(data)
	}

	isTTY := format == "tty" && !noColor

	if noColor || !isTTY {
		lipgloss.SetColorProfile(termenv.Ascii)
	} else {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}

	// Styles
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7D56F4"))
	sectionStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#00D7D7"))
	warnStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF5F87"))
	goodStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#04B575"))
	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#808080"))

	var sb strings.Builder

	// Top Banner
	bannerTitle := fmt.Sprintf("TZRO RESUMPTION DASHBOARD: %s", vm.SessionID)
	sb.WriteString(headerStyle.Render(bannerTitle))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render(strings.Repeat("─", min(termWidth, 78))))
	sb.WriteString("\n\n")

	// Session Identity
	sb.WriteString(sectionStyle.Render("📍 Task Identity & Git State"))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  Workspace:    %s\n", vm.Workspace))
	branchLine := fmt.Sprintf("  Branch:       %s", vm.Branch)
	if vm.BranchMismatch {
		branchLine += fmt.Sprintf(" %s (Current branch: %s)", warnStyle.Render("[BRANCH MISMATCH]"), vm.CurrentBranch)
	}
	sb.WriteString(branchLine + "\n")
	sb.WriteString(fmt.Sprintf("  Divergence:   %s\n", vm.BranchDivergence))
	if vm.HeadCommit != "" {
		sb.WriteString(fmt.Sprintf("  Saved Commit: %s\n", vm.HeadCommit))
	}
	sb.WriteString(fmt.Sprintf("  Paused:       %s ago\n", vm.PauseDurationText))
	sb.WriteString("\n")

	// Objective
	sb.WriteString(sectionStyle.Render("🎯 Objective & Tasks"))
	sb.WriteString("\n")
	if vm.Objective != "" {
		sb.WriteString(fmt.Sprintf("  Objective: %s\n", vm.Objective))
	} else {
		sb.WriteString("  Objective: (none recorded)\n")
	}

	if len(vm.PendingTasks) > 0 {
		sb.WriteString("  Pending Tasks:\n")
		for _, task := range vm.PendingTasks {
			sb.WriteString(fmt.Sprintf("    [ ] %s\n", task))
		}
	} else {
		sb.WriteString("  Pending Tasks: (none recorded)\n")
	}
	sb.WriteString("\n")

	// Decisions and Constraints
	if len(vm.Decisions) > 0 || len(vm.Constraints) > 0 {
		sb.WriteString(sectionStyle.Render("💡 Approved Intent"))
		sb.WriteString("\n")
		for _, d := range vm.Decisions {
			sb.WriteString(fmt.Sprintf("  • Decision:   %s\n", d))
		}
		for _, c := range vm.Constraints {
			sb.WriteString(fmt.Sprintf("  • Constraint: %s\n", c))
		}
		sb.WriteString("\n")
	}

	// Modified Files & Drift
	sb.WriteString(sectionStyle.Render("📝 Modified Files & Drift"))
	sb.WriteString("\n")
	if len(vm.ModifiedFiles) > 0 {
		for _, f := range vm.ModifiedFiles {
			statusTag := fmt.Sprintf("[%s]", f.Status)
			driftTag := fmt.Sprintf("[%s]", strings.ToUpper(f.Drift))
			if f.Drift == "fresh" {
				driftTag = goodStyle.Render(driftTag)
			} else if f.Drift == "modified" || f.Drift == "missing" {
				driftTag = warnStyle.Render(driftTag)
			}

			pathStr := f.Path
			if f.OldPath != "" {
				pathStr = fmt.Sprintf("%s -> %s", f.OldPath, f.Path)
			}

			sb.WriteString(fmt.Sprintf("  %s %s %s\n", statusTag, driftTag, pathStr))
		}
		if vm.ModifiedFilesOmitted > 0 {
			sb.WriteString(dimStyle.Render(fmt.Sprintf("  ... and %d more modified files omitted\n", vm.ModifiedFilesOmitted)))
		}
	} else {
		sb.WriteString("  (no modified files recorded)\n")
	}
	sb.WriteString("\n")

	// Verified Evidence (Executed Checks)
	sb.WriteString(sectionStyle.Render("✅ Executed Checks Freshness"))
	sb.WriteString("\n")
	if len(vm.Checks) > 0 {
		for _, ch := range vm.Checks {
			resLabel := fmt.Sprintf("[%s]", ch.Outcome)
			if strings.HasPrefix(ch.Outcome, "PASS") {
				resLabel = goodStyle.Render(resLabel)
			} else {
				resLabel = warnStyle.Render(resLabel)
			}

			freshLabel := fmt.Sprintf("[%s]", strings.ToUpper(string(ch.Freshness)))
			if ch.Freshness == FreshnessFresh {
				freshLabel = goodStyle.Render(freshLabel)
			} else if ch.Freshness == FreshnessStale {
				freshLabel = warnStyle.Render(freshLabel)
			} else {
				freshLabel = dimStyle.Render(freshLabel)
			}

			driftInfo := ""
			if len(ch.DriftedFiles) > 0 {
				driftInfo = fmt.Sprintf(" (drifted: %s)", strings.Join(ch.DriftedFiles, ", "))
			}

			artRef := ""
			if ch.OutputID != "" {
				artRef = fmt.Sprintf(" [Artifact: %s]", ch.OutputID)
			}

			sb.WriteString(fmt.Sprintf("  %s %s %s%s%s\n", resLabel, freshLabel, ch.Command, driftInfo, artRef))

			if ch.FailureDetail != "" {
				sb.WriteString(dimStyle.Render("    Failure detail:\n"))
				for _, line := range strings.Split(ch.FailureDetail, "\n") {
					sb.WriteString(fmt.Sprintf("      %s\n", line))
				}
			}
		}
	} else {
		sb.WriteString("  (no checks recorded)\n")
	}
	sb.WriteString("\n")

	// Active Symbols
	sb.WriteString(sectionStyle.Render("🔍 Active Symbols"))
	sb.WriteString("\n")
	if len(vm.ActiveSymbols) > 0 {
		for _, sym := range vm.ActiveSymbols {
			statusTag := fmt.Sprintf("[%s]", strings.ToUpper(sym.Status))
			if sym.Status == "intact" {
				statusTag = goodStyle.Render(statusTag)
			} else if sym.Status == "moved" {
				statusTag = warnStyle.Render(statusTag)
			} else {
				statusTag = dimStyle.Render(statusTag)
			}

			sb.WriteString(fmt.Sprintf("  %s %s (%s: %s)\n", statusTag, sym.Name, sym.FilePath, sym.Detail))
		}
		if vm.ActiveSymbolsOmitted > 0 {
			sb.WriteString(dimStyle.Render(fmt.Sprintf("  ... and %d more active symbols omitted\n", vm.ActiveSymbolsOmitted)))
		}
	} else {
		sb.WriteString("  (no active symbols recorded)\n")
	}
	sb.WriteString("\n")

	// Recent Commands (History)
	sb.WriteString(sectionStyle.Render("⌨️ Recent Commands"))
	sb.WriteString("\n")
	if len(vm.RecentCommands) > 0 {
		for _, cmd := range vm.RecentCommands {
			sb.WriteString(fmt.Sprintf("  [%s] (exit %s) %s\n", cmd.Timestamp, cmd.ExitStatus, cmd.DisplayText))
		}
	} else {
		sb.WriteString("  (no command capture recorded)\n")
	}
	sb.WriteString("\n")

	// Missing Artifacts
	if len(vm.MissingArtifacts) > 0 {
		sb.WriteString(warnStyle.Render("⚠️ Missing or Expired Artifacts"))
		sb.WriteString("\n")
		for _, art := range vm.MissingArtifacts {
			sb.WriteString(fmt.Sprintf("  • Artifact %s absent from store\n", art))
		}
		sb.WriteString("\n")
	}

	// Context Hydration
	sb.WriteString(sectionStyle.Render("🧠 Bounded Context Hydration"))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  Status: %s\n", vm.ContextHydrationStatus))

	return sb.String()
}
