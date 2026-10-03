// Package verification implements the grouped edit-and-verify operation
// for Tzro's Automatic Verification workflow. It applies a batch of literal
// text edits and executes a repository-defined Verification Preset,
// returning a mandatory Verification Summary.
package verification

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Request contains the edits to apply atomically.
type Request struct {
	Edits []Edit `json:"edits"`
}

// Edit describes a single file modification.
type Edit struct {
	Kind            string `json:"kind"`                       // "replace" or "create"
	Path            string `json:"path"`                       // workspace-relative path
	OldText         string `json:"old_text,omitempty"`         // text to find (replace only)
	NewText         string `json:"new_text,omitempty"`         // replacement text (replace only)
	ExpectedMatches int    `json:"expected_matches,omitempty"` // exact match count; default 1
	Content         string `json:"content,omitempty"`          // file content (create only)
}

// Options configures the operation.
type Options struct {
	Timeout            time.Duration // default 5m
	SummaryTargetBytes int           // default 8000
}

func (o Options) effectiveTimeout() time.Duration {
	if o.Timeout <= 0 {
		return 5 * time.Minute
	}
	return o.Timeout
}

func (o Options) effectiveSummaryTarget() int {
	if o.SummaryTargetBytes <= 0 {
		return 8000
	}
	return o.SummaryTargetBytes
}

// ApplicationStatus describes the outcome of the edit batch.
type ApplicationStatus string

const (
	ApplicationApplied    ApplicationStatus = "applied"
	ApplicationRejected   ApplicationStatus = "rejected"
	ApplicationNotApplied ApplicationStatus = "not_applied"
	ApplicationPartial    ApplicationStatus = "partial"
)

// VerificationStatus describes the outcome of check execution.
type VerificationStatus string

const (
	VerificationPassed        VerificationStatus = "passed"
	VerificationFailed        VerificationStatus = "failed"
	VerificationIncomplete    VerificationStatus = "incomplete"
	VerificationNotConfigured VerificationStatus = "not_configured"
	VerificationUnavailable   VerificationStatus = "unavailable"
)

// CheckStatus describes the outcome of a single check command.
type CheckStatus string

const (
	CheckPassed    CheckStatus = "passed"
	CheckFailed    CheckStatus = "failed"
	CheckBlocked   CheckStatus = "blocked"
	CheckNotRun    CheckStatus = "not_run"
	CheckTimedOut  CheckStatus = "timed_out"
	CheckCancelled CheckStatus = "cancelled"
)

// TerminationReason explains why the operation stopped.
type TerminationReason string

const (
	TerminationCompleted        TerminationReason = "completed"
	TerminationDeadline         TerminationReason = "deadline"
	TerminationCancelled        TerminationReason = "cancelled"
	TerminationApplicationError TerminationReason = "application_error"
)

// RetentionStatus indicates whether evidence was stored.
type RetentionStatus string

const (
	RetentionRetained    RetentionStatus = "retained"
	RetentionNotNeeded   RetentionStatus = "not_needed"
	RetentionUnavailable RetentionStatus = "unavailable"
)

// CheckResult holds the outcome of one preset check command.
type CheckResult struct {
	ID               string        `json:"id"`
	Status           CheckStatus   `json:"status"`
	ExitCode         *int          `json:"exit_code,omitempty"`
	Duration         time.Duration `json:"duration_ms"`
	DiagnosticCounts *string       `json:"diagnostic_counts,omitempty"` // "unknown" or "3 failed, 5 passed"
	Diagnostics      []string      `json:"diagnostics,omitempty"`       // representative primary diagnostics
	OmittedDetail    string        `json:"omitted_detail,omitempty"`    // notice when detail is omitted
	EvidenceRef      *EvidenceRef  `json:"evidence_ref,omitempty"`
	Output           []byte        `json:"-"` // raw output for evidence retention; not serialized
}

// Summary is the mandatory structured result of an edit-and-verify operation.
type Summary struct {
	Schema          string             `json:"schema"`
	Application     ApplicationStatus  `json:"application"`
	ChangedFiles    []string           `json:"changed_files"`
	UncertainFiles  []string           `json:"uncertain_files,omitempty"`
	Verification    VerificationStatus `json:"verification"`
	Checks          []CheckResult      `json:"checks,omitempty"`
	Termination     TerminationReason  `json:"termination"`
	Retention       RetentionStatus    `json:"retention"`
	RetentionReason string             `json:"retention_reason,omitempty"`
	RejectionReason string             `json:"rejection_reason,omitempty"`
	Error           string             `json:"error,omitempty"`
}

// ToMap converts the Summary to a generic map for structured MCP output.
func (s Summary) ToMap() map[string]interface{} {
	data, err := json.Marshal(s)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return m
}

// FormatText renders a human-readable summary.
func (s Summary) FormatText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema: %s\n", s.Schema)
	fmt.Fprintf(&b, "application: %s\n", s.Application)
	if len(s.ChangedFiles) > 0 {
		fmt.Fprintf(&b, "changed_files: %s\n", strings.Join(s.ChangedFiles, ", "))
	}
	if len(s.UncertainFiles) > 0 {
		fmt.Fprintf(&b, "uncertain_files: %s\n", strings.Join(s.UncertainFiles, ", "))
	}
	fmt.Fprintf(&b, "verification: %s\n", s.Verification)
	for _, c := range s.Checks {
		fmt.Fprintf(&b, "  check %s: %s", c.ID, c.Status)
		if c.ExitCode != nil {
			fmt.Fprintf(&b, " (exit %d)", *c.ExitCode)
		}
		if c.Duration > 0 {
			fmt.Fprintf(&b, " [%s]", c.Duration.Round(time.Millisecond))
		}
		b.WriteString("\n")
		for _, d := range c.Diagnostics {
			fmt.Fprintf(&b, "    %s\n", d)
		}
		if c.OmittedDetail != "" {
			fmt.Fprintf(&b, "    [%s]\n", c.OmittedDetail)
		}
	}
	fmt.Fprintf(&b, "termination: %s\n", s.Termination)
	fmt.Fprintf(&b, "retention: %s\n", s.Retention)
	if s.RetentionReason != "" {
		fmt.Fprintf(&b, "retention_reason: %s\n", s.RetentionReason)
	}
	if s.RejectionReason != "" {
		fmt.Fprintf(&b, "rejection_reason: %s\n", s.RejectionReason)
	}
	if s.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", s.Error)
	}
	return b.String()
}

// FileState holds a file's content and metadata.
type FileState struct {
	Exists bool
	Bytes  []byte
	Mode   os.FileMode
}

// FileAccess provides workspace-bound file operations.
type FileAccess interface {
	Read(ctx context.Context, path string) (FileState, error)
	Write(ctx context.Context, path string, state FileState) error
}

// Command describes an exact-argument command to execute.
type Command struct {
	ID   string   // check ID from the preset
	Argv []string // exact arguments, no shell interpolation
	Cwd  string   // working directory (workspace-relative)
}

// Execution holds the result of a command run.
type Execution struct {
	ExitCode   *int
	Output     []byte
	StartupErr error
	Cancelled  bool
	TimedOut   bool
	Duration   time.Duration
}

// CommandRunner executes preset check commands.
type CommandRunner interface {
	Run(ctx context.Context, cmd Command) Execution
}

// Evidence is the data to retain in the store.
type Evidence struct {
	CheckID string
	Output  []byte
}

// EvidenceRef references retained evidence in the store.
type EvidenceRef struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Retained  bool   `json:"retained"`
}

// EvidenceStore retains full check output for later expansion.
type EvidenceStore interface {
	Retain(ctx context.Context, evidence Evidence) (EvidenceRef, error)
}

// Dependencies groups the service's external collaborators.
type Dependencies struct {
	FileAccess    FileAccess
	CommandRunner CommandRunner
	EvidenceStore EvidenceStore
}
