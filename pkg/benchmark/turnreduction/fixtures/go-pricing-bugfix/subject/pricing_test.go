package pricing

import "testing"

func TestDiscountedPrice(t *testing.T) {
	tests := []struct {
		name     string
		base     float64
		discount float64
		want     float64
	}{
		{"no discount", 100.00, 0, 100.00},
		{"10% off 100", 100.00, 10, 90.00},
		{"15% off 19.99", 19.99, 15, 16.99},
		// This is the boundary case that exposes the bug:
		// 10.05 * 0.90 = 9.045, which should round to 9.05, not truncate to 9.04
		{"10% off 10.05 boundary", 10.05, 10, 9.05},
		{"20% off 33.33", 33.33, 20, 26.66},
		{"negative discount ignored", 50.00, -5, 50.00},
		{"over 100 discount ignored", 50.00, 105, 50.00},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiscountedPrice(tt.base, tt.discount)
			if got != tt.want {
				t.Errorf("DiscountedPrice(%v, %v) = %v, want %v", tt.base, tt.discount, got, tt.want)
			}
		})
	}
}

func TestBulkDiscount(t *testing.T) {
	tests := []struct {
		quantity int
		want     float64
	}{
		{1, 0},
		{10, 10},
		{50, 15},
		{100, 20},
	}
	for _, tt := range tests {
		if got := BulkDiscount(tt.quantity); got != tt.want {
			t.Errorf("BulkDiscount(%d) = %v, want %v", tt.quantity, got, tt.want)
		}
	}
}
