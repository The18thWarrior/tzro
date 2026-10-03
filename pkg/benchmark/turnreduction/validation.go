package turnreduction

import (
	"context"
	"fmt"
	"os"
	"time"

	"tzro/pkg/verification"
)

type FixtureValidation struct {
	ID                string                `json:"id"`
	Passed            bool                  `json:"passed"`
	BrokenGradeLog    string                `json:"broken_grade_log"`
	ReferenceGradeLog string                `json:"reference_grade_log"`
	ReferenceChecks   *verification.Summary `json:"reference_checks,omitempty"`
	Error             string                `json:"error,omitempty"`
}

type ValidationReport struct {
	Passed   bool                `json:"passed"`
	Fixtures []FixtureValidation `json:"fixtures"`
}

// ValidateFixtures proves each starting failure and reference pass offline.
// The reference edits and private tests never enter an agent workspace.
func ValidateFixtures(ctx context.Context, fixtures []Fixture) ValidationReport {
	report := ValidationReport{Passed: len(fixtures) > 0}
	for _, f := range fixtures {
		result := validateFixture(ctx, f)
		report.Fixtures = append(report.Fixtures, result)
		report.Passed = report.Passed && result.Passed
	}
	return report
}

func validateFixture(ctx context.Context, f Fixture) (result FixtureValidation) {
	result.ID = f.ID
	if f.Version != 2 || f.Prompt == "" || len(f.ReferenceEdits) == 0 || len(f.EditableFiles) == 0 || len(f.RequiredGradeTests) == 0 {
		result.Error = "missing version 2 fixture contract, explicit prompt, editable files, or reference edits"
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	ws, err := os.MkdirTemp("", "tzro-reference-*")
	if err != nil {
		result.Error = err.Error()
		return
	}
	defer os.RemoveAll(ws)
	if err = copyDirContext(ctx, f.SubjectDir, ws); err != nil {
		result.Error = err.Error()
		return
	}
	passed, log, err := GradeFixture(ctx, f, ws)
	result.BrokenGradeLog = log
	if passed || err != nil {
		result.Error = fmt.Sprintf("starting fixture must fail its private assertions: passed=%v error=%v", passed, err)
		return
	}
	fa, err := verification.NewWorkspaceFileAccess(ws)
	if err != nil {
		result.Error = err.Error()
		return
	}
	svc := verification.NewService(ws, verification.Dependencies{FileAccess: fa, CommandRunner: verification.NewExactCommandRunner(ws)})
	summary := svc.ApplyAndVerify(ctx, verification.Request{Edits: f.ReferenceEdits}, verification.Options{})
	result.ReferenceChecks = &summary
	if summary.Application != verification.ApplicationApplied || summary.Verification != verification.VerificationPassed || len(summary.Checks) == 0 {
		result.Error = "reference edits must apply and pass every preset check"
		return
	}
	passed, log, err = GradeFixture(ctx, f, ws)
	result.ReferenceGradeLog = log
	if !passed || err != nil {
		result.Error = fmt.Sprintf("reference must pass private assertions: passed=%v error=%v", passed, err)
		return
	}
	result.Passed = true
	return
}
