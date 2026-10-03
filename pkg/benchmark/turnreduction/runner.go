// Package turnreduction implements the turn-reduction evaluation framework.
//
// The framework measures Verified Completion Time across three conditions:
// - Native: The agent uses ordinary tools (editor, terminal, etc.)
// - Simple: The agent has a bulk-edit helper (scripts/bulk_update.py)
// - Tzro: The agent has tzro_edit_and_verify through MCP
//
// Each condition runs the same 9 fixtures (3 languages × 3 task shapes)
// and produces a 27-cell matrix of grading outcomes.
package turnreduction

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"tzro/pkg/verification"
)

// Fixture defines a single evaluation task.
type Fixture struct {
	Version  int    `json:"version"`
	ID       string `json:"id"`
	Language string `json:"language"` // "go", "python", "typescript"
	Shape    string `json:"shape"`    // "bugfix", "rename", "diagnosis"

	// SubjectDir is the directory containing the broken source code.
	SubjectDir string `json:"subject_dir"`
	// GradingDir is the directory containing private grading tests.
	GradingDir string `json:"grading_dir"`

	// Prompt is the task description given to the agent.
	Prompt string `json:"prompt"`

	EditableFiles      []string            `json:"editable_files"`
	RequiredGradeTests []string            `json:"required_grade_tests"`
	ReferenceEdits     []verification.Edit `json:"reference_edits"`
}

// Condition represents one of the three evaluation conditions.
type Condition string

const (
	ConditionNative Condition = "native"
	ConditionSimple Condition = "simple"
	ConditionTzro   Condition = "tzro"
)

// LoadFixtures loads all fixture manifests from the given directory.
func LoadFixtures(fixturesDir string) ([]Fixture, error) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		return nil, err
	}

	var fixtures []Fixture
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		subjectDir := filepath.Join(fixturesDir, e.Name(), "subject")
		gradingDir := filepath.Join(fixturesDir, e.Name(), "grading")

		if _, err := os.Stat(subjectDir); os.IsNotExist(err) {
			continue
		}

		var fixture Fixture
		manifest := filepath.Join(fixturesDir, e.Name(), "fixture.json")
		if data, err := os.ReadFile(manifest); err == nil {
			if err := json.Unmarshal(data, &fixture); err != nil {
				return nil, fmt.Errorf("read fixture contract %s: %w", manifest, err)
			}
			if fixture.ID != e.Name() || fixture.Version != 2 {
				return nil, fmt.Errorf("invalid fixture identity or version: %s", manifest)
			}
			fixture.SubjectDir, fixture.GradingDir = subjectDir, gradingDir
		} else {
			return nil, fmt.Errorf("read required fixture contract: %w", err)
		}
		fixtures = append(fixtures, fixture)
	}

	return fixtures, nil
}
