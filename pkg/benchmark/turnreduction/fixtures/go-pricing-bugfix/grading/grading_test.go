package pricing

import (
	"math"
	"testing"
)

// Private grading test — agent must not see this file.
// Validates that the fix correctly rounds to the nearest cent at all
// known boundary values, and doesn't introduce regressions.

func TestGrading_RoundingBoundaries(t *testing.T) {
	boundaries := []struct {
		base     float64
		discount float64
		want     float64
	}{
		// The original bug case.
		{10.05, 10, 9.05},
		// Additional boundary cases to catch partial fixes.
		{10.15, 10, 9.14}, // 10.15 * 0.9 = 9.135 → 9.14
		{7.77, 15, 6.60},  // 7.77 * 0.85 = 6.6045 → 6.60
		{0.01, 50, 0.01},  // 0.01 * 0.5 = 0.005 → 0.01
		{99.99, 1, 98.99}, // 99.99 * 0.99 = 98.9901 → 98.99
		// Standard values (regression check).
		{100.00, 0, 100.00},
		{100.00, 10, 90.00},
		{100.00, 100, 0.00},
	}

	for _, b := range boundaries {
		got := DiscountedPrice(b.base, b.discount)
		if got != b.want {
			t.Errorf("DiscountedPrice(%v, %v) = %v, want %v", b.base, b.discount, got, b.want)
		}
	}
}

// TestGrading_UsesRound verifies the implementation uses proper rounding.
func TestGrading_UsesRound(t *testing.T) {
	// 0.005 boundary: math.Round(0.5) = 1, math.Floor(0.5) = 0.
	// Price: 0.01 at 50% discount = 0.005, should round to 0.01.
	got := DiscountedPrice(0.01, 50)
	want := math.Round(0.01*0.5*100) / 100
	if got != want {
		t.Errorf("rounding validation: got %v, want %v", got, want)
	}
}
