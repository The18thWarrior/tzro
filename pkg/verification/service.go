package verification

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Service implements the grouped edit-and-verify operation.
type Service struct {
	workspace string
	deps      Dependencies
}

// NewService creates a verification service bound to the given workspace.
func NewService(workspace string, deps Dependencies) *Service {
	// Resolve symlinks for consistent path comparison (e.g., /var → /private/var on macOS).
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	return &Service{workspace: workspace, deps: deps}
}

// ApplyAndVerify preflights, applies edits, and runs verification checks.
// It always returns a Summary; it never panics on invalid input.
func (s *Service) ApplyAndVerify(ctx context.Context, req Request, opts Options) Summary {
	timeout := opts.effectiveTimeout()

	// Apply the operation deadline.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Validate request.
	if len(req.Edits) == 0 {
		return Summary{
			Schema:          "tzro.verification.v1",
			Application:     ApplicationRejected,
			Verification:    VerificationUnavailable,
			Termination:     TerminationCompleted,
			Retention:       RetentionUnavailable,
			RetentionReason: "no edits submitted",
			RejectionReason: "empty edit batch",
		}
	}

	// Validate and normalize edits.
	for i := range req.Edits {
		if err := s.validateEdit(&req.Edits[i]); err != nil {
			return Summary{
				Schema:          "tzro.verification.v1",
				Application:     ApplicationRejected,
				Verification:    VerificationUnavailable,
				Termination:     TerminationCompleted,
				Retention:       RetentionUnavailable,
				RetentionReason: "batch rejected at validation",
				RejectionReason: fmt.Sprintf("edit %d: %v", i, err),
			}
		}
	}

	// Check context before preflight.
	if err := ctx.Err(); err != nil {
		return s.deadlineOrCancelSummary(err)
	}

	// Preflight: stage all edits in memory before writing any file.
	staged, err := s.preflight(ctx, req.Edits)
	if err != nil {
		return Summary{
			Schema:          "tzro.verification.v1",
			Application:     ApplicationRejected,
			Verification:    VerificationUnavailable,
			Termination:     TerminationCompleted,
			Retention:       RetentionUnavailable,
			RetentionReason: "batch rejected at preflight",
			RejectionReason: err.Error(),
		}
	}

	// Check context before application.
	if err := ctx.Err(); err != nil {
		return s.deadlineOrCancelSummary(err)
	}

	// Apply: write staged results.
	changed, uncertain, appErr := s.apply(ctx, staged)
	if appErr != nil {
		appStatus := ApplicationPartial
		if len(changed) == 0 {
			appStatus = ApplicationNotApplied
		}
		return Summary{
			Schema:          "tzro.verification.v1",
			Application:     appStatus,
			ChangedFiles:    changed,
			UncertainFiles:  uncertain,
			Verification:    VerificationUnavailable,
			Termination:     TerminationApplicationError,
			Retention:       RetentionUnavailable,
			RetentionReason: "application error: verification not run",
			Error:           appErr.Error(),
		}
	}

	// Check context before verification.
	if err := ctx.Err(); err != nil {
		summary := s.deadlineOrCancelSummary(err)
		summary.Application = ApplicationApplied
		summary.ChangedFiles = changed
		return summary
	}

	// Load Verification Preset.
	preset, presetErr := LoadPreset(filepath.Join(s.workspace, ".tzro", "verification.yaml"))
	if presetErr != nil {
		return Summary{
			Schema:          "tzro.verification.v1",
			Application:     ApplicationApplied,
			ChangedFiles:    changed,
			Verification:    VerificationNotConfigured,
			Termination:     TerminationCompleted,
			Retention:       RetentionUnavailable,
			RetentionReason: fmt.Sprintf("preset error: %v", presetErr),
		}
	}

	if len(preset.Checks) == 0 {
		return Summary{
			Schema:          "tzro.verification.v1",
			Application:     ApplicationApplied,
			ChangedFiles:    changed,
			Verification:    VerificationNotConfigured,
			Termination:     TerminationCompleted,
			Retention:       RetentionUnavailable,
			RetentionReason: "no checks defined in preset",
		}
	}

	// Run checks sequentially.
	checks, termination := s.runChecks(ctx, preset)

	// Determine overall verification status.
	verStatus := s.overallVerification(checks, termination)

	// Retain evidence.
	retention, retentionReason := s.retainEvidence(ctx, checks)

	return Summary{
		Schema:          "tzro.verification.v1",
		Application:     ApplicationApplied,
		ChangedFiles:    changed,
		Verification:    verStatus,
		Checks:          checks,
		Termination:     termination,
		Retention:       retention,
		RetentionReason: retentionReason,
	}
}

// validateEdit checks a single edit for validity.
func (s *Service) validateEdit(e *Edit) error {
	if e.Kind != "replace" && e.Kind != "create" {
		return fmt.Errorf("unsupported kind %q", e.Kind)
	}
	if e.Path == "" {
		return fmt.Errorf("path is required")
	}
	if filepath.IsAbs(e.Path) {
		return fmt.Errorf("absolute path not allowed: %s", e.Path)
	}
	// Reject path traversal.
	cleaned := filepath.Clean(e.Path)
	if strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("path escapes workspace: %s", e.Path)
	}
	e.Path = cleaned

	switch e.Kind {
	case "replace":
		if e.OldText == "" {
			return fmt.Errorf("old_text is required for replace")
		}
		if e.ExpectedMatches == 0 {
			e.ExpectedMatches = 1
		}
		if e.ExpectedMatches < 0 {
			return fmt.Errorf("expected_matches must be positive")
		}
	case "create":
		// Content may be empty for creating an empty file.
	}
	return nil
}

// stagedFile holds the in-memory result of preflighting edits for one file.
type stagedFile struct {
	path     string
	content  []byte
	mode     os.FileMode
	isCreate bool
}

// preflight validates all edits against current file state and produces
// staged in-memory results. It returns an error if any edit conflicts.
func (s *Service) preflight(ctx context.Context, edits []Edit) ([]stagedFile, error) {
	// Group edits by path for multi-edit staging.
	type fileEdits struct {
		path  string
		edits []Edit
	}
	seen := map[string]int{} // path → index in groups
	var groups []fileEdits

	for _, e := range edits {
		if idx, ok := seen[e.Path]; ok {
			groups[idx].edits = append(groups[idx].edits, e)
		} else {
			seen[e.Path] = len(groups)
			groups = append(groups, fileEdits{path: e.Path, edits: []Edit{e}})
		}
	}

	var staged []stagedFile
	for _, g := range groups {
		sf, err := s.preflightFile(ctx, g.path, g.edits)
		if err != nil {
			return nil, fmt.Errorf("file %s: %w", g.path, err)
		}
		staged = append(staged, sf)
	}
	return staged, nil
}

// preflightFile validates and stages edits for a single file.
func (s *Service) preflightFile(ctx context.Context, path string, edits []Edit) (stagedFile, error) {
	absPath := filepath.Join(s.workspace, path)

	// Check for symlink escape.
	resolved, err := filepath.EvalSymlinks(filepath.Dir(absPath))
	if err == nil {
		if !strings.HasPrefix(resolved, s.workspace) {
			return stagedFile{}, fmt.Errorf("symlink target escapes workspace")
		}
	}

	// Determine whether this is all creates, all replaces, or mixed.
	hasCreate := false
	hasReplace := false
	for _, e := range edits {
		switch e.Kind {
		case "create":
			hasCreate = true
		case "replace":
			hasReplace = true
		}
	}

	if hasCreate && hasReplace {
		return stagedFile{}, fmt.Errorf("cannot mix create and replace for the same file")
	}

	if hasCreate {
		if len(edits) > 1 {
			return stagedFile{}, fmt.Errorf("multiple create edits for the same file")
		}
		// Check that the file does not exist.
		fs, readErr := s.deps.FileAccess.Read(ctx, path)
		if readErr == nil && fs.Exists {
			return stagedFile{}, fmt.Errorf("file already exists; use replace instead")
		}
		return stagedFile{
			path:     path,
			content:  []byte(edits[0].Content),
			mode:     0644,
			isCreate: true,
		}, nil
	}

	// Replace edits: read current content and apply each edit in order.
	fs, err := s.deps.FileAccess.Read(ctx, path)
	if err != nil {
		return stagedFile{}, fmt.Errorf("cannot read: %w", err)
	}
	if !fs.Exists {
		return stagedFile{}, fmt.Errorf("file does not exist")
	}

	content := string(fs.Bytes)
	for _, e := range edits {
		count := strings.Count(content, e.OldText)
		if count != e.ExpectedMatches {
			return stagedFile{}, fmt.Errorf("expected %d matches of old_text, found %d", e.ExpectedMatches, count)
		}
		content = strings.Replace(content, e.OldText, e.NewText, e.ExpectedMatches)
	}

	return stagedFile{
		path:    path,
		content: []byte(content),
		mode:    fs.Mode,
	}, nil
}

// apply writes the staged files. It returns changed, uncertain files,
// and any error. It continues writing after individual failures.
func (s *Service) apply(ctx context.Context, staged []stagedFile) (changed, uncertain []string, err error) {
	for _, sf := range staged {
		if ctx.Err() != nil {
			uncertain = append(uncertain, sf.path)
			continue
		}
		writeErr := s.deps.FileAccess.Write(ctx, sf.path, FileState{
			Exists: true,
			Bytes:  sf.content,
			Mode:   sf.mode,
		})
		if writeErr != nil {
			uncertain = append(uncertain, sf.path)
			if err == nil {
				err = fmt.Errorf("write %s: %w", sf.path, writeErr)
			}
		} else {
			changed = append(changed, sf.path)
		}
	}
	return
}

// runChecks executes preset checks sequentially.
func (s *Service) runChecks(ctx context.Context, preset *Preset) ([]CheckResult, TerminationReason) {
	results := make([]CheckResult, 0, len(preset.Checks))
	termination := TerminationCompleted

	for _, check := range preset.Checks {
		if ctx.Err() != nil {
			// Deadline or cancellation.
			if ctx.Err() == context.DeadlineExceeded {
				termination = TerminationDeadline
			} else {
				termination = TerminationCancelled
			}
			results = append(results, CheckResult{
				ID:     check.ID,
				Status: CheckNotRun,
			})
			continue
		}

		// Check prerequisites (depends_on).
		if blocked := s.isBlocked(check, results); blocked {
			results = append(results, CheckResult{
				ID:     check.ID,
				Status: CheckBlocked,
			})
			continue
		}

		cmd := Command{
			ID:   check.ID,
			Argv: check.Argv,
			Cwd:  check.Cwd,
		}

		exec := s.deps.CommandRunner.Run(ctx, cmd)

		cr := CheckResult{
			ID:       check.ID,
			Duration: exec.Duration,
		}

		switch {
		case exec.StartupErr != nil:
			cr.Status = CheckFailed
			cr.Diagnostics = []string{fmt.Sprintf("startup error: %v", exec.StartupErr)}
		case exec.TimedOut:
			cr.Status = CheckTimedOut
			termination = TerminationDeadline
		case exec.Cancelled:
			cr.Status = CheckCancelled
			termination = TerminationCancelled
		case exec.ExitCode != nil && *exec.ExitCode == 0:
			cr.Status = CheckPassed
			cr.ExitCode = exec.ExitCode
		default:
			cr.Status = CheckFailed
			cr.ExitCode = exec.ExitCode
			// Extract representative diagnostics from output.
			cr.Diagnostics = extractDiagnostics(exec.Output)
		}
		// Store raw output for evidence retention.
		if cr.Status == CheckFailed || cr.Status == CheckTimedOut {
			cr.Output = exec.Output
			if len(exec.Output) > 500 {
				cr.OmittedDetail = fmt.Sprintf("full output (%d bytes) available via tzro expand", len(exec.Output))
			}
		}

		results = append(results, cr)
	}

	return results, termination
}

// isBlocked checks whether a check's prerequisites have all passed.
func (s *Service) isBlocked(check PresetCheck, results []CheckResult) bool {
	if len(check.DependsOn) == 0 {
		return false
	}
	for _, dep := range check.DependsOn {
		found := false
		for _, r := range results {
			if r.ID == dep {
				found = true
				if r.Status != CheckPassed {
					return true
				}
			}
		}
		if !found {
			return true // dependency not yet executed
		}
	}
	return false
}

// overallVerification determines the aggregate verification status.
func (s *Service) overallVerification(checks []CheckResult, termination TerminationReason) VerificationStatus {
	if len(checks) == 0 {
		return VerificationNotConfigured
	}

	allPassed := true
	anyFailed := false
	for _, c := range checks {
		if c.Status != CheckPassed {
			allPassed = false
		}
		if c.Status == CheckFailed {
			anyFailed = true
		}
	}

	if allPassed {
		return VerificationPassed
	}
	if anyFailed {
		return VerificationFailed
	}
	return VerificationIncomplete
}

// retainEvidence stores check output for later expansion.
func (s *Service) retainEvidence(ctx context.Context, checks []CheckResult) (RetentionStatus, string) {
	if s.deps.EvidenceStore == nil {
		return RetentionUnavailable, "no evidence store configured"
	}

	retained := 0
	for i := range checks {
		if checks[i].Status == CheckPassed || checks[i].Output == nil {
			continue
		}
		if len(checks[i].Output) < 500 {
			// Small output — no need to store separately.
			continue
		}
		ref, err := s.deps.EvidenceStore.Retain(ctx, Evidence{
			CheckID: checks[i].ID,
			Output:  checks[i].Output,
		})
		if err != nil {
			continue // Best effort; don't fail the whole operation.
		}
		checks[i].EvidenceRef = &ref
		retained++
	}

	if retained == 0 {
		return RetentionNotNeeded, ""
	}
	return RetentionRetained, ""
}

// deadlineOrCancelSummary returns a summary for context expiry.
func (s *Service) deadlineOrCancelSummary(err error) Summary {
	termination := TerminationCancelled
	if err == context.DeadlineExceeded {
		termination = TerminationDeadline
	}
	return Summary{
		Schema:          "tzro.verification.v1",
		Application:     ApplicationNotApplied,
		Verification:    VerificationUnavailable,
		Termination:     termination,
		Retention:       RetentionUnavailable,
		RetentionReason: fmt.Sprintf("operation stopped: %v", err),
	}
}

// extractDiagnostics extracts representative lines from command output.
func extractDiagnostics(output []byte) []string {
	if len(output) == 0 {
		return nil
	}
	lines := strings.Split(string(output), "\n")
	var diagnostics []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Keep lines that look like diagnostics (contain FAIL, Error, error, panic, etc.)
		lower := strings.ToLower(line)
		if strings.Contains(lower, "fail") || strings.Contains(lower, "error") ||
			strings.Contains(lower, "panic") || strings.Contains(lower, "--- fail") {
			diagnostics = append(diagnostics, line)
		}
	}
	// Cap at 10 representative diagnostics.
	if len(diagnostics) > 10 {
		diagnostics = diagnostics[:10]
	}
	// If no diagnostic lines found, take first few lines.
	if len(diagnostics) == 0 && len(lines) > 0 {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				diagnostics = append(diagnostics, line)
				if len(diagnostics) >= 5 {
					break
				}
			}
		}
	}
	return diagnostics
}
