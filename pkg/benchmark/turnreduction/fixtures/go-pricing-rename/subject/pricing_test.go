package pricing

import "testing"

// Tests use the new name CalculateTotal — they fail until the rename is done.
func TestCalculateTotal_BasicDiscount(t *testing.T) {
	got := CalculateTotal(100.0, 10.0, 0.0)
	want := 90.0
	if got != want {
		t.Errorf("CalculateTotal(100, 10, 0) = %v, want %v", got, want)
	}
}

func TestCalculateTotal_WithTax(t *testing.T) {
	got := CalculateTotal(100.0, 0.0, 8.0)
	want := 108.0
	if got != want {
		t.Errorf("CalculateTotal(100, 0, 8) = %v, want %v", got, want)
	}
}

func TestOrderTotal(t *testing.T) {
	items := []float64{10.0, 20.0, 30.0}
	got := OrderTotal(items, 10.0, 5.0)
	// (60 * 0.9) * 1.05 = 54 * 1.05 = 56.7
	want := 56.7
	if got != want {
		t.Errorf("OrderTotal = %v, want %v", got, want)
	}
}

func TestFormatPrice(t *testing.T) {
	tests := []struct {
		price float64
		want  string
	}{
		{10.00, "10.00"},
		{99.99, "99.99"},
		{0.50, "0.50"},
	}
	for _, tt := range tests {
		got := FormatPrice(tt.price)
		if got != tt.want {
			t.Errorf("FormatPrice(%v) = %q, want %q", tt.price, got, tt.want)
		}
	}
}
