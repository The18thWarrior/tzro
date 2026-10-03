package turnreduction

import (
	"fmt"
	"math"
	"testing"
)

func TestEvaluateMatrix_UsesLowerEligibleWholeSuiteTotal(t *testing.T) {
	fixtures, cells := passingMatrix()
	got := EvaluateMatrix(fixtures, cells, false)
	if got.Status != "screen_pass" || got.Comparator != ConditionSimple || got.SpeedReductionPct == nil || math.Abs(*got.SpeedReductionPct-20) > 1e-9 {
		t.Fatalf("exact 20 percent threshold must use the lower complete comparator: %+v", got)
	}
	if got.Confirmed {
		t.Fatal("a single pass cannot confirm a causal speed improvement")
	}
}

func passingMatrix() ([]Fixture, []Observation) {
	var fixtures []Fixture
	var cells []Observation
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("fixture-%d", i)
		fixtures = append(fixtures, Fixture{ID: id})
		for _, pair := range []struct {
			condition Condition
			seconds   float64
		}{{ConditionNative, 12}, {ConditionSimple, 10}, {ConditionTzro, 8}} {
			seconds := pair.seconds
			cells = append(cells, Observation{FixtureID: id, Condition: pair.condition, Passed: true, VerifiedCompletionSeconds: &seconds})
		}
	}
	return fixtures, cells
}

func TestEvaluateMatrix_FailuresAndMissingEvidenceCannotImproveSpeed(t *testing.T) {
	for _, test := range []struct {
		name, status string
		change       func([]Observation) []Observation
		offline      bool
	}{
		{"Tzro failure", "incorrect", func(c []Observation) []Observation { c[2].Passed = false; return c }, false},
		{"both controls fail", "ineligible_control", func(c []Observation) []Observation { c[0].Passed = false; c[1].Passed = false; return c }, false},
		{"missing cell", "incomplete", func(c []Observation) []Observation { return c[1:] }, false},
		{"duplicate cell", "incomplete", func(c []Observation) []Observation { return append(c, c[0]) }, false},
		{"missing verified time", "incorrect", func(c []Observation) []Observation { c[2].VerifiedCompletionSeconds = nil; return c }, false},
		{"offline evidence", "offline_only", func(c []Observation) []Observation { return c }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, c := passingMatrix()
			got := EvaluateMatrix(f, test.change(c), test.offline)
			if got.Status != test.status || got.Confirmed {
				t.Fatalf("invalid gate result: %+v", got)
			}
		})
	}
	f, c := passingMatrix()
	c[1].Passed = false
	got := EvaluateMatrix(f, c, false)
	if got.Comparator != ConditionNative || got.Status != "screen_pass" {
		t.Fatalf("eligible Native control was discarded: %+v", got)
	}
}

func TestBalancedSchedule_InterleavesAndBalancesConditionPositions(t *testing.T) {
	fixtures, _ := passingMatrix()
	schedule := BalancedSchedule(fixtures, []Condition{ConditionNative, ConditionSimple, ConditionTzro})
	if len(schedule) != 27 {
		t.Fatalf("wrong schedule size: %d", len(schedule))
	}
	positions := map[Condition][3]int{}
	for i, cell := range schedule {
		if cell.FixtureID != fixtures[i/3].ID {
			t.Fatal("matched conditions must stay adjacent")
		}
		counts := positions[cell.Condition]
		counts[i%3]++
		positions[cell.Condition] = counts
	}
	for condition, counts := range positions {
		if counts != [3]int{3, 3, 3} {
			t.Fatalf("unbalanced condition %s: %v", condition, counts)
		}
	}
}
