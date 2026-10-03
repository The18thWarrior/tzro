package pricing

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.001
}

// Private grading: verify the rename was done correctly and nothing is broken.
func TestGrading_RenameComplete(t *testing.T) {
	// Must be callable as CalculateTotal.
	result := CalculateTotal(200.0, 15.0, 10.0)
	// (200 * 0.85) * 1.10 = 170 * 1.10 = 187.0
	want := 187.0
	if !almostEqual(result, want) {
		t.Errorf("CalculateTotal(200, 15, 10) = %v, want %v", result, want)
	}
}

func TestGrading_OrderUsesNewName(t *testing.T) {
	items := []float64{25.0, 75.0}
	got := OrderTotal(items, 20.0, 8.0)
	// (100 * 0.8) * 1.08 = 80 * 1.08 = 86.4
	want := 86.4
	if !almostEqual(got, want) {
		t.Errorf("OrderTotal = %v, want %v", got, want)
	}
}

func TestGrading_FormatStillWorks(t *testing.T) {
	if got := FormatPrice(42.50); got != "42.50" {
		t.Errorf("FormatPrice(42.50) = %q, want 42.50", got)
	}
}
