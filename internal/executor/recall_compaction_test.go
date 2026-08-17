package executor

import (
	"testing"

	"tzro/internal/memory"
)

// --- Slice 4 RED (Run 32 fix): Stratified Step Manifest Windowing ---

// makeSteps creates a slice of n ThoughtSteps with sequential StepIndex values.
func makeSteps(n int) []memory.ThoughtStep {
	steps := make([]memory.ThoughtStep, n)
	for i := range steps {
		steps[i] = memory.ThoughtStep{
			StepIndex:  i,
			ToolName:   "read_file",
			ToolOutput: "output from step " + string(rune('A'+i%26)),
		}
	}
	return steps
}

// TestWindowThoughtSteps_SmallSet_AllIncluded verifies that when len(steps) ≤ max,
// all steps are returned unchanged.
func TestWindowThoughtSteps_SmallSet_AllIncluded(t *testing.T) {
	steps := makeSteps(10)
	got := windowThoughtSteps(steps, 25)
	if len(got) != 10 {
		t.Errorf("expected all 10 steps returned, got %d", len(got))
	}
}

// TestWindowThoughtSteps_ExactlyMax_AllIncluded verifies that exactly max steps
// are returned unchanged (boundary condition).
func TestWindowThoughtSteps_ExactlyMax_AllIncluded(t *testing.T) {
	steps := makeSteps(25)
	got := windowThoughtSteps(steps, 25)
	if len(got) != 25 {
		t.Errorf("expected all 25 steps returned, got %d", len(got))
	}
}

// TestWindowThoughtSteps_LargeSet_CappedAt25 verifies that when len(steps) > 25,
// the result is exactly 25 steps.
func TestWindowThoughtSteps_LargeSet_CappedAt25(t *testing.T) {
	steps := makeSteps(60)
	got := windowThoughtSteps(steps, 25)
	if len(got) != 25 {
		t.Errorf("expected 25 steps after windowing, got %d", len(got))
	}
}

// TestWindowThoughtSteps_HeadAlwaysPresent verifies that the first 5 steps
// (head) are always included in the result.
func TestWindowThoughtSteps_HeadAlwaysPresent(t *testing.T) {
	steps := makeSteps(60)
	got := windowThoughtSteps(steps, 25)

	indexSet := make(map[int]bool)
	for _, s := range got {
		indexSet[s.StepIndex] = true
	}
	for i := 0; i < 5; i++ {
		if !indexSet[i] {
			t.Errorf("expected head step %d to be present after windowing", i)
		}
	}
}

// TestWindowThoughtSteps_TailAlwaysPresent verifies that the last 2 steps
// are always included in the result.
func TestWindowThoughtSteps_TailAlwaysPresent(t *testing.T) {
	steps := makeSteps(60)
	got := windowThoughtSteps(steps, 25)

	indexSet := make(map[int]bool)
	for _, s := range got {
		indexSet[s.StepIndex] = true
	}
	for _, tail := range []int{58, 59} {
		if !indexSet[tail] {
			t.Errorf("expected tail step %d to be present after windowing", tail)
		}
	}
}

// TestWindowThoughtSteps_OrderPreserved verifies that the returned steps are
// in ascending StepIndex order.
func TestWindowThoughtSteps_OrderPreserved(t *testing.T) {
	steps := makeSteps(60)
	got := windowThoughtSteps(steps, 25)
	for i := 1; i < len(got); i++ {
		if got[i].StepIndex <= got[i-1].StepIndex {
			t.Errorf("steps out of order: got[%d].StepIndex=%d <= got[%d].StepIndex=%d",
				i, got[i].StepIndex, i-1, got[i-1].StepIndex)
		}
	}
}
